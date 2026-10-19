package dash

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Selection is the set of tracks that get downloaded and muxed.
type Selection struct {
	Video     *Track
	Audio     []Track
	Subtitles []Track
}

// subLangPattern selects the German and English subtitle tracks, matching the
// `en.*,de.*` language filter of the original pipeline.
var subLangPattern = regexp.MustCompile(`(?i)^(de|en)([._-].*)?$`)

// SelectTracks picks the best video representation, every audio
// representation and the German/English subtitles.
func SelectTracks(tracks []Track) (Selection, error) {
	var selection Selection

	var bestVideo *Track
	var audio []Track
	seenAudio := map[string]bool{}
	subtitleByLang := map[string]bool{}

	for i := range tracks {
		track := tracks[i]
		switch track.Kind {
		case KindVideo:
			if bestVideo == nil ||
				track.Width*track.Height > bestVideo.Width*bestVideo.Height ||
				(track.Width*track.Height == bestVideo.Width*bestVideo.Height && track.Bandwidth > bestVideo.Bandwidth) {
				candidate := track
				bestVideo = &candidate
			}
		case KindAudio:
			key := strings.ToLower(strings.Join([]string{
				track.Lang,
				track.Role,
				channelKey(track.Channels),
			}, "|"))
			if seenAudio[key] {
				continue
			}
			seenAudio[key] = true
			audio = append(audio, track)
		case KindSubtitle:
			lang := strings.ToLower(strings.TrimSpace(track.Lang))
			if !subLangPattern.MatchString(lang) || subtitleByLang[lang] {
				continue
			}
			subtitleByLang[lang] = true
			selection.Subtitles = append(selection.Subtitles, track)
		}
	}

	selection.Video = bestVideo
	selection.Audio = audio
	if bestVideo == nil && len(audio) == 0 {
		return Selection{}, errors.New("no playable representation in DASH manifest")
	}
	return selection, nil
}

func channelKey(channels int) string {
	if channels == 0 {
		return ""
	}
	return strconv.Itoa(channels)
}
