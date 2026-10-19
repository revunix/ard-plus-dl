package dash

import (
	"encoding/binary"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadManifest(t *testing.T, file, manifestURL string) []Track {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("open fixture %s: %v", file, err)
	}
	defer f.Close()
	parsed, err := url.Parse(manifestURL)
	if err != nil {
		t.Fatalf("parse manifest URL: %v", err)
	}
	tracks, err := ParseManifest(f, parsed)
	if err != nil {
		t.Fatalf("ParseManifest(%s): %v", file, err)
	}
	return tracks
}

func parseManifestString(t *testing.T, document, manifestURL string) ([]Track, error) {
	t.Helper()
	parsed, err := url.Parse(manifestURL)
	if err != nil {
		t.Fatalf("parse manifest URL: %v", err)
	}
	return ParseManifest(strings.NewReader(document), parsed)
}

func findTrack(t *testing.T, tracks []Track, id string) Track {
	t.Helper()
	for _, track := range tracks {
		if track.ID == id {
			return track
		}
	}
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	t.Fatalf("track %q not found in %v", id, ids)
	return Track{}
}

func segmentURLs(track Track) []string {
	urls := make([]string, 0, len(track.Segments))
	for _, segment := range track.Segments {
		urls = append(urls, segment.URL)
	}
	return urls
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q, want %q", what, i, got[i], want[i])
		}
	}
}

func assertRange(t *testing.T, what string, got *ByteRange, start, end int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s range is nil, want %d-%d", what, start, end)
	}
	if got.Start != start || got.End != end {
		t.Fatalf("%s range = %d-%d, want %d-%d", what, got.Start, got.End, start, end)
	}
}

const manifestBase = "https://cdn.example.test/dash/manifest.mpd"

func TestParseManifestSegmentTemplateNumber(t *testing.T) {
	tracks := loadManifest(t, "segmenttemplate-number.mpd", manifestBase)
	if len(tracks) != 3 {
		t.Fatalf("tracks = %d, want 3", len(tracks))
	}

	video := findTrack(t, tracks, "v1080")
	if video.Kind != KindVideo {
		t.Fatalf("kind = %v, want video", video.Kind)
	}
	if video.Width != 1920 || video.Height != 1080 || video.Bandwidth != 5000000 {
		t.Fatalf("video track = %+v", video)
	}
	if video.MimeType != "video/mp4" || video.Lang != "de" {
		t.Fatalf("mimeType/lang = %q/%q", video.MimeType, video.Lang)
	}
	if video.Init == nil || video.Init.URL != "https://cdn.example.test/dash/video/v1080/init.mp4" {
		t.Fatalf("init = %+v", video.Init)
	}
	assertStrings(t, "segments", segmentURLs(video), []string{
		"https://cdn.example.test/dash/video/v1080/seg-00001.m4s",
		"https://cdn.example.test/dash/video/v1080/seg-00002.m4s",
		"https://cdn.example.test/dash/video/v1080/seg-00003.m4s",
	})

	second := findTrack(t, tracks, "v720")
	if second.Width != 1280 || second.Height != 720 {
		t.Fatalf("second video track = %+v", second)
	}

	audio := findTrack(t, tracks, "a128")
	if audio.Kind != KindAudio {
		t.Fatalf("kind = %v, want audio", audio.Kind)
	}
	if audio.Channels != 2 {
		t.Fatalf("channels = %d, want 2", audio.Channels)
	}
	if audio.Init == nil || audio.Init.URL != "https://cdn.example.test/dash/audio/a128/init.mp4" {
		t.Fatalf("audio init = %+v", audio.Init)
	}
}

func TestParseManifestSegmentTemplateTime(t *testing.T) {
	tracks := loadManifest(t, "segmenttemplate-time.mpd", manifestBase)
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(tracks))
	}
	track := tracks[0]
	assertStrings(t, "segments", segmentURLs(track), []string{
		"https://cdn.example.test/dash/video/vtime/0.m4s",
		"https://cdn.example.test/dash/video/vtime/4.m4s",
		"https://cdn.example.test/dash/video/vtime/8.m4s",
	})
}

func TestParseManifestSegmentList(t *testing.T) {
	tracks := loadManifest(t, "segmentlist.mpd", manifestBase)
	track := findTrack(t, tracks, "vlist")
	if track.Init == nil {
		t.Fatal("SegmentList initialization was not parsed")
	}
	if track.Init.URL != "https://cdn.example.test/dash/video/init.mp4" {
		t.Fatalf("init URL = %q", track.Init.URL)
	}
	assertRange(t, "init", track.Init.Range, 0, 999)
	if len(track.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(track.Segments))
	}
	assertRange(t, "segment 1", track.Segments[0].Range, 1000, 1999)
	if track.Segments[0].URL != "https://cdn.example.test/dash/video/seg-1.m4s" {
		t.Fatalf("segment 1 URL = %q", track.Segments[0].URL)
	}
	if track.Segments[1].Range != nil {
		t.Fatalf("segment 2 range = %+v, want nil", track.Segments[1].Range)
	}
}

func TestParseManifestSegmentBaseIsPending(t *testing.T) {
	tracks := loadManifest(t, "segmentbase-sidx.mpd", manifestBase)
	track := findTrack(t, tracks, "vsidx")
	if track.pending == nil {
		t.Fatal("SegmentBase addressing must stay pending until the sidx is fetched")
	}
	if !track.pending.byteRangeMode {
		t.Fatal("byte-range mode is not set")
	}
	assertRange(t, "sidx", track.pending.sidxRange, 100, 199)
	assertRange(t, "init", track.pending.initRange, 0, 99)
	if track.pending.initURL != "https://cdn.example.test/dash/video.mp4" {
		t.Fatalf("initURL = %q", track.pending.initURL)
	}
	if track.Init != nil || len(track.Segments) != 0 {
		t.Fatalf("pending track resolved eagerly: init=%+v segments=%d", track.Init, len(track.Segments))
	}
}

func TestParseManifestNestedBaseURL(t *testing.T) {
	tracks := loadManifest(t, "nested-baseurl.mpd", manifestBase)
	track := findTrack(t, tracks, "vbase")
	if track.URL != "https://cdn.example.test/dash/root/period/set/rep/" {
		t.Fatalf("track URL = %q", track.URL)
	}
	if track.Init == nil || track.Init.URL != "https://cdn.example.test/dash/root/period/set/rep/init.mp4" {
		t.Fatalf("init = %+v", track.Init)
	}
	assertStrings(t, "segments", segmentURLs(track), []string{
		"https://cdn.example.test/dash/root/period/set/rep/seg-1.m4s",
		"https://cdn.example.test/dash/root/period/set/rep/seg-2.m4s",
	})
}

func TestParseManifestSingleFileRepresentation(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "single-file.mpd"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	parsed, err := url.Parse(manifestBase)
	if err != nil {
		t.Fatalf("parse manifest URL: %v", err)
	}
	tracks, err := ParseManifest(f, parsed)
	if err != nil {
		if errors.Is(err, ErrDRM) {
			t.Fatalf("manifest was treated as DRM: %v", err)
		}
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(tracks))
	}
	track := tracks[0]
	if track.pending != nil {
		t.Fatal("single-file representation must not stay pending")
	}
	if track.Init != nil {
		t.Fatalf("init = %+v, want nil", track.Init)
	}
	if len(track.Segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(track.Segments))
	}
	if track.Segments[0].URL != "https://cdn.example.test/dash/video.mp4" {
		t.Fatalf("segment URL = %q", track.Segments[0].URL)
	}
	if track.Segments[0].Range != nil {
		t.Fatalf("segment range = %+v, want nil", track.Segments[0].Range)
	}
}

func TestParseManifestKeepsManifestQuery(t *testing.T) {
	tracks := loadManifest(t, "segmenttemplate-number.mpd", manifestBase+"?token=abc&exp=1")
	track := findTrack(t, tracks, "v1080")
	if track.baseQuery != "token=abc&exp=1" {
		t.Fatalf("baseQuery = %q", track.baseQuery)
	}
	// The segment URL itself stays clean; the query is appended on download.
	if got := track.Segments[0].URL; strings.Contains(got, "?") {
		t.Fatalf("segment URL carries a query: %q", got)
	}
}

func TestParseManifestDRM(t *testing.T) {
	fixtures := []string{
		"drm-mpd.mpd",
		"drm-period.mpd",
		"drm-adaptationset.mpd",
		"drm-representation.mpd",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", fixture))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer f.Close()
			parsed, _ := url.Parse(manifestBase)
			_, err = ParseManifest(f, parsed)
			if !errors.Is(err, ErrDRM) {
				t.Fatalf("err = %v, want ErrDRM", err)
			}
			if err.Error() != "DRM-geschützter Stream wird nicht unterstützt" {
				t.Fatalf("message = %q", err.Error())
			}
		})
	}
}

func TestParseManifestUnsupportedAddressing(t *testing.T) {
	tests := []struct {
		fixture string
		want    string
	}{
		{"unsupported.mpd", "unsupported DASH addressing for representation no-addr"},
		{"segmentbase-noindex.mpd", "unsupported DASH addressing for representation no-index: SegmentBase without indexRange"},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer f.Close()
			parsed, _ := url.Parse(manifestBase)
			_, err = ParseManifest(f, parsed)
			if err == nil {
				t.Fatal("expected an addressing error")
			}
			if err.Error() != tc.want {
				t.Fatalf("err = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseManifestErrors(t *testing.T) {
	if _, err := ParseManifest(strings.NewReader("<MPD/>"), nil); err == nil {
		t.Fatal("a nil manifest URL must fail")
	} else if err.Error() != "no manifest URL" {
		t.Fatalf("err = %q", err.Error())
	}

	if _, err := parseManifestString(t, "<MPD><Period></MPD>", manifestBase); err == nil {
		t.Fatal("invalid XML must fail")
	} else if !strings.Contains(err.Error(), "parse MPD") {
		t.Fatalf("err = %q", err.Error())
	}

	empty := `<MPD mediaPresentationDuration="PT4S"><Period></Period></MPD>`
	if _, err := parseManifestString(t, empty, manifestBase); err == nil {
		t.Fatal("a manifest without representations must fail")
	} else if err.Error() != "no representation in DASH manifest" {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestParseManifestRepresentationIDFallback(t *testing.T) {
	document := `<MPD mediaPresentationDuration="PT4S"><Period>
	  <AdaptationSet id="v" contentType="video" mimeType="video/mp4">
	    <SegmentTemplate duration="4" timescale="1" initialization="init.mp4" media="seg-$Number$.m4s"/>
	    <Representation id="explicit" bandwidth="1"/>
	    <Representation bandwidth="2"/>
	  </AdaptationSet></Period></MPD>`
	tracks, err := parseManifestString(t, document, manifestBase)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(tracks))
	}
	if tracks[0].ID != "explicit" {
		t.Fatalf("tracks[0].ID = %q", tracks[0].ID)
	}
	if tracks[1].ID != "v-1" {
		t.Fatalf("tracks[1].ID = %q, want the adaptation set fallback", tracks[1].ID)
	}
}

func TestExpandTemplate(t *testing.T) {
	tests := []struct {
		name   string
		tmpl   string
		number int64
		time   int64
		want   string
	}{
		{"representation id", "video/$RepresentationID$/init.mp4", 1, 0, "video/v1/init.mp4"},
		{"padded number", "seg-$Number%05d$.m4s", 7, 0, "seg-00007.m4s"},
		{"plain number", "seg-$Number$.m4s", 7, 0, "seg-7.m4s"},
		{"bandwidth", "$Bandwidth$", 1, 0, "128000"},
		{"time", "$Time$.m4s", 1, 12, "12.m4s"},
		{"wide time", "$Time%08d$.m4s", 1, 12, "00000012.m4s"},
		{"no placeholder", "plain.m4s", 1, 0, "plain.m4s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expandTemplate(tc.tmpl, "v1", 128000, tc.number, tc.time)
			if got != tc.want {
				t.Fatalf("expandTemplate(%q) = %q, want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

func TestParseISODuration(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"PT12S", 12 * time.Second},
		{"PT1M30S", 90 * time.Second},
		{"PT1H2M3S", time.Hour + 2*time.Minute + 3*time.Second},
		{"P1DT2H", 26 * time.Hour},
		{"PT0.5S", 500 * time.Millisecond},
		{"", 0},
		{"12S", 0},
		{"garbage", 0},
	}
	for _, tc := range tests {
		if got := parseISODuration(tc.value); got != tc.want {
			t.Fatalf("parseISODuration(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestParseByteRange(t *testing.T) {
	assertRange(t, "closed", parseByteRange("100-200"), 100, 199)
	assertRange(t, "open end", parseByteRange("50-"), 50, -1)
	if parseByteRange("") != nil {
		t.Fatal("empty range must be nil")
	}
	if parseByteRange("50") != nil {
		t.Fatal("range without a dash must be nil")
	}
	if parseByteRange("a-b") != nil {
		t.Fatal("non-numeric range must be nil")
	}
}

// buildBox renders one ISO-BMFF box.
func buildBox(boxType string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[0:4], uint32(8+len(payload)))
	copy(box[4:8], boxType)
	copy(box[8:], payload)
	return box
}

type sidxRef struct {
	size     uint32
	duration uint32
}

// buildSidxBox renders a sidx box with the given references.
func buildSidxBox(version byte, timescale uint32, ept, firstOffset uint64, refs []sidxRef) []byte {
	payload := make([]byte, 0, 32+len(refs)*12)
	payload = append(payload, version, 0, 0, 0) // version + flags
	payload = binary.BigEndian.AppendUint32(payload, 1)
	payload = binary.BigEndian.AppendUint32(payload, timescale)
	if version == 0 {
		payload = binary.BigEndian.AppendUint32(payload, uint32(ept))
		payload = binary.BigEndian.AppendUint32(payload, uint32(firstOffset))
	} else {
		payload = binary.BigEndian.AppendUint64(payload, ept)
		payload = binary.BigEndian.AppendUint64(payload, firstOffset)
	}
	payload = append(payload, 0, 0) // reserved
	payload = binary.BigEndian.AppendUint16(payload, uint16(len(refs)))
	for _, ref := range refs {
		payload = binary.BigEndian.AppendUint32(payload, 0) // reference type + size
		payload = binary.BigEndian.AppendUint32(payload, ref.size)
		payload = binary.BigEndian.AppendUint32(payload, ref.duration)
	}
	return buildBox("sidx", payload)
}

// padSidxBox renders a sidx box with trailing filler so that it has exactly
// size bytes.
func padSidxBox(size int, version byte, timescale uint32, ept, firstOffset uint64, refs []sidxRef) []byte {
	box := buildSidxBox(version, timescale, ept, firstOffset, refs)
	if len(box) > size {
		panic("padSidxBox: box larger than the requested size")
	}
	binary.BigEndian.PutUint32(box[0:4], uint32(size))
	return append(box, make([]byte, size-len(box))...)
}

func TestParseSidxVersion0(t *testing.T) {
	sidx := buildSidxBox(0, 1000, 900, 40, []sidxRef{{size: 100, duration: 1000}, {size: 200, duration: 1000}})
	data := append(buildBox("ftyp", []byte("isom")), sidx...)

	info, err := ParseSidx(data)
	if err != nil {
		t.Fatalf("ParseSidx: %v", err)
	}
	if info.Timescale != 1000 || info.EarliestPresentationTime != 900 || info.FirstOffset != 40 {
		t.Fatalf("info = %+v", info)
	}
	if len(info.References) != 2 {
		t.Fatalf("references = %+v", info.References)
	}
	if info.References[0].ReferencedSize != 100 || info.References[0].SubsegmentDuration != 1000 {
		t.Fatalf("reference 0 = %+v", info.References[0])
	}
	if info.References[1].ReferencedSize != 200 {
		t.Fatalf("reference 1 = %+v", info.References[1])
	}
}

func TestParseSidxVersion1(t *testing.T) {
	sidx := buildSidxBox(1, 90000, 1<<33, 1<<32, []sidxRef{{size: 4096, duration: 90000}})
	info, err := ParseSidx(sidx)
	if err != nil {
		t.Fatalf("ParseSidx: %v", err)
	}
	if info.Timescale != 90000 {
		t.Fatalf("timescale = %d", info.Timescale)
	}
	if info.EarliestPresentationTime != 1<<33 || info.FirstOffset != 1<<32 {
		t.Fatalf("64-bit fields = %d/%d", info.EarliestPresentationTime, info.FirstOffset)
	}
	if len(info.References) != 1 || info.References[0].ReferencedSize != 4096 {
		t.Fatalf("references = %+v", info.References)
	}
}

func TestParseSidxErrors(t *testing.T) {
	if _, err := ParseSidx(buildBox("ftyp", []byte("isom"))); err == nil {
		t.Fatal("data without a sidx box must fail")
	} else if err.Error() != "no sidx box found" {
		t.Fatalf("err = %q", err.Error())
	}

	truncated := buildBox("sidx", []byte{0, 0, 0, 0, 0, 0, 0, 1})
	if _, err := ParseSidx(truncated); err == nil {
		t.Fatal("a truncated sidx box must fail")
	}

	empty := buildSidxBox(0, 1000, 0, 0, nil)
	if _, err := ParseSidx(empty); err == nil {
		t.Fatal("a sidx box without references must fail")
	} else if err.Error() != "sidx box without references" {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestFindBoxLargeSize(t *testing.T) {
	payload := make([]byte, 16)
	box := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(box[0:4], 1) // 64-bit size
	copy(box[4:8], "mdat")
	binary.BigEndian.PutUint64(box[8:16], uint64(len(box)))
	for i := range payload {
		box[16+i] = byte(i)
	}
	got := findBox(box, "mdat")
	if len(got) != len(payload) {
		t.Fatalf("findBox payload length = %d, want %d", len(got), len(payload))
	}
}
