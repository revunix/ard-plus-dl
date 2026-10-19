package dash

import (
	"strings"
	"testing"
)

func TestSelectTracksMultiAudio(t *testing.T) {
	tracks := loadManifest(t, "multi-audio.mpd", manifestBase)
	selection, err := SelectTracks(tracks)
	if err != nil {
		t.Fatalf("SelectTracks: %v", err)
	}
	if selection.Video == nil || selection.Video.ID != "1080" {
		t.Fatalf("video = %+v, want the largest representation", selection.Video)
	}
	if len(selection.Audio) != 2 {
		t.Fatalf("audio = %d tracks, want 2", len(selection.Audio))
	}
	if selection.Audio[0].ID != "de-128" || selection.Audio[1].ID != "en-64" {
		t.Fatalf("audio order = %s, %s", selection.Audio[0].ID, selection.Audio[1].ID)
	}
	if len(selection.Subtitles) != 0 {
		t.Fatalf("subtitles = %+v, want none", selection.Subtitles)
	}
}

func TestSelectTracksSubtitleLanguageFilter(t *testing.T) {
	tracks := loadManifest(t, "subs-vtt.mpd", manifestBase)
	selection, err := SelectTracks(tracks)
	if err != nil {
		t.Fatalf("SelectTracks: %v", err)
	}
	if len(selection.Subtitles) != 2 {
		t.Fatalf("subtitles = %+v, want the German and English tracks only", selection.Subtitles)
	}
	if selection.Subtitles[0].ID != "de-vtt" || selection.Subtitles[1].ID != "en-vtt" {
		t.Fatalf("subtitles = %s, %s", selection.Subtitles[0].ID, selection.Subtitles[1].ID)
	}
	for _, subtitle := range selection.Subtitles {
		if subtitle.ID == "fr-vtt" {
			t.Fatal("the French subtitle track must be filtered out")
		}
		if subtitle.ID == "de-hard-vtt" {
			t.Fatal("a second German track for the same language must be dropped")
		}
	}
}

func TestSelectTracksVideoTieBreak(t *testing.T) {
	document := `<MPD mediaPresentationDuration="PT4S"><Period>
	  <AdaptationSet contentType="video" mimeType="video/mp4">
	    <SegmentTemplate duration="4" timescale="1" initialization="i.mp4" media="$Number$.m4s"/>
	    <Representation id="small" bandwidth="1000" width="640" height="360"/>
	    <Representation id="low-bandwidth" bandwidth="1000" width="1920" height="1080"/>
	    <Representation id="high-bandwidth" bandwidth="9000" width="1920" height="1080"/>
	  </AdaptationSet></Period></MPD>`
	tracks, err := parseManifestString(t, document, manifestBase)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	selection, err := SelectTracks(tracks)
	if err != nil {
		t.Fatalf("SelectTracks: %v", err)
	}
	if selection.Video == nil || selection.Video.ID != "high-bandwidth" {
		t.Fatalf("video = %+v, want the higher bandwidth on an area tie", selection.Video)
	}
}

func TestSelectTracksAudioDeduplication(t *testing.T) {
	document := `<MPD mediaPresentationDuration="PT4S"><Period>
	  <AdaptationSet contentType="audio" mimeType="audio/mp4" lang="de">
	    <SegmentTemplate duration="4" timescale="1" initialization="i.mp4" media="$Number$.m4s"/>
	    <Representation id="de-128" bandwidth="128000"/>
	  </AdaptationSet>
	  <AdaptationSet contentType="audio" mimeType="audio/mp4" lang="de">
	    <SegmentTemplate duration="4" timescale="1" initialization="i2.mp4" media="$Number$.m4s"/>
	    <Representation id="de-96" bandwidth="96000"/>
	  </AdaptationSet>
	  <AdaptationSet contentType="audio" mimeType="audio/mp4" lang="de">
	    <SegmentTemplate duration="4" timescale="1" initialization="i3.mp4" media="$Number$.m4s"/>
	    <Representation id="de-stereo" bandwidth="64000">
	      <AudioChannelConfiguration value="2"/>
	    </Representation>
	  </AdaptationSet>
	</Period></MPD>`
	tracks, err := parseManifestString(t, document, manifestBase)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	selection, err := SelectTracks(tracks)
	if err != nil {
		t.Fatalf("SelectTracks: %v", err)
	}
	if len(selection.Audio) != 2 {
		t.Fatalf("audio = %d tracks, want 2 (one per distinct channel layout)", len(selection.Audio))
	}
	if selection.Audio[0].ID != "de-128" || selection.Audio[1].ID != "de-stereo" {
		t.Fatalf("audio = %s, %s", selection.Audio[0].ID, selection.Audio[1].ID)
	}
}

func TestSelectTracksNoPlayableRepresentation(t *testing.T) {
	if _, err := SelectTracks(nil); err == nil {
		t.Fatal("an empty manifest must fail")
	} else if err.Error() != "no playable representation in DASH manifest" {
		t.Fatalf("err = %q", err.Error())
	}

	subtitlesOnly := []Track{{ID: "de", Kind: KindSubtitle, Lang: "de"}}
	if _, err := SelectTracks(subtitlesOnly); err == nil {
		t.Fatal("a subtitle-only manifest must fail")
	}
}

func TestSelectTracksVideoOnly(t *testing.T) {
	selection, err := SelectTracks([]Track{{ID: "v", Kind: KindVideo, Width: 640, Height: 360}})
	if err != nil {
		t.Fatalf("SelectTracks: %v", err)
	}
	if selection.Video == nil || selection.Video.ID != "v" {
		t.Fatalf("video = %+v", selection.Video)
	}
	if len(selection.Audio) != 0 {
		t.Fatalf("audio = %+v", selection.Audio)
	}
}

func TestSubtitleLanguagePattern(t *testing.T) {
	tests := []struct {
		lang string
		want bool
	}{
		{"de", true},
		{"de-DE", true},
		{"de_DE", true},
		{"de.sub", true},
		{"en", true},
		{"EN-us", true},
		{"fr", false},
		{"", false},
		{"deu", false},
	}
	for _, tc := range tests {
		if got := subLangPattern.MatchString(strings.ToLower(tc.lang)); got != tc.want {
			t.Fatalf("subLangPattern(%q) = %v, want %v", tc.lang, got, tc.want)
		}
	}
}
