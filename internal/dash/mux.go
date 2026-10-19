package dash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DownloadedTrack is one track's segments on disk, in playback order.
type DownloadedTrack struct {
	Track *Track
	Files []string
}

// Muxer concatenates the downloaded segments and muxes them into one MP4 with
// ffmpeg.
type Muxer struct {
	FFmpeg string
	Logf   func(format string, args ...any)
}

func (m *Muxer) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

func (m *Muxer) ffmpeg() string {
	if m.FFmpeg != "" {
		return m.FFmpeg
	}
	return "ffmpeg"
}

// Mux writes out+".mp4". Subtitle tracks that cannot be prepared are skipped
// with a warning instead of failing the download.
func (m *Muxer) Mux(ctx context.Context, workDir string, tracks []DownloadedTrack, out string) error {
	var inputs []string
	var kinds []Kind
	var langs []string
	for i, downloaded := range tracks {
		if len(downloaded.Files) == 0 {
			continue
		}
		path, err := m.prepareInput(ctx, workDir, i, downloaded)
		if err != nil {
			if downloaded.Track.Kind == KindSubtitle {
				m.logf("WARNUNG: Untertitel %s konnte nicht aufbereitet werden: %v", downloaded.Track.ID, err)
				continue
			}
			return err
		}
		inputs = append(inputs, path)
		kinds = append(kinds, downloaded.Track.Kind)
		langs = append(langs, normalizeLang(downloaded.Track.Lang))
	}
	if len(inputs) == 0 {
		return errors.New("no tracks to mux")
	}

	target := out + ".mp4"
	// The temporary name must keep a known extension: ffmpeg refuses to pick
	// a muxer for "*.mp4.tmp".
	tmp := out + ".tmp.mp4"
	args := buildMuxArgs(inputs, kinds, langs, tmp)
	stderr, err := m.run(ctx, args)
	if err != nil {
		// Some codec combinations cannot be copied into MP4; transcode the
		// audio and try once more before giving up.
		args = buildMuxArgs(inputs, kinds, langs, tmp)
		for i := range args {
			if args[i] == "copy" && i > 0 && args[i-1] == "-c:a" {
				args[i] = "aac"
			}
		}
		// ffmpeg ignores options that follow the output file, so the AAC
		// bitrate has to be inserted before it.
		output := args[len(args)-1]
		args = append(args[:len(args)-1], "-b:a", "192k", output)
		m.logf("WARNUNG: Muxen mit Copy fehlgeschlagen, neuer Versuch mit AAC-Audio")
		stderr, err = m.run(ctx, args)
		if err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("ffmpeg mux failed: %w (%s)", err, tailLines(stderr, 20))
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	return nil
}

func (m *Muxer) run(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, m.ffmpeg(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	err := cmd.Run()
	return stderr.String(), err
}

// buildMuxArgs maps every input explicitly so multi-audio files keep all
// tracks and subtitles become mov_text.
func buildMuxArgs(inputs []string, kinds []Kind, langs []string, output string) []string {
	args := []string{"-y", "-v", "error"}
	for _, input := range inputs {
		args = append(args, "-i", input)
	}

	videoIdx := -1
	var audioIdx, subIdx []int
	for i, kind := range kinds {
		switch kind {
		case KindVideo:
			if videoIdx < 0 {
				videoIdx = i
			}
		case KindAudio:
			audioIdx = append(audioIdx, i)
		case KindSubtitle:
			subIdx = append(subIdx, i)
		}
	}
	if videoIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("%d:v:0", videoIdx))
	}
	for _, i := range audioIdx {
		args = append(args, "-map", fmt.Sprintf("%d:a:0", i))
	}
	for _, i := range subIdx {
		args = append(args, "-map", fmt.Sprintf("%d:0", i))
	}
	args = append(args, "-c:v", "copy", "-c:a", "copy", "-c:s", "mov_text")
	for n, i := range audioIdx {
		if langs[i] != "" {
			args = append(args, fmt.Sprintf("-metadata:s:a:%d", n), "language="+langs[i])
		}
	}
	for n, i := range subIdx {
		if langs[i] != "" {
			args = append(args, fmt.Sprintf("-metadata:s:s:%d", n), "language="+langs[i])
		}
	}
	args = append(args, "-movflags", "+faststart", output)
	return args
}

func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return ""
	}
	lang, _, _ = strings.Cut(lang, "-")
	lang, _, _ = strings.Cut(lang, "_")
	return lang
}

// prepareInput concatenates the track files into workDir and returns the path
// ffmpeg should read. ISO-BMFF subtitles are extracted to SRT first.
func (m *Muxer) prepareInput(ctx context.Context, workDir string, index int, downloaded DownloadedTrack) (string, error) {
	ext := ".mp4"
	isoSubtitle := false
	if downloaded.Track.Kind == KindSubtitle {
		mime := strings.ToLower(downloaded.Track.MimeType)
		codecs := strings.ToLower(downloaded.Track.Codecs)
		switch {
		case strings.Contains(mime, "ttml"):
			ext = ".ttml"
		case strings.Contains(mime, "vtt"):
			ext = ".vtt"
		default:
			isoSubtitle = strings.Contains(codecs, "wvtt") || strings.Contains(codecs, "stpp") || strings.Contains(mime, "mp4")
		}
	}

	concatPath := filepath.Join(workDir, fmt.Sprintf("track%d%s", index, ext))
	if err := concatFiles(concatPath, downloaded.Files); err != nil {
		return "", err
	}
	if !isoSubtitle {
		return concatPath, nil
	}

	srtPath := filepath.Join(workDir, fmt.Sprintf("track%d.srt", index))
	stderr, err := m.run(ctx, []string{"-y", "-v", "error", "-i", concatPath, "-map", "0:s:0", "-f", "srt", srtPath})
	if err != nil {
		return "", fmt.Errorf("subtitle extraction failed: %w (%s)", err, tailLines(stderr, 5))
	}
	return srtPath, nil
}

func concatFiles(dest string, files []string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	for _, file := range files {
		in, err := os.Open(file)
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := in.Close()
		if copyErr != nil {
			out.Close()
			return copyErr
		}
		if closeErr != nil {
			out.Close()
			return closeErr
		}
	}
	return out.Close()
}

func tailLines(text string, want int) string {
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > want {
		lines = lines[len(lines)-want:]
	}
	return strings.Join(lines, " | ")
}
