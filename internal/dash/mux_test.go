package dash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeFFmpegScript = `#!/bin/sh
{
  printf '%s\n' "$@"
  printf -- '---\n'
} >> "$FFMPEG_ARGS"
for arg in "$@"; do
  if [ "$arg" = srt ]; then
    exit 3
  fi
done
if [ -n "$FFMPEG_FAIL_FIRST" ] && [ ! -f "$FFMPEG_FAILED_MARKER" ]; then
  : > "$FFMPEG_FAILED_MARKER"
  exit 1
fi
if [ -n "$FFMPEG_ALWAYS_FAIL" ]; then
  echo "boom-line-one" >&2
  echo "boom-line-two" >&2
  exit 1
fi
last=''
for arg in "$@"; do
  last="$arg"
done
: > "$last"
exit 0
`

// fakeFFmpeg installs a recording ffmpeg stand-in and returns its path plus
// the file that collects the argument vectors.
func fakeFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(bin, []byte(fakeFFmpegScript), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("FFMPEG_ARGS", argsFile)
	return bin, argsFile
}

func readInvocations(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read args file: %v", err)
	}
	var invocations [][]string
	var current []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "---" {
			invocations = append(invocations, current)
			current = nil
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		invocations = append(invocations, current)
	}
	return invocations
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestMuxerArgumentVector(t *testing.T) {
	bin, argsFile := fakeFFmpeg(t)
	sourceDir := t.TempDir()
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	out := filepath.Join(t.TempDir(), "Final Name")

	tracks := []DownloadedTrack{
		{
			Track: &Track{ID: "v", Kind: KindVideo, Lang: "de"},
			Files: []string{
				writeTempFile(t, sourceDir, "v1.m4s", "video-one"),
				writeTempFile(t, sourceDir, "v2.m4s", "video-two"),
			},
		},
		{
			Track: &Track{ID: "a", Kind: KindAudio, Lang: "de-DE", Channels: 2},
			Files: []string{writeTempFile(t, sourceDir, "a.m4s", "audio")},
		},
		{
			Track: &Track{ID: "s", Kind: KindSubtitle, Lang: "de", MimeType: "text/vtt", Codecs: "wvtt"},
			Files: []string{writeTempFile(t, sourceDir, "s.vtt", "WEBVTT")},
		},
	}

	var logs []string
	muxer := &Muxer{FFmpeg: bin, Logf: func(format string, args ...any) { logs = append(logs, format) }}
	if err := muxer.Mux(context.Background(), workDir, tracks, out); err != nil {
		t.Fatalf("Mux: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("logs = %v, want none", logs)
	}
	if _, err := os.Stat(out + ".mp4"); err != nil {
		t.Fatalf("output missing: %v", err)
	}

	invocations := readInvocations(t, argsFile)
	if len(invocations) != 1 {
		t.Fatalf("invocations = %d, want 1", len(invocations))
	}
	want := []string{
		"-y", "-v", "error",
		"-i", filepath.Join(workDir, "track0.mp4"),
		"-i", filepath.Join(workDir, "track1.mp4"),
		"-i", filepath.Join(workDir, "track2.vtt"),
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-map", "2:0",
		"-c:v", "copy",
		"-c:a", "copy",
		"-c:s", "mov_text",
		"-metadata:s:a:0", "language=de",
		"-metadata:s:s:0", "language=de",
		"-movflags", "+faststart",
		out + ".tmp.mp4",
	}
	assertStrings(t, "ffmpeg args", invocations[0], want)

	// The temporary output must keep an extension ffmpeg can map to a muxer,
	// and must not collide with the final name.
	tmpArg := invocations[0][len(invocations[0])-1]
	if !strings.HasSuffix(tmpArg, ".mp4") {
		t.Fatalf("temporary output %q does not end in a known extension", tmpArg)
	}
	if tmpArg == out+".mp4" {
		t.Fatalf("temporary output %q equals the final name", tmpArg)
	}

	// The segments of each track are concatenated into the intermediate files.
	if got := readFile(t, filepath.Join(workDir, "track0.mp4")); got != "video-onevideo-two" {
		t.Fatalf("concatenated video = %q", got)
	}
	if got := readFile(t, filepath.Join(workDir, "track1.mp4")); got != "audio" {
		t.Fatalf("concatenated audio = %q", got)
	}
	if got := readFile(t, filepath.Join(workDir, "track2.vtt")); got != "WEBVTT" {
		t.Fatalf("concatenated subtitle = %q", got)
	}
}

func TestMuxerFallbackTranscode(t *testing.T) {
	bin, argsFile := fakeFFmpeg(t)
	t.Setenv("FFMPEG_FAIL_FIRST", "1")
	t.Setenv("FFMPEG_FAILED_MARKER", filepath.Join(t.TempDir(), "failed-once"))
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	out := filepath.Join(t.TempDir(), "Out")

	tracks := []DownloadedTrack{
		{Track: &Track{ID: "v", Kind: KindVideo}, Files: []string{writeTempFile(t, t.TempDir(), "v.m4s", "v")}},
		{Track: &Track{ID: "a", Kind: KindAudio, Lang: "de"}, Files: []string{writeTempFile(t, t.TempDir(), "a.m4s", "a")}},
	}

	var logs []string
	muxer := &Muxer{FFmpeg: bin, Logf: func(format string, args ...any) { logs = append(logs, format) }}
	if err := muxer.Mux(context.Background(), workDir, tracks, out); err != nil {
		t.Fatalf("Mux: %v", err)
	}
	if _, err := os.Stat(out + ".mp4"); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	invocations := readInvocations(t, argsFile)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %d, want the copy try plus the AAC retry", len(invocations))
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Muxen mit Copy fehlgeschlagen") {
		t.Fatalf("logs = %v, want the fallback warning", logs)
	}

	first := strings.Join(invocations[0], " ")
	second := strings.Join(invocations[1], " ")
	if !strings.Contains(first, "-c:a copy") {
		t.Fatalf("first invocation = %s, want -c:a copy", first)
	}
	if strings.Contains(second, "-c:a copy") {
		t.Fatalf("second invocation = %s, still uses copy", second)
	}
	if !strings.Contains(second, "-c:a aac") {
		t.Fatalf("second invocation = %s, want -c:a aac", second)
	}
	// The bitrate must precede the output file, otherwise ffmpeg ignores it.
	if !strings.HasSuffix(second, "-b:a 192k "+out+".tmp.mp4") {
		t.Fatalf("second invocation = %s, want the AAC bitrate before the output", second)
	}
}

func TestMuxerFailureReturnsStderrTail(t *testing.T) {
	bin, _ := fakeFFmpeg(t)
	t.Setenv("FFMPEG_ALWAYS_FAIL", "1")
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	out := filepath.Join(t.TempDir(), "Out")
	tracks := []DownloadedTrack{
		{Track: &Track{ID: "v", Kind: KindVideo}, Files: []string{writeTempFile(t, t.TempDir(), "v.m4s", "v")}},
	}

	muxer := &Muxer{FFmpeg: bin}
	err := muxer.Mux(context.Background(), workDir, tracks, out)
	if err == nil {
		t.Fatal("a failing ffmpeg must fail the mux")
	}
	if !strings.Contains(err.Error(), "ffmpeg mux failed") {
		t.Fatalf("err = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "boom-line-two") {
		t.Fatalf("err = %q, want the stderr tail", err.Error())
	}
	if _, statErr := os.Stat(out + ".mp4"); !os.IsNotExist(statErr) {
		t.Fatalf("output must not exist after a failure: %v", statErr)
	}
	if _, statErr := os.Stat(out + ".tmp.mp4"); !os.IsNotExist(statErr) {
		t.Fatalf("the temporary output must be removed: %v", statErr)
	}
}

func TestMuxerSubtitleExtractionWarning(t *testing.T) {
	bin, argsFile := fakeFFmpeg(t)
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	out := filepath.Join(t.TempDir(), "Out")

	tracks := []DownloadedTrack{
		{Track: &Track{ID: "v", Kind: KindVideo}, Files: []string{writeTempFile(t, t.TempDir(), "v.m4s", "v")}},
		{
			Track: &Track{ID: "s", Kind: KindSubtitle, Lang: "de", MimeType: "application/mp4", Codecs: "wvtt"},
			Files: []string{writeTempFile(t, t.TempDir(), "s.mp4", "sub")},
		},
	}

	var logs []string
	muxer := &Muxer{FFmpeg: bin, Logf: func(format string, args ...any) { logs = append(logs, format) }}
	if err := muxer.Mux(context.Background(), workDir, tracks, out); err != nil {
		t.Fatalf("Mux: %v", err)
	}
	if _, err := os.Stat(out + ".mp4"); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "WARNUNG: Untertitel") {
		t.Fatalf("logs = %v, want the subtitle warning", logs)
	}
	invocations := readInvocations(t, argsFile)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %d, want the extraction attempt plus the mux", len(invocations))
	}
	if !strings.Contains(strings.Join(invocations[0], " "), "-f srt") {
		t.Fatalf("extraction invocation = %v", invocations[0])
	}
	mux := strings.Join(invocations[1], " ")
	if strings.Count(mux, "-i ") != 1 {
		t.Fatalf("mux invocation = %s, want only the video input", mux)
	}
}

func TestPrepareInputExtensions(t *testing.T) {
	bin, _ := fakeFFmpeg(t)
	workDir := t.TempDir()
	sourceDir := t.TempDir()
	muxer := &Muxer{FFmpeg: bin}

	tests := []struct {
		name     string
		mimeType string
		codecs   string
		want     string
	}{
		{"webvtt", "text/vtt", "", "track0.vtt"},
		{"ttml", "application/ttml+xml", "stpp", "track0.ttml"},
		{"plain", "text/plain", "", "track0.mp4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			track := &Track{ID: "s", Kind: KindSubtitle, MimeType: tc.mimeType, Codecs: tc.codecs}
			downloaded := DownloadedTrack{Track: track, Files: []string{writeTempFile(t, sourceDir, tc.name, "x")}}
			path, err := muxer.prepareInput(context.Background(), workDir, 0, downloaded)
			if err != nil {
				t.Fatalf("prepareInput: %v", err)
			}
			if filepath.Base(path) != tc.want {
				t.Fatalf("input path = %q, want %q", filepath.Base(path), tc.want)
			}
		})
	}
}

func TestMuxerNoTracks(t *testing.T) {
	muxer := &Muxer{FFmpeg: "/nonexistent/ffmpeg"}
	err := muxer.Mux(context.Background(), t.TempDir(), nil, filepath.Join(t.TempDir(), "out"))
	if err == nil {
		t.Fatal("Mux without tracks must fail")
	}
	if err.Error() != "no tracks to mux" {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestBuildMuxArgsOrdering(t *testing.T) {
	inputs := []string{"a.mp4", "v.mp4", "s.vtt"}
	kinds := []Kind{KindAudio, KindVideo, KindSubtitle}
	langs := []string{"de", "", "en"}
	args := buildMuxArgs(inputs, kinds, langs, "out.mp4")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-map 1:v:0", "-map 0:a:0", "-map 2:0", "-metadata:s:a:0 language=de", "-metadata:s:s:0 language=en"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %s, want %s", joined, want)
		}
	}
	if strings.Contains(joined, "language=") && strings.Count(joined, "language=") != 2 {
		t.Fatalf("args = %s, want language metadata for audio and subtitles only", joined)
	}
	if !strings.HasSuffix(joined, "-movflags +faststart out.mp4") {
		t.Fatalf("args = %s", joined)
	}
}

func TestNormalizeLang(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"de", "de"},
		{"De-DE", "de"},
		{"en_US", "en"},
		{" de ", "de"},
		{"", ""},
		{"ger", "ger"},
	}
	for _, tc := range tests {
		if got := normalizeLang(tc.value); got != tc.want {
			t.Fatalf("normalizeLang(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
