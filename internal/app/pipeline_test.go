package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/dash"
)

// TestPipelineDebugDump checks that the diagnostic dump contains the manifest,
// the track summary and the media requests, and that media requests carry the
// user agent only.
func TestPipelineDebugDump(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "dash", "testdata", "segmenttemplate-number.mpd"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	seenHeaders := make(chan http.Header, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seenHeaders <- r.Header.Clone():
		default:
		}
		if r.URL.Path == "/dash/manifest.mpd" {
			w.Header().Set("Content-Type", "application/dash+xml")
			_, _ = w.Write(manifest)
			return
		}
		_, _ = w.Write([]byte("segment"))
	}))
	defer srv.Close()

	debugDir := t.TempDir()
	workDir := t.TempDir()
	pipeline := &Pipeline{
		Downloader: &dash.Downloader{HTTP: srv.Client(), Workers: 1},
		Muxer:      &dash.Muxer{FFmpeg: "false"},
		DebugDir:   debugDir,
	}

	tracks, err := pipeline.Fetch(context.Background(), srv.URL+"/dash/manifest.mpd?token=abc&exp=1", workDir, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(tracks) == 0 {
		t.Fatal("no tracks downloaded")
	}

	for _, name := range []string{"manifest.mpd", "tracks.txt", "requests.log"} {
		if _, err := os.Stat(filepath.Join(debugDir, name)); err != nil {
			t.Fatalf("debug file %s missing: %v", name, err)
		}
	}
	dumped, err := os.ReadFile(filepath.Join(debugDir, "manifest.mpd"))
	if err != nil {
		t.Fatalf("read manifest.mpd: %v", err)
	}
	if string(dumped) != string(manifest) {
		t.Fatal("manifest.mpd differs from the served manifest")
	}
	summary, err := os.ReadFile(filepath.Join(debugDir, "tracks.txt"))
	if err != nil {
		t.Fatalf("read tracks.txt: %v", err)
	}
	text := string(summary)
	for _, want := range []string{"manifest offers", "selected:", "video v1080"} {
		if !strings.Contains(text, want) {
			t.Fatalf("tracks.txt misses %q:\n%s", want, text)
		}
	}
	log, err := os.ReadFile(filepath.Join(debugDir, "requests.log"))
	if err != nil {
		t.Fatalf("read requests.log: %v", err)
	}
	if !strings.Contains(string(log), "-> 200") {
		t.Fatalf("requests.log has no successful request:\n%s", log)
	}
	if strings.Contains(string(log), "token=abc") {
		t.Fatalf("requests.log leaks the token:\n%s", log)
	}

	header := <-seenHeaders
	if got := header.Get("Origin"); got != "" {
		t.Fatalf("media requests carry Origin = %q", got)
	}
	if got := header.Get("Referer"); got != "" {
		t.Fatalf("media requests carry Referer = %q", got)
	}
	if got := header.Get("User-Agent"); !strings.Contains(got, "Chrome/") {
		t.Fatalf("User-Agent = %q", got)
	}
}
