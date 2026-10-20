package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/revunix/ard-plus-dl/internal/auth"
	"github.com/revunix/ard-plus-dl/internal/dash"
)

// Pipeline fetches a DASH manifest, selects its tracks and downloads them.
type Pipeline struct {
	HTTP       *http.Client
	Downloader *dash.Downloader
	Muxer      *dash.Muxer
	UA         string
	// DebugDir, when set, receives the fetched manifest and the track summary.
	DebugDir string
	Logf     func(format string, args ...any)
}

func (p *Pipeline) logf(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

func (p *Pipeline) httpClient() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return http.DefaultClient
}

func (p *Pipeline) ua() string {
	if p.UA != "" {
		return p.UA
	}
	return auth.DefaultUA
}

// Fetch downloads every selected track of the manifest into workDir.
func (p *Pipeline) Fetch(ctx context.Context, manifestURL, workDir string, progress func(done, total int)) ([]dash.DownloadedTrack, error) {
	if p.Downloader == nil {
		return nil, errors.New("no downloader configured")
	}
	// The manifest URL carries the playback token, so the error messages must
	// not repeat it.
	parsed, err := url.Parse(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("invalid manifest URL: %s", dash.RedactURL(manifestURL))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid manifest request: %s", dash.RedactURL(manifestURL))
	}
	// Only the user agent is sent: that is what the previously working yt-dlp
	// pipeline did, while the CDN reacts to unexpected headers with 403.
	req.Header.Set("User-Agent", p.ua())

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("manifest request failed: HTTP %d", resp.StatusCode)
	}
	manifest, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if p.DebugDir != "" {
		_ = os.WriteFile(filepath.Join(p.DebugDir, "manifest.mpd"), manifest, 0o644)
		appendDebugLine(p.DebugDir, fmt.Sprintf("manifest: GET %s -> %d (query keys: %s)",
			dash.RedactURL(manifestURL), resp.StatusCode, dash.QueryKeys(parsed.RawQuery)))
		if p.Downloader != nil && p.Downloader.DebugDir == "" {
			p.Downloader.DebugDir = p.DebugDir
		}
	}
	tracks, err := dash.ParseManifest(bytes.NewReader(manifest), parsed)
	if err != nil {
		return nil, err
	}
	selection, err := dash.SelectTracks(tracks)
	if err != nil {
		return nil, err
	}
	if p.DebugDir != "" {
		dumpTracks(filepath.Join(p.DebugDir, "tracks.txt"), tracks, selection)
	}

	ordered := make([]dash.Track, 0, 1+len(selection.Audio)+len(selection.Subtitles))
	if selection.Video != nil {
		ordered = append(ordered, *selection.Video)
	}
	ordered = append(ordered, selection.Audio...)
	ordered = append(ordered, selection.Subtitles...)

	downloaded := make([]dash.DownloadedTrack, 0, len(ordered))
	completed := 0
	for i := range ordered {
		track := ordered[i]
		dir := filepath.Join(workDir, fmt.Sprintf("%02d-%s", i, safeName(track.ID)))
		base := completed
		files, err := p.Downloader.Download(ctx, &track, dir, func(done, total int) {
			if progress != nil {
				progress(base+done, base+total)
			}
		})
		if err != nil {
			return nil, err
		}
		completed = base + len(files)
		downloaded = append(downloaded, dash.DownloadedTrack{Track: &track, Files: files})
	}
	if len(downloaded) == 0 {
		return nil, errors.New("no tracks downloaded")
	}
	return downloaded, nil
}

// Mux concatenates and muxes the downloaded tracks into out+".mp4".
func (p *Pipeline) Mux(ctx context.Context, workDir string, tracks []dash.DownloadedTrack, out string) error {
	if p.Muxer == nil {
		return errors.New("no muxer configured")
	}
	return p.Muxer.Mux(ctx, workDir, tracks, out)
}

func safeName(value string) string {
	cleaned := make([]rune, 0, len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			cleaned = append(cleaned, r)
		default:
			cleaned = append(cleaned, '_')
		}
	}
	if len(cleaned) == 0 {
		return "track"
	}
	return string(cleaned)
}

// appendDebugLine adds one line to the shared diagnostic log.
func appendDebugLine(dir, line string) {
	file, err := os.OpenFile(filepath.Join(dir, "requests.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintln(file, line)
}

// dumpTracks writes what the manifest offered and what was selected, so a CDN
// rejection can be diagnosed without the token.
func dumpTracks(path string, tracks []dash.Track, selection dash.Selection) {
	var b strings.Builder
	fmt.Fprintf(&b, "manifest offers %d representations\n", len(tracks))
	for _, track := range tracks {
		fmt.Fprintf(&b, "  %-24s %-8s lang=%-6s mime=%-28s codecs=%-16s %dx%d bw=%d segments=%d init=%v\n",
			track.ID, track.Kind, track.Lang, track.MimeType, track.Codecs,
			track.Width, track.Height, track.Bandwidth, len(track.Segments), track.Init != nil)
	}
	b.WriteString("selected:\n")
	if selection.Video != nil {
		fmt.Fprintf(&b, "  video %s (%dx%d)\n", selection.Video.ID, selection.Video.Width, selection.Video.Height)
	}
	for _, audio := range selection.Audio {
		fmt.Fprintf(&b, "  audio %s lang=%s channels=%d\n", audio.ID, audio.Lang, audio.Channels)
	}
	for _, subtitle := range selection.Subtitles {
		fmt.Fprintf(&b, "  subtitle %s lang=%s mime=%s codecs=%s\n", subtitle.ID, subtitle.Lang, subtitle.MimeType, subtitle.Codecs)
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}
