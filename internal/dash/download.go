package dash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/revunix/ard-plus-dl/internal/auth"
)

// mediaQueryMode describes how the manifest's query string reaches the media
// requests. ARD's CDN is not documented, so the downloader probes once per run:
// the segment URL as the manifest specifies it, and the same URL with the
// manifest query merged in. The mode that answers with a success status is used
// for every following request.
type mediaQueryMode int32

const (
	// queryModeAppended sends the manifest query verbatim with every media
	// request that has none of its own.
	queryModeAppended mediaQueryMode = 0
	// queryModeCookies sends the CloudFront parameters of the manifest query as
	// cookies, which is the only form ARD's media cache behaviour accepts.
	queryModeCookies mediaQueryMode = 1
	// queryModeAsIs requests exactly the URL the manifest specifies.
	queryModeAsIs mediaQueryMode = 2
)

func (m mediaQueryMode) String() string {
	switch m {
	case queryModeAppended:
		return "manifest-query"
	case queryModeCookies:
		return "manifest-cookies"
	default:
		return "as-is"
	}
}

// Downloader fetches the segments of one representation into a directory.
type Downloader struct {
	HTTP *http.Client
	// Workers bounds the parallel segment downloads (default 4).
	Workers int
	// Retries is the per-segment attempt count (default 3).
	Retries int
	UA      string

	// RequestTimeout bounds a single segment request (default 60s).
	RequestTimeout time.Duration

	// DebugDir, when set, receives the manifest query decisions and every
	// media request line for diagnosing CDN rejections.
	DebugDir string

	Logf func(format string, args ...any)

	// Query mode state: unknown until the first successful media request.
	queryProbed atomic.Bool
	queryMode   atomic.Int32

	debugMu    sync.Mutex
	debugLines int
}

func (d *Downloader) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
	}
}

func (d *Downloader) httpClient() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return http.DefaultClient
}

func (d *Downloader) ua() string {
	if d.UA != "" {
		return d.UA
	}
	return auth.DefaultUA
}

func (d *Downloader) workers() int {
	if d.Workers > 0 {
		return d.Workers
	}
	return 4
}

func (d *Downloader) retries() int {
	if d.Retries > 0 {
		return d.Retries
	}
	return 3
}

func (d *Downloader) requestTimeout() time.Duration {
	if d.RequestTimeout > 0 {
		return d.RequestTimeout
	}
	return 60 * time.Second
}

// debugf appends one diagnostic line; the log stays bounded.
func (d *Downloader) debugf(format string, args ...any) {
	if d.DebugDir == "" {
		return
	}
	d.debugMu.Lock()
	defer d.debugMu.Unlock()
	if d.debugLines >= 1000 {
		return
	}
	d.debugLines++
	file, err := os.OpenFile(filepath.Join(d.DebugDir, "requests.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, format+"\n", args...)
}

// candidateModes returns the query variants to try, in order. ARD's CDN signs
// the media paths with the parameters the manifest URL carries (CloudFront
// policy with a `.../full_movie/*` resource), so the appended variant is tried
// first; the bare URL is the fallback for distributions that serve segments
// unsigned.
func (d *Downloader) candidateModes(track *Track) []mediaQueryMode {
	if d.queryProbed.Load() {
		return []mediaQueryMode{mediaQueryMode(d.queryMode.Load())}
	}
	modes := []mediaQueryMode{queryModeAppended}
	if CloudFrontCookieHeader(track.baseQuery) != "" {
		modes = append(modes, queryModeCookies)
	}
	return append(modes, queryModeAsIs)
}

// mediaCookie returns the cookie header for a media request in the given mode.
func (d *Downloader) mediaCookie(track *Track, mode mediaQueryMode) string {
	if mode == queryModeCookies {
		return CloudFrontCookieHeader(track.baseQuery)
	}
	return ""
}

func (d *Downloader) rememberMode(mode mediaQueryMode, track *Track) {
	if !d.queryProbed.Swap(true) {
		d.queryMode.Store(int32(mode))
		d.logf("Medien-Anfragen nutzen den Modus: %s", mode)
		d.debugf("query mode resolved: %s (representation %s, manifest query keys: %s)",
			mode, track.ID, QueryKeys(track.baseQuery))
		if resource, expires, ok := CloudFrontPolicy(track.baseQuery); ok {
			d.debugf("cloudfront policy: resource=%s expires=%s", resource, expires.UTC().Format(time.RFC3339))
		}
	}
}

// Download resolves the segment list if necessary and downloads the track into
// dir. It returns the files in playback order (init first).
func (d *Downloader) Download(ctx context.Context, track *Track, dir string, progress func(done, total int)) ([]string, error) {
	if err := d.resolvePending(ctx, track); err != nil {
		return nil, err
	}
	if len(track.Segments) == 0 {
		return nil, fmt.Errorf("representation %s has no segments", track.ID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	type job struct {
		seg  Segment
		dest string
	}
	var files []string
	var jobs []job
	if track.Init != nil {
		dest := filepath.Join(dir, "init.mp4")
		files = append(files, dest)
		jobs = append(jobs, job{seg: *track.Init, dest: dest})
	}
	for i, seg := range track.Segments {
		dest := filepath.Join(dir, fmt.Sprintf("%05d.m4s", i+1))
		files = append(files, dest)
		jobs = append(jobs, job{seg: seg, dest: dest})
	}

	total := len(jobs)
	var done int64
	var reportMu sync.Mutex
	lastReport := time.Time{}
	report := func(force bool) {
		if progress == nil {
			return
		}
		reportMu.Lock()
		defer reportMu.Unlock()
		if !force && time.Since(lastReport) < 100*time.Millisecond {
			return
		}
		lastReport = time.Now()
		progress(int(atomic.LoadInt64(&done)), total)
	}
	report(true)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Resolve the query mode with one sequential request: a parallel sibling
	// that fails would otherwise cancel the probe before it can try the other
	// variant.
	if !d.queryProbed.Load() {
		probe := jobs[0]
		if err := d.fetchSegment(ctx, track, probe.seg, probe.dest); err != nil {
			return nil, err
		}
		atomic.AddInt64(&done, 1)
		report(true)
		jobs = jobs[1:]
		if len(jobs) == 0 {
			return files, nil
		}
	}

	jobCh := make(chan job)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range d.workers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobCh {
				if err := d.fetchSegment(ctx, track, item.seg, item.dest); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				atomic.AddInt64(&done, 1)
				report(false)
			}
		}()
	}
	go func() {
		defer close(jobCh)
		for _, item := range jobs {
			select {
			case jobCh <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	report(true)
	return files, nil
}

func (d *Downloader) resolvePending(ctx context.Context, track *Track) error {
	pending := track.pending
	if pending == nil {
		return nil
	}
	track.pending = nil

	var indexData []byte
	var lastErr error
	for _, mode := range d.candidateModes(track) {
		indexURL := d.mediaURL(pending.initURL, track, mode)
		data, err := d.fetchIndex(ctx, indexURL, pending.sidxRange, d.mediaCookie(track, mode))
		if err == nil {
			d.rememberMode(mode, track)
			indexData = data
			lastErr = nil
			break
		}
		lastErr = err
		if !probeOtherMode(err) {
			return err
		}
	}
	if indexData == nil {
		return lastErr
	}

	info, err := ParseSidx(indexData)
	if err != nil {
		return fmt.Errorf("representation %s: %w", track.ID, err)
	}

	if pending.byteRangeMode {
		base := int64(0)
		if pending.sidxRange != nil && pending.sidxRange.End >= 0 {
			base = pending.sidxRange.End + 1
		}
		base += info.FirstOffset
		offset := base
		segments := make([]Segment, 0, len(info.References))
		for _, ref := range info.References {
			if ref.ReferencedSize <= 0 {
				continue
			}
			segments = append(segments, Segment{
				URL:   pending.initURL,
				Range: &ByteRange{Start: offset, End: offset + ref.ReferencedSize - 1},
			})
			offset += ref.ReferencedSize
		}
		track.Segments = segments
		if track.Init == nil {
			initRange := pending.initRange
			if initRange == nil && pending.sidxRange != nil {
				initRange = &ByteRange{Start: 0, End: pending.sidxRange.Start - 1}
			}
			track.Init = &Segment{URL: pending.initURL, Range: initRange}
		}
		return nil
	}

	if pending.base == nil {
		return fmt.Errorf("representation %s: no base URL for the segment template", track.ID)
	}
	timeValue := info.EarliestPresentationTime
	segments := make([]Segment, 0, len(info.References))
	for i, ref := range info.References {
		media := expandTemplate(pending.mediaTemplate, pending.representationID, pending.bandwidth,
			pending.startNumber+int64(i), timeValue)
		segments = append(segments, Segment{URL: resolveSegmentURL(pending.base, media)})
		timeValue += ref.SubsegmentDuration
	}
	track.Segments = segments
	return nil
}

// mediaURL applies the query mode to one media URL. The manifest query is
// appended verbatim: CloudFront signatures are base64 values whose `+`, `/` and
// `=` must reach the CDN exactly as signed, so they are never re-encoded.
func (d *Downloader) mediaURL(rawURL string, track *Track, mode mediaQueryMode) string {
	if mode == queryModeAppended {
		return AppendQuery(rawURL, track.baseQuery)
	}
	return rawURL
}

func (d *Downloader) fetchSegment(ctx context.Context, track *Track, seg Segment, dest string) error {
	// A segment that was downloaded completely is kept and not fetched again.
	if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
		return nil
	}
	var lastErr error
	for _, mode := range d.candidateModes(track) {
		segURL := d.mediaURL(seg.URL, track, mode)
		err := d.fetchWithRetries(ctx, segURL, seg.Range, dest, mode, d.mediaCookie(track, mode))
		if err == nil {
			d.rememberMode(mode, track)
			return nil
		}
		lastErr = err
		if !probeOtherMode(err) {
			return err
		}
	}
	return lastErr
}

// probeOtherMode reports whether a failed request justifies trying the other
// query variant: the CDN rejected it as unauthorized. A 404 is a real manifest
// problem and fails immediately.
func probeOtherMode(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.Status == http.StatusUnauthorized || statusErr.Status == http.StatusForbidden
}

func (d *Downloader) fetchWithRetries(ctx context.Context, segURL string, byteRange *ByteRange, dest string, mode mediaQueryMode, cookie string) error {
	attempts := d.retries()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := d.fetchOnce(ctx, segURL, byteRange, dest, mode, cookie)
		if err == nil {
			return nil
		}
		lastErr = err
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && !statusErr.retryable() {
			return err
		}
		if attempt < attempts {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return lastErr
}

type httpStatusError struct {
	URL    string
	Status int
	// Mode names the query variant that was rejected, so a failing run is
	// diagnosable without seeing the token.
	Mode string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d [%s]", RedactURL(e.URL), e.Status, e.Mode)
}

func (e *httpStatusError) retryable() bool {
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

func (d *Downloader) fetchOnce(ctx context.Context, segURL string, byteRange *ByteRange, dest string, mode mediaQueryMode, cookie string) error {
	reqCtx, cancel := context.WithTimeout(ctx, d.requestTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, segURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", d.ua())
	if byteRange != nil {
		req.Header.Set("Range", byteRange.header())
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := d.httpClient().Do(req)
	if err != nil {
		d.debugf("GET %s range=%v cookie=%v -> transport error: %v", RedactURL(segURL), rangeLabel(byteRange), cookie != "", err)
		return err
	}
	defer resp.Body.Close()
	d.debugf("GET %s range=%v cookie=%v -> %d", RedactURL(segURL), rangeLabel(byteRange), cookie != "", resp.StatusCode)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &httpStatusError{URL: segURL, Status: resp.StatusCode, Mode: mode.String()}
	}

	part := dest + ".part"
	// The part file is written from the start: a half written segment cannot
	// be continued, because the expected length is not known for every
	// segment. The completed segments next to it are kept, so the download
	// continues at segment level.
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		_ = os.Remove(part)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	if byteRange != nil {
		if info, statErr := os.Stat(part); statErr == nil && info.Size() == 0 {
			_ = os.Remove(part)
			return fmt.Errorf("%s: empty ranged response", RedactURL(segURL))
		}
	}
	return os.Rename(part, dest)
}

// fetchIndex fetches the sidx box (optionally as a byte range).
func (d *Downloader) fetchIndex(ctx context.Context, rawURL string, byteRange *ByteRange, cookie string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, d.requestTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", d.ua())
	if byteRange != nil {
		req.Header.Set("Range", byteRange.header())
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := d.httpClient().Do(req)
	if err != nil {
		d.debugf("GET index %s range=%v -> transport error: %v", RedactURL(rawURL), rangeLabel(byteRange), err)
		return nil, err
	}
	defer resp.Body.Close()
	d.debugf("GET index %s range=%v -> %d", RedactURL(rawURL), rangeLabel(byteRange), resp.StatusCode)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, &httpStatusError{URL: rawURL, Status: resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func rangeLabel(r *ByteRange) string {
	if r == nil {
		return "full"
	}
	return r.header()
}

// header renders the range for the HTTP Range header.
func (r *ByteRange) header() string {
	if r.End < 0 {
		return fmt.Sprintf("bytes=%d-", r.Start)
	}
	return fmt.Sprintf("bytes=%d-%d", r.Start, r.End)
}

// AppendQuery returns segURL with baseQuery appended when the segment URL has
// none of its own.
func AppendQuery(segURL, baseQuery string) string {
	if baseQuery == "" {
		return segURL
	}
	parsed, err := url.Parse(segURL)
	if err != nil || parsed.RawQuery != "" {
		return segURL
	}
	parsed.RawQuery = baseQuery
	return parsed.String()
}

// RedactURL keeps query key names in place of their values; bare tokens without
// a key are hidden completely.
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery == "" {
		return raw
	}
	parts := strings.Split(parsed.RawQuery, "&")
	redacted := make([]string, 0, len(parts))
	for _, part := range parts {
		key, _, hasValue := strings.Cut(part, "=")
		if !hasValue || !isPlainKey(key) {
			redacted = append(redacted, "…")
			continue
		}
		redacted = append(redacted, key+"=…")
	}
	parsed.RawQuery = strings.Join(redacted, "&")
	return parsed.String()
}

func isPlainKey(key string) bool {
	if key == "" || len(key) > 32 {
		return false
	}
	for i := range len(key) {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// QueryKeys renders the key names of a raw query for the debug log.
func QueryKeys(rawQuery string) string {
	if rawQuery == "" {
		return "(none)"
	}
	var keys []string
	for _, part := range strings.Split(rawQuery, "&") {
		key, _, hasValue := strings.Cut(part, "=")
		if !hasValue || key == "" {
			// A bare token or an empty parameter is not reported by name.
			return "(bare token)"
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
