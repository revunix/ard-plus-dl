package dash

import (
	"os"
	"path/filepath"
	"testing"
)

// A real ARD Plus manifest splits the SegmentTemplate across two levels: the
// AdaptationSet carries timescale, the Representation carries media and
// initialization. Ignoring the inherited timescale collapsed the video track to
// a single segment, which silently truncated the download.
func TestSplitSegmentTemplateInheritsTimescale(t *testing.T) {
	tracks := loadManifest(t, "split-segmenttemplate.mpd",
		"https://cdn.ardplus.de/output/ardplus/Bavar_1/full_movie/dash/manifest.mpd?CloudFront-Signature=x")
	base := "https://cdn.ardplus.de/output/ardplus/Bavar_1/full_movie/dash/"

	video := findTrack(t, tracks, "1")
	if want := 404; len(video.Segments) != want {
		t.Fatalf("video segments = %d, want %d", len(video.Segments), want)
	}
	if video.Init == nil || video.Init.URL != base+"dashvideo_576init.mp4" {
		t.Fatalf("video init = %+v", video.Init)
	}
	if got, want := video.Segments[0].URL, base+"dashvideo_576_000000001.mp4"; got != want {
		t.Fatalf("first video segment = %q, want %q", got, want)
	}
	if got, want := video.Segments[len(video.Segments)-1].URL, base+"dashvideo_576_000000404.mp4"; got != want {
		t.Fatalf("last video segment = %q, want %q", got, want)
	}
	if video.Height != 576 || video.Width != 1024 {
		t.Fatalf("video dimensions = %dx%d", video.Width, video.Height)
	}

	audio := findTrack(t, tracks, "3")
	if want := 404; len(audio.Segments) != want {
		t.Fatalf("audio segments = %d, want %d", len(audio.Segments), want)
	}
	if got, want := audio.Segments[0].URL, base+"dashaudio_20de_000000001.mp4"; got != want {
		t.Fatalf("first audio segment = %q, want %q", got, want)
	}
	if audio.Kind != KindAudio || audio.Channels != 2 || audio.Lang != "deu" {
		t.Fatalf("audio track = %+v", audio)
	}

	selection, err := SelectTracks(tracks)
	if err != nil {
		t.Fatalf("SelectTracks: %v", err)
	}
	if selection.Video == nil || selection.Video.ID != "1" {
		t.Fatalf("selected video = %+v, want the 576p representation", selection.Video)
	}
	if len(selection.Audio) != 1 || selection.Audio[0].ID != "3" {
		t.Fatalf("selected audio = %+v", selection.Audio)
	}
	if len(selection.Subtitles) != 0 {
		t.Fatalf("selected subtitles = %+v", selection.Subtitles)
	}
}

// The fixture must not be picked up from the working directory.
func TestSplitSegmentTemplateFixturePath(t *testing.T) {
	if _, err := os.Stat(filepath.Join("testdata", "split-segmenttemplate.mpd")); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
}
