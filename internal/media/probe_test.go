package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeFile creates a file with the given content and mode.
func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// fakeFFprobe writes a shell script into a temporary directory that behaves
// like the ffprobe stub of the Bash suite: it prints the contents of a JSON
// file and exits with the requested status. When argsFile is not empty the
// script also records its argument vector there.
func fakeFFprobe(t *testing.T, json string, status int, argsFile string) string {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "streams.json")
	writeFile(t, data, json, 0o644)
	bin := filepath.Join(dir, "ffprobe")
	script := "#!/bin/sh\n"
	if argsFile != "" {
		script += "printf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
	}
	script += "cat " + data + "\n"
	script += "exit " + strconv.Itoa(status) + "\n"
	writeFile(t, bin, script, 0o755)
	return bin
}

func TestProbeMediaTag(t *testing.T) {
	t.Run("the German audio track is preferred", func(t *testing.T) {
		json := `{"streams":[` +
			`{"codec_type":"video","codec_name":"hevc","height":2160},` +
			`{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"eng"}},` +
			`{"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"ger"}}]}`
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
		if err != nil {
			t.Fatalf("ProbeMediaTag: %v", err)
		}
		if tag != "German.AC3.6ch.2160p.Web-DL.x265" {
			t.Fatalf("tag = %q", tag)
		}
	})

	t.Run("the German language match is case insensitive", func(t *testing.T) {
		json := `{"streams":[` +
			`{"codec_type":"video","codec_name":"h264","height":1080},` +
			`{"codec_type":"audio","codec_name":"ac3","channels":2,"tags":{"language":"GER"}},` +
			`{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"eng"}}]}`
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
		if err != nil {
			t.Fatalf("ProbeMediaTag: %v", err)
		}
		if tag != "German.AC3.1080p.Web-DL.x264" {
			t.Fatalf("tag = %q", tag)
		}
	})

	t.Run("other German language tags are accepted", func(t *testing.T) {
		for _, lang := range []string{"deu", "de", "deu "} {
			t.Run(lang, func(t *testing.T) {
				json := `{"streams":[` +
					`{"codec_type":"video","codec_name":"h264","height":720},` +
					`{"codec_type":"audio","codec_name":"eac3","channels":6,"tags":{"language":"` + lang + `"}}]}`
				file := filepath.Join(t.TempDir(), "video.mp4")
				writeFile(t, file, "video", 0o644)
				tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
				if err != nil {
					t.Fatalf("ProbeMediaTag: %v", err)
				}
				if tag != "German.EAC3.6ch.720p.Web-DL.x264" {
					t.Fatalf("tag = %q", tag)
				}
			})
		}
	})

	t.Run("a missing German track falls back to the first audio track", func(t *testing.T) {
		json := `{"streams":[` +
			`{"codec_type":"video","codec_name":"h264","height":720},` +
			`{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"eng"}},` +
			`{"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"fra"}}]}`
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
		if err != nil {
			t.Fatalf("ProbeMediaTag: %v", err)
		}
		if tag != "German.AAC.720p.Web-DL.x264" {
			t.Fatalf("tag = %q", tag)
		}
	})

	t.Run("a missing audio track is reported as unknown", func(t *testing.T) {
		json := `{"streams":[{"codec_type":"video","codec_name":"h264","height":720}]}`
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
		if err != nil {
			t.Fatalf("ProbeMediaTag: %v", err)
		}
		if tag != "German.Unknown.720p.Web-DL.x264" {
			t.Fatalf("tag = %q", tag)
		}
	})

	t.Run("unknown codecs are uppercased", func(t *testing.T) {
		json := `{"streams":[` +
			`{"codec_type":"video","codec_name":"mpeg4","height":1080},` +
			`{"codec_type":"audio","codec_name":"dtsx","channels":2,"tags":{"language":"ger"}}]}`
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
		if err != nil {
			t.Fatalf("ProbeMediaTag: %v", err)
		}
		if tag != "German.DTSX.1080p.Web-DL.MPEG4" {
			t.Fatalf("tag = %q", tag)
		}
	})

	t.Run("the profile selects the DTS-HD label", func(t *testing.T) {
		json := `{"streams":[` +
			`{"codec_type":"video","codec_name":"h264","height":1080},` +
			`{"codec_type":"audio","codec_name":"dts","channels":8,"profile":"DTS-HD MA","tags":{"language":"ger"}}]}`
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		tag, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, ""), file)
		if err != nil {
			t.Fatalf("ProbeMediaTag: %v", err)
		}
		if tag != "German.DTS-HD.MA.1080p.Web-DL.x264" {
			t.Fatalf("tag = %q", tag)
		}
	})

	t.Run("ffprobe failure is reported", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		if _, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, "", 1, ""), file); err == nil {
			t.Fatal("expected an error for a failing ffprobe")
		}
	})

	t.Run("no JSON output is reported", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		_, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, "  \n", 0, ""), file)
		if !errors.Is(err, ErrNoMediaInfo) {
			t.Fatalf("err = %v, want ErrNoMediaInfo", err)
		}
	})

	t.Run("unparseable JSON is reported", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "video.mp4")
		writeFile(t, file, "video", 0o644)
		if _, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, "not json at all", 0, ""), file); err == nil {
			t.Fatal("expected an error for unparseable output")
		}
	})
}

func TestProbeMediaTagPassesTheExpectedCommandLine(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	json := `{"streams":[{"codec_type":"video","codec_name":"h264","height":1080},{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"ger"}}]}`
	file := filepath.Join(t.TempDir(), "my video.mp4")
	writeFile(t, file, "video", 0o644)

	if _, err := ProbeMediaTag(context.Background(), fakeFFprobe(t, json, 0, argsFile), file); err != nil {
		t.Fatalf("ProbeMediaTag: %v", err)
	}
	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", argsFile, err)
	}
	args := strings.Split(strings.TrimRight(string(recorded), "\n"), "\n")
	want := []string{
		"-v",
		"error",
		"-show_entries",
		"stream=codec_type,codec_name,profile,height,channels:stream_tags=language",
		"-of",
		"json",
		file,
	}
	if len(args) != len(want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %q, want %q", args, want)
		}
	}
}
