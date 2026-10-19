package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/dash"
	"github.com/revunix/ard-plus-dl/internal/download"
	"github.com/revunix/ard-plus-dl/internal/media"
)

// TestEndToEndNativeDash runs the full headless pipeline against a real DASH
// stream: ffmpeg generates the manifest and segments, httptest serves them, the
// native downloader fetches them, ffmpeg muxes them and the ffprobe-backed
// media probe produces the final file name.
func TestEndToEndNativeDash(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed; skipping the native DASH end-to-end test")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed; skipping the native DASH end-to-end test")
	}

	mediaDir := t.TempDir()
	manifest := filepath.Join(mediaDir, "manifest.mpd")
	// -g 25 forces one keyframe per second. Without it x264 emits a single
	// video segment while the manifest still advertises two, so the parser's
	// second segment would answer 404.
	generate := exec.Command(ffmpegPath, "-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "2",
		"-c:v", "libx264", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0",
		"-c:a", "aac",
		"-f", "dash", "-seg_duration", "1", "-use_template", "1", "-use_timeline", "0",
		"-adaptation_sets", "id=0,streams=v id=1,streams=a",
		manifest)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot produce a DASH stream (%v): %s", err, out)
	}

	mediaSrv := httptest.NewServer(http.FileServer(http.Dir(mediaDir)))
	defer mediaSrv.Close()

	backend := newBackend(t)
	manifestURL := mediaSrv.URL + "/manifest.mpd"
	backend.setGraphQL(func(operation string, _ map[string]any) (string, int) {
		if operation != "MovieDetails" {
			return graphQLErrorResponse("unexpected operation " + operation)
		}
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", manifestURL),
			"series": nil,
		})
	})

	downloads := filepath.Join(t.TempDir(), "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	api := backend.client()
	client := mediaSrv.Client()
	application := &App{
		Config:  &config.Options{URL: "https://www.ardplus.de/details/m1", Automatic: true, Skip: 1, FileTag: "example"},
		Session: backend.session(t, api),
		API:     api,
		Fetcher: &Pipeline{
			HTTP:       client,
			Downloader: &dash.Downloader{HTTP: client},
			Muxer:      &dash.Muxer{FFmpeg: ffmpegPath},
		},
		Manager: &download.Manager{
			DownloadsDir: downloads,
			FileTag:      "example",
			Probe:        func(path string) (string, error) { return media.ProbeMediaTag(ffprobePath, path) },
		},
	}
	if err := application.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	files := mp4Files(t, downloads)
	if len(files) != 1 {
		t.Fatalf("mp4 files = %v, want exactly the muxed movie", files)
	}
	final := files[0]

	mediaTag, err := media.ProbeMediaTag(ffprobePath, final)
	if err != nil {
		t.Fatalf("ProbeMediaTag(%s): %v", final, err)
	}
	stem := media.MovieNameStem("Test Film", "2020")
	wantName := stem + "." + media.ApplyFileTag(mediaTag, "example") + ".mp4"
	if got := filepath.Base(final); got != wantName {
		t.Errorf("final name = %q, want %q", got, wantName)
	}
	if got := filepath.Base(filepath.Dir(final)); got != stem {
		t.Errorf("final directory = %q, want %q", got, stem)
	}

	out, err := exec.Command(ffprobePath, "-v", "error",
		"-show_entries", "stream=codec_type", "-of", "json", final).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", final, err)
	}
	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("ffprobe output: %v", err)
	}
	var video, audio int
	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			video++
		case "audio":
			audio++
		}
	}
	if video != 1 || audio != 1 {
		t.Errorf("streams = %d video, %d audio, want exactly one each", video, audio)
	}
}
