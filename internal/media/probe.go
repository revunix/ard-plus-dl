package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ErrNoMediaInfo is returned when ffprobe produced no output at all. Callers
// keep the staging name in that case.
var ErrNoMediaInfo = errors.New("no media information from ffprobe")

var germanLanguage = regexp.MustCompile(`^(?i:ger|deu|de)$`)

type ffprobeStream struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Profile   string `json:"profile"`
	Height    int    `json:"height"`
	Channels  int    `json:"channels"`
	Tags      struct {
		Language string `json:"language"`
	} `json:"tags"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
}

// ProbeMediaTag runs ffprobe and returns
// "German.<audio>.<resolution>.Web-DL.<video>" for a finished file. The German
// audio track is preferred; otherwise the first audio track is used. A stream
// kind that is missing or unknown is labelled "Unknown", matching the Bash
// implementation.
func ProbeMediaTag(ffprobe, path string) (string, error) {
	cmd := exec.Command(ffprobe, "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,profile,height,channels:stream_tags=language",
		"-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe %s: %w", path, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "", ErrNoMediaInfo
	}
	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("ffprobe output: %w", err)
	}

	var video, german, firstAudio *ffprobeStream
	for i := range parsed.Streams {
		s := &parsed.Streams[i]
		switch s.CodecType {
		case "video":
			if video == nil {
				video = s
			}
		case "audio":
			if firstAudio == nil {
				firstAudio = s
			}
			if german == nil && germanLanguage.MatchString(strings.TrimSpace(s.Tags.Language)) {
				german = s
			}
		}
	}
	audio := german
	if audio == nil {
		audio = firstAudio
	}

	var audioCodec, profile string
	var channels int
	if audio != nil {
		audioCodec, channels, profile = audio.CodecName, audio.Channels, audio.Profile
	}
	var videoCodec string
	var height int
	if video != nil {
		videoCodec, height = video.CodecName, video.Height
	}

	return fmt.Sprintf("German.%s.%s.Web-DL.%s",
		AudioCodecLabel(audioCodec, channels, profile),
		ResolutionLabel(height),
		VideoCodecLabel(videoCodec)), nil
}
