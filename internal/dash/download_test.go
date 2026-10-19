package dash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// requestRecord is one observed download request.
type requestRecord struct {
	path  string
	query string
	rng   string
}

// recorder collects the requests of a test server.
type recorder struct {
	mu             sync.Mutex
	seen           []requestRecord
	requiredQuery  *string
	requiredCookie *string
}

// requireCookie makes the test server reject media requests without exactly
// this cookie header, which is how ARD's media cache behaviour behaves.
func (rec *recorder) requireCookie(cookie string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.requiredCookie = &cookie
}

func (rec *recorder) cookieOK(cookie string) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.requiredCookie == nil || cookie == *rec.requiredCookie
}

// requireQuery makes the test server reject media requests that do not carry
// exactly this query, which is what ARD's CDN does with its signed URLs.
func (rec *recorder) requireQuery(query string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.requiredQuery = &query
}

// requireNoQuery is the opposite: the server answers only unsigned requests.
func (rec *recorder) requireNoQuery() {
	empty := ""
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.requiredQuery = &empty
}

func (rec *recorder) queryOK(rawQuery string) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.requiredQuery == nil || rawQuery == *rec.requiredQuery
}

func (rec *recorder) record(r *http.Request) requestRecord {
	entry := requestRecord{path: r.URL.Path, query: r.URL.RawQuery, rng: r.Header.Get("Range")}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seen = append(rec.seen, entry)
	return entry
}

func (rec *recorder) all() []requestRecord {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]requestRecord(nil), rec.seen...)
}

func (rec *recorder) countPath(path string) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	count := 0
	for _, entry := range rec.seen {
		if entry.path == path {
			count++
		}
	}
	return count
}

// parseRangeHeader parses "bytes=start-end" for the test server.
func parseRangeHeader(value string, size int64) (int64, int64, bool) {
	spec, ok := strings.CutPrefix(value, "bytes=")
	if !ok {
		return 0, 0, false
	}
	startText, endText, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if endText != "" {
		end, err = strconv.ParseInt(endText, 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}
	}
	if end >= size {
		end = size - 1
	}
	return start, end, true
}

// serveBytes answers a request, honouring the Range header.
func serveBytes(w http.ResponseWriter, r *http.Request, data []byte) {
	if rng := r.Header.Get("Range"); rng != "" {
		start, end, ok := parseRangeHeader(rng, int64(len(data)))
		if !ok {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// fileServer serves a fixed file map and records every request.
func fileServer(t *testing.T, files map[string][]byte) (*recorder, *httptest.Server) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if !rec.queryOK(r.URL.RawQuery) || !rec.cookieOK(r.Header.Get("Cookie")) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		data, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		serveBytes(w, r, data)
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestDownloaderDownloadsSegments(t *testing.T) {
	rec, srv := fileServer(t, map[string][]byte{
		"/init.mp4":  []byte("init"),
		"/00001.m4s": []byte("one"),
		"/00002.m4s": []byte("two"),
	})
	track := &Track{
		ID:   "v",
		Kind: KindVideo,
		Init: &Segment{URL: srv.URL + "/init.mp4"},
		Segments: []Segment{
			{URL: srv.URL + "/00001.m4s"},
			{URL: srv.URL + "/00002.m4s"},
		},
	}
	dir := t.TempDir()
	var lastDone, lastTotal int
	progress := make(chan [2]int, 16)

	downloader := &Downloader{HTTP: srv.Client(), Workers: 2}
	files, err := downloader.Download(context.Background(), track, dir, func(done, total int) {
		lastDone, lastTotal = done, total
		select {
		case progress <- [2]int{done, total}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := []string{
		filepath.Join(dir, "init.mp4"),
		filepath.Join(dir, "00001.m4s"),
		filepath.Join(dir, "00002.m4s"),
	}
	assertStrings(t, "files", files, want)
	if got := readFile(t, want[0]); got != "init" {
		t.Fatalf("init content = %q", got)
	}
	if got := readFile(t, want[1]); got != "one" {
		t.Fatalf("segment 1 content = %q", got)
	}
	if got := readFile(t, want[2]); got != "two" {
		t.Fatalf("segment 2 content = %q", got)
	}
	if lastDone != 3 || lastTotal != 3 {
		t.Fatalf("progress = %d/%d, want 3/3", lastDone, lastTotal)
	}
	if len(rec.all()) != 3 {
		t.Fatalf("requests = %v, want 3", rec.all())
	}
	// The segment files must not keep a .part sibling behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".part") {
			t.Fatalf("leftover part file %q", entry.Name())
		}
	}
	close(progress)
}

func TestDownloaderResumeSkipsExistingSegment(t *testing.T) {
	rec, srv := fileServer(t, map[string][]byte{
		"/init.mp4":  []byte("init"),
		"/00001.m4s": []byte("fresh-one"),
		"/00002.m4s": []byte("two"),
	})
	track := &Track{
		ID:   "v",
		Kind: KindVideo,
		Init: &Segment{URL: srv.URL + "/init.mp4"},
		Segments: []Segment{
			{URL: srv.URL + "/00001.m4s"},
			{URL: srv.URL + "/00002.m4s"},
		},
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "00001.m4s"), []byte("already-there"), 0o644); err != nil {
		t.Fatalf("seed segment: %v", err)
	}

	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	if _, err := downloader.Download(context.Background(), track, dir, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := rec.countPath("/00001.m4s"); got != 0 {
		t.Fatalf("the completed segment was fetched %d time(s)", got)
	}
	if got := rec.countPath("/00002.m4s"); got != 1 {
		t.Fatalf("segment 2 fetches = %d, want 1", got)
	}
	if got := readFile(t, filepath.Join(dir, "00001.m4s")); got != "already-there" {
		t.Fatalf("completed segment was overwritten: %q", got)
	}
}

func TestDownloaderRetriesOnServerError(t *testing.T) {
	rec := &recorder{}
	var mu sync.Mutex
	failures := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		mu.Lock()
		first := failures == 0
		failures++
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serveBytes(w, r, []byte("ok"))
	}))
	defer srv.Close()

	track := &Track{ID: "v", Kind: KindVideo, Segments: []Segment{{URL: srv.URL + "/seg.m4s"}}}
	dir := t.TempDir()
	downloader := &Downloader{HTTP: srv.Client(), Workers: 1, Retries: 3}
	if _, err := downloader.Download(context.Background(), track, dir, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := rec.countPath("/seg.m4s"); got != 2 {
		t.Fatalf("requests = %d, want 2 (one retry)", got)
	}
	if got := readFile(t, filepath.Join(dir, "00001.m4s")); got != "ok" {
		t.Fatalf("content = %q", got)
	}
}

func TestDownloaderFailsImmediatelyOnClientError(t *testing.T) {
	rec, srv := fileServer(t, nil)
	track := &Track{ID: "v", Kind: KindVideo, Segments: []Segment{{URL: srv.URL + "/missing.m4s"}}}
	dir := t.TempDir()
	downloader := &Downloader{HTTP: srv.Client(), Workers: 1, Retries: 3}

	_, err := downloader.Download(context.Background(), track, dir, nil)
	if err == nil {
		t.Fatal("a 404 must fail the download")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %q, want the HTTP status", err.Error())
	}
	if got := rec.countPath("/missing.m4s"); got != 1 {
		t.Fatalf("a 404 cost %d requests, want 1", got)
	}
}

func TestHTTPStatusErrorRetryable(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{500, true},
		{503, true},
		{429, true},
		{404, false},
		{403, false},
		{400, false},
	}
	for _, tc := range tests {
		err := &httpStatusError{URL: "https://example.test/x", Status: tc.status}
		if got := err.retryable(); got != tc.want {
			t.Fatalf("retryable(%d) = %v, want %v", tc.status, got, tc.want)
		}
		if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
			t.Fatalf("error text omits the status: %q", err.Error())
		}
	}
}

func TestDownloaderByteRangeTrack(t *testing.T) {
	initData := bytes.Repeat([]byte{0xA1}, 100)
	sidxBox := padSidxBox(100, 0, 1000, 0, 0, []sidxRef{{size: 10, duration: 1000}, {size: 20, duration: 1000}})
	seg1 := bytes.Repeat([]byte{0xB1}, 10)
	seg2 := bytes.Repeat([]byte{0xC2}, 20)
	file := append(append(append([]byte{}, initData...), sidxBox...), append(seg1, seg2...)...)

	rec, srv := fileServer(t, map[string][]byte{"/dash/video.mp4": file})
	tracks := loadManifest(t, "segmentbase-sidx.mpd", srv.URL+"/dash/manifest.mpd")
	track := findTrack(t, tracks, "vsidx")
	dir := t.TempDir()

	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	files, err := downloader.Download(context.Background(), &track, dir, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v, want init plus two segments", files)
	}
	if got := readFile(t, filepath.Join(dir, "init.mp4")); got != string(initData) {
		t.Fatalf("init bytes = %d, want %d", len(got), len(initData))
	}
	if got := readFile(t, filepath.Join(dir, "00001.m4s")); got != string(seg1) {
		t.Fatalf("segment 1 bytes = %d, want %d", len(got), len(seg1))
	}
	if got := readFile(t, filepath.Join(dir, "00002.m4s")); got != string(seg2) {
		t.Fatalf("segment 2 bytes = %d, want %d", len(got), len(seg2))
	}

	wantRanges := map[string]bool{
		"bytes=100-199": true, // sidx index range
		"bytes=0-99":    true, // initialization range
		"bytes=200-209": true, // first subsegment
		"bytes=210-229": true, // second subsegment
	}
	seen := rec.all()
	if len(seen) != len(wantRanges) {
		t.Fatalf("requests = %v, want %d ranged requests", seen, len(wantRanges))
	}
	for _, entry := range seen {
		if entry.path != "/dash/video.mp4" {
			t.Fatalf("path = %q", entry.path)
		}
		if !wantRanges[entry.rng] {
			t.Fatalf("unexpected range %q", entry.rng)
		}
	}
}

// A distribution that serves unsigned segments: the appended variant is
// rejected first, the probe falls back to the bare URL and keeps it.
func TestDownloaderFallsBackToUnsignedRequests(t *testing.T) {
	rec, srv := fileServer(t, map[string][]byte{
		"/dash/video/v1080/init.mp4":      []byte("i"),
		"/dash/video/v1080/seg-00001.m4s": []byte("1"),
		"/dash/video/v1080/seg-00002.m4s": []byte("2"),
		"/dash/video/v1080/seg-00003.m4s": []byte("3"),
	})
	tracks := loadManifest(t, "segmenttemplate-number.mpd", srv.URL+"/dash/manifest.mpd?token=abc&exp=1")
	track := findTrack(t, tracks, "v1080")
	rec.requireNoQuery()
	dir := t.TempDir()

	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	files, err := downloader.Download(context.Background(), &track, dir, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %v", files)
	}
	seen := rec.all()
	if len(seen) != 5 {
		t.Fatalf("requests = %v, want 5 (one rejected probe plus the four files)", seen)
	}
	signed := 0
	for _, entry := range seen {
		if entry.query != "" {
			signed++
		}
	}
	if signed != 1 {
		t.Fatalf("requests with the manifest query = %d, want exactly 1 rejected probe", signed)
	}
	if mode := mediaQueryMode(downloader.queryMode.Load()); !downloader.queryProbed.Load() || mode != queryModeAsIs {
		t.Fatalf("query mode = %v (probed %v), want as-is", mode, downloader.queryProbed.Load())
	}
}

// ARD's media cache behaviour reads the CloudFront parameters from cookies and
// ignores the query string, so the downloader probes the cookie variant.
func TestDownloaderProbesCloudFrontCookies(t *testing.T) {
	rec, srv := fileServer(t, map[string][]byte{
		"/dash/video/v1080/init.mp4":      []byte("i"),
		"/dash/video/v1080/seg-00001.m4s": []byte("1"),
		"/dash/video/v1080/seg-00002.m4s": []byte("2"),
		"/dash/video/v1080/seg-00003.m4s": []byte("3"),
	})
	query := testCloudFrontQuery()
	tracks := loadManifest(t, "segmenttemplate-number.mpd", srv.URL+"/dash/manifest.mpd?"+query)
	track := findTrack(t, tracks, "v1080")
	rec.requireCookie(CloudFrontCookieHeader(query))

	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	files, err := downloader.Download(context.Background(), &track, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %v", files)
	}
	if mode := mediaQueryMode(downloader.queryMode.Load()); !downloader.queryProbed.Load() || mode != queryModeCookies {
		t.Fatalf("query mode = %v (probed %v), want cookies", mode, downloader.queryProbed.Load())
	}
	// The query variant is probed first and rejected once, then every request
	// carries the cookies.
	seen := rec.all()
	if len(seen) != 5 {
		t.Fatalf("requests = %d, want 5 (one rejected probe plus four files)", len(seen))
	}
	if seen[0].query == "" {
		t.Fatalf("first request = %+v, want the query variant", seen[0])
	}
}

// The signed distribution: the manifest query goes to every media request that
// has none of its own, without a wasted probe.
func TestDownloaderUsesManifestQueryOnMediaRequests(t *testing.T) {
	rec, srv := fileServer(t, map[string][]byte{
		"/dash/video/v1080/init.mp4":      []byte("i"),
		"/dash/video/v1080/seg-00001.m4s": []byte("1"),
		"/dash/video/v1080/seg-00002.m4s": []byte("2"),
		"/dash/video/v1080/seg-00003.m4s": []byte("3"),
	})
	tracks := loadManifest(t, "segmenttemplate-number.mpd", srv.URL+"/dash/manifest.mpd?token=abc")
	track := findTrack(t, tracks, "v1080")
	rec.requireQuery("token=abc")

	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	files, err := downloader.Download(context.Background(), &track, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %v", files)
	}
	seen := rec.all()
	if len(seen) != 4 {
		t.Fatalf("requests = %v, want exactly the four media files", seen)
	}
	for _, entry := range seen {
		if entry.query != "token=abc" {
			t.Fatalf("request %s carries query %q, want the manifest query", entry.path, entry.query)
		}
	}
	if mode := mediaQueryMode(downloader.queryMode.Load()); !downloader.queryProbed.Load() || mode != queryModeAppended {
		t.Fatalf("query mode = %v, want appended", mode)
	}
}

func TestDownloaderContextCancellation(t *testing.T) {
	started := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	track := &Track{
		ID:       "v",
		Kind:     KindVideo,
		Segments: []Segment{{URL: srv.URL + "/a.m4s"}, {URL: srv.URL + "/b.m4s"}, {URL: srv.URL + "/c.m4s"}},
	}
	dir := t.TempDir()
	downloader := &Downloader{HTTP: srv.Client(), Workers: 2}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := downloader.Download(ctx, track, dir, nil)
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the downloader never issued a request")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Download did not return after cancellation")
	}
}

func TestDownloaderEmptyTrack(t *testing.T) {
	downloader := &Downloader{}
	_, err := downloader.Download(context.Background(), &Track{ID: "v"}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("a track without segments must fail")
	}
	if err.Error() != "representation v has no segments" {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestAppendQuery(t *testing.T) {
	tests := []struct {
		name      string
		segURL    string
		baseQuery string
		want      string
	}{
		{"no base query", "https://cdn.test/a.m4s", "", "https://cdn.test/a.m4s"},
		{"appends", "https://cdn.test/a.m4s", "token=abc", "https://cdn.test/a.m4s?token=abc"},
		{"keeps own query", "https://cdn.test/a.m4s?x=1", "token=abc", "https://cdn.test/a.m4s?x=1"},
		{"unparsable URL", "://bad url", "token=abc", "://bad url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AppendQuery(tc.segURL, tc.baseQuery); got != tc.want {
				t.Fatalf("AppendQuery(%q, %q) = %q, want %q", tc.segURL, tc.baseQuery, got, tc.want)
			}
		})
	}
}

func TestDownloaderRequestHeaders(t *testing.T) {
	headers := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case headers <- r.Header.Clone():
		default:
		}
		serveBytes(w, r, []byte("x"))
	}))
	defer srv.Close()

	track := &Track{ID: "v", Kind: KindVideo, Segments: []Segment{{URL: srv.URL + "/a.m4s"}}}
	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	if _, err := downloader.Download(context.Background(), track, t.TempDir(), nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got := <-headers
	if ua := got.Get("User-Agent"); !strings.Contains(ua, "Chrome/") {
		t.Fatalf("User-Agent = %q", ua)
	}
	// The CDN rejects requests with an Origin/Referer it does not expect; the
	// previously working yt-dlp pipeline sent the user agent only.
	if origin := got.Get("Origin"); origin != "" {
		t.Fatalf("Origin = %q, want none", origin)
	}
	if referer := got.Get("Referer"); referer != "" {
		t.Fatalf("Referer = %q, want none", referer)
	}
}

func TestDownloaderResolvesPendingTemplateThroughSidx(t *testing.T) {
	// A SegmentTemplate without a duration is resolved from the sidx of the
	// initialization segment.
	sidx := buildSidxBox(0, 1000, 0, 0, []sidxRef{{size: 0, duration: 2000}, {size: 0, duration: 2000}})
	initBox := append(buildBox("ftyp", []byte("isom")), sidx...)
	rec, srv := fileServer(t, map[string][]byte{
		"/dash/init.mp4":  initBox,
		"/dash/seg-1.m4s": []byte("one"),
		"/dash/seg-2.m4s": []byte("two"),
	})
	document := `<MPD><Period><AdaptationSet contentType="video" mimeType="video/mp4">
	  <SegmentTemplate timescale="1" startNumber="1" initialization="init.mp4" media="seg-$Number$.m4s"/>
	  <Representation id="v" bandwidth="1000" width="640" height="360"/>
	</AdaptationSet></Period></MPD>`
	tracks, err := parseManifestString(t, document, srv.URL+"/dash/manifest.mpd")
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	dir := t.TempDir()
	downloader := &Downloader{HTTP: srv.Client(), Workers: 1}
	files, err := downloader.Download(context.Background(), &tracks[0], dir, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v", files)
	}
	if got := readFile(t, filepath.Join(dir, "00002.m4s")); got != "two" {
		t.Fatalf("segment 2 content = %q", got)
	}
	// The sidx probe reads the initialization segment once and the download
	// then fetches it as a normal init job, so the path is requested twice.
	if rec.countPath("/dash/init.mp4") < 1 {
		t.Fatalf("the initialization segment was never requested")
	}
	if rec.countPath("/dash/seg-1.m4s") != 1 || rec.countPath("/dash/seg-2.m4s") != 1 {
		t.Fatalf("segment requests = %v", rec.all())
	}
}

func TestMediaURLQueryModes(t *testing.T) {
	track := &Track{baseQuery: "token=abc"}
	const signedSegments = "https://cdn.test/a.m4s?existing=1"

	if got := (&Downloader{}).mediaURL(signedSegments, track, queryModeAsIs); got != signedSegments {
		t.Fatalf("as-is = %q, want the unchanged segment URL", got)
	}
	// A segment URL that already carries parameters keeps them untouched.
	if got := (&Downloader{}).mediaURL(signedSegments, track, queryModeAppended); got != signedSegments {
		t.Fatalf("appended = %q, want the segment URL unchanged", got)
	}
	if got := (&Downloader{}).mediaURL("https://cdn.test/a.m4s", track, queryModeAppended); got != "https://cdn.test/a.m4s?token=abc" {
		t.Fatalf("appended without an existing query = %q", got)
	}
	if got := (&Downloader{}).mediaURL("https://cdn.test/a.m4s", track, queryModeAsIs); got != "https://cdn.test/a.m4s" {
		t.Fatalf("as-is without query = %q", got)
	}
}
