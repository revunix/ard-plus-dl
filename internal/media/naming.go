// Package media implements ARD Plus output naming and media probing.
//
// The naming rules follow the original implementation: the same path
// component limits, the same trimming rules and the same media tag layout, so
// an existing library keeps its file names.
package media

import (
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	// pathComponentMaxBytes keeps one path component plus ".mp4" and temporary
	// suffixes below the 255-byte NAME_MAX of APFS/ext4/tmpfs.
	pathComponentMaxBytes = 200
	// stemMaxBytes is shorter because the media tag and the file tag are
	// appended afterwards.
	stemMaxBytes = 170
)

// windowsNaming is a variable so tests can exercise the Windows rules on any
// host.
var windowsNaming = runtime.GOOS == "windows"

func isSpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

func leadingSpaceLen(s string) int {
	i := 0
	for i < len(s) && isSpaceByte(s[i]) {
		i++
	}
	return i
}

func trailingSpaceLen(s string) int {
	i := 0
	for i < len(s) && isSpaceByte(s[len(s)-1-i]) {
		i++
	}
	return i
}

// TrimPathComponent removes leading and trailing spaces and dots until the
// string is stable, then collapses inner space runs. Removing a dot can expose
// a space and the other way round, so the trim repeats.
func TrimPathComponent(s string) string {
	for {
		hasLead := len(s) > 0 && isSpaceByte(s[0])
		hasTrail := len(s) > 0 && isSpaceByte(s[len(s)-1])
		if !hasLead && !hasTrail && !strings.HasPrefix(s, ".") && !strings.HasSuffix(s, ".") {
			break
		}
		s = s[leadingSpaceLen(s):]
		s = s[:len(s)-trailingSpaceLen(s)]
		s = strings.TrimPrefix(s, ".")
		s = strings.TrimSuffix(s, ".")
		if s == "" {
			break
		}
	}
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
}

func isWindowsReserved(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	upper := strings.ToUpper(base)
	if windowsReservedNames[upper] {
		return true
	}
	if len(upper) == 4 {
		prefix := upper[:3]
		if (prefix == "COM" || prefix == "LPT") && upper[3] >= '1' && upper[3] <= '9' {
			return true
		}
	}
	return false
}

// SanitizePathComponent makes one path component safe for the current
// platform. Unix keeps every character except path separators, control
// characters and the glob characters used by the skip checks; Windows also
// rejects its reserved characters and device names.
func SanitizePathComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '/':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// control characters are dropped
		case r == '*' || r == '?' || r == '[' || r == ']':
			// dropped: the output globs treat them as wildcards
		case windowsNaming && (r == '<' || r == '>' || r == ':' || r == '"' || r == '\\' || r == '|'):
			// dropped on Windows only
		default:
			b.WriteRune(r)
		}
	}
	out := TrimPathComponent(b.String())
	if windowsNaming && isWindowsReserved(out) {
		out = "_" + out
	}
	if out == "" {
		out = "_"
	}
	return out
}

// CapComponentBytes truncates s so its UTF-8 length stays within max bytes,
// always on a rune boundary.
func CapComponentBytes(max int, s string) string {
	if len(s) <= max {
		return s
	}
	var b strings.Builder
	b.Grow(max)
	runes := 0
	for _, r := range s {
		if runes >= max {
			break
		}
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if b.Len()+size > max {
			break
		}
		b.WriteRune(r)
		runes++
	}
	return b.String()
}

func cappedTrimmmed(max int, s string) string {
	return TrimPathComponent(CapComponentBytes(max, s))
}

// NormalizeURL strips the fragment, the query, surrounding whitespace and a
// single trailing slash.
func NormalizeURL(raw string) string {
	u := raw
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	u = u[:len(u)-trailingSpaceLen(u)]
	u = u[leadingSpaceLen(u):]
	u = strings.TrimSuffix(u, "/")
	return u
}

// BuildMoviePath returns the staging path for a movie relative to the
// downloads directory: "<Title> (<Year>)/<Title> (<Year>)".
func BuildMoviePath(title, year string) string {
	safe := cappedTrimmmed(pathComponentMaxBytes, SanitizePathComponent(title))
	if year != "" {
		return fmt.Sprintf("%s (%s)/%s", safe, year, safe)
	}
	return safe + "/" + safe
}

// BuildEpisodePath returns the staging path for an episode:
// "<Show>/Season NN/<Show> SNNENN - <Episode title>".
func BuildEpisodePath(show string, season int, episodeNo *int, title string) string {
	safeShow := cappedTrimmmed(pathComponentMaxBytes, SanitizePathComponent(show))
	safeTitle := SanitizePathComponent(title)
	episodeLabel := "??"
	if episodeNo != nil {
		episodeLabel = fmt.Sprintf("%02d", *episodeNo)
	}
	stem := fmt.Sprintf("%s S%02dE%s - %s", safeShow, season, episodeLabel, safeTitle)
	return fmt.Sprintf("%s/Season %02d/%s", safeShow, season, cappedTrimmmed(pathComponentMaxBytes, stem))
}

// BuildTatortPath returns the staging name for a Tatort episode. It is a single
// path component.
func BuildTatortPath(city, team string, episodeNo, title, year string) string {
	safeCity := SanitizePathComponent(city)
	safeTeam := SanitizePathComponent(team)
	safeName := SanitizePathComponent(title)

	out := "Tatort " + safeCity
	if team != "" {
		out = fmt.Sprintf("%s (%s)", out, safeTeam)
	}
	if episodeNo != "" {
		out = fmt.Sprintf("%s - Folge %s", out, episodeNo)
	}
	if year != "" {
		out = fmt.Sprintf("%s - %s (%s)", out, safeName, year)
	} else {
		out = fmt.Sprintf("%s - %s", out, safeName)
	}
	return cappedTrimmmed(pathComponentMaxBytes, out)
}

// DotsComponent replaces spaces with dots, as used by the final output names.
func DotsComponent(s string) string {
	out := strings.ReplaceAll(SanitizePathComponent(s), " ", ".")
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	out = TrimPathComponent(out)
	if out == "" {
		out = "_"
	}
	return out
}

// EpisodePadding returns the number of digits used for episode numbers in a
// season: three for seasons with 100 or more episodes.
func EpisodePadding(maxEpisode int) int {
	if maxEpisode >= 100 {
		return 3
	}
	return 2
}

// EpisodeNameStem returns "<Show>.SNNENN" for the final file name.
func EpisodeNameStem(show string, season int, episodeNo *int, padding int) string {
	label := "??"
	if episodeNo != nil {
		label = fmt.Sprintf("%0*d", padding, *episodeNo)
	}
	return CapComponentBytes(stemMaxBytes, fmt.Sprintf("%s.S%02dE%s", DotsComponent(show), season, label))
}

// SeasonNameStem returns "<Show>.SNN" for the final directory name.
func SeasonNameStem(show string, season int) string {
	return CapComponentBytes(stemMaxBytes, fmt.Sprintf("%s.S%02d", DotsComponent(show), season))
}

// MovieNameStem returns "<Title>.<Year>" for the final directory name.
func MovieNameStem(title, year string) string {
	stem := DotsComponent(title)
	if year != "" {
		stem += "." + year
	}
	return CapComponentBytes(stemMaxBytes, stem)
}

// TatortNameStem returns "Tatort.<City>[.Folge.N][.Year]" for the final name.
func TatortNameStem(city, episodeNo, year string) string {
	base := "Tatort." + DotsComponent(city)
	if episodeNo != "" {
		base += ".Folge." + episodeNo
	}
	if year != "" {
		base += "." + year
	}
	return CapComponentBytes(stemMaxBytes, base)
}

// ResolutionLabel maps a stream height to the label used in file names.
func ResolutionLabel(height int) string {
	switch height {
	case 2160:
		return "2160p"
	case 1440:
		return "1440p"
	case 1080:
		return "1080p"
	case 720:
		return "720p"
	case 576:
		return "576p"
	case 480:
		return "480p"
	case 360:
		return "360p"
	}
	switch {
	case height >= 2160:
		return "2160p"
	case height >= 1440:
		return "1440p"
	case height >= 1080:
		return "1080p"
	case height >= 720:
		return "720p"
	case height >= 576:
		return "576p"
	case height >= 480:
		return "480p"
	case height > 0:
		return fmt.Sprintf("%dp", height)
	}
	return "Unknown"
}

// VideoCodecLabel maps an ffprobe video codec name to the file name label.
func VideoCodecLabel(codec string) string {
	switch codec {
	case "h264":
		return "x264"
	case "hevc":
		return "x265"
	case "av1":
		return "AV1"
	case "":
		return "Unknown"
	}
	return strings.ToUpper(codec)
}

// AudioCodecLabel maps an ffprobe audio codec name to the file name label.
func AudioCodecLabel(codec string, channels int, profile string) string {
	var label string
	switch codec {
	case "aac":
		label = "AAC"
	case "ac3":
		label = "AC3"
	case "eac3":
		label = "EAC3"
	case "dts":
		switch {
		case strings.Contains(profile, "MA"):
			label = "DTS-HD.MA"
		case strings.Contains(profile, "HRA"):
			label = "DTS-HD.HRA"
		default:
			label = "DTS"
		}
	case "truehd":
		label = "TrueHD"
	case "opus":
		label = "Opus"
	case "mp3":
		label = "MP3"
	case "flac":
		label = "FLAC"
	case "":
		label = "Unknown"
	default:
		label = strings.ToUpper(codec)
	}
	if channels > 2 && (label == "AC3" || label == "EAC3") {
		label = fmt.Sprintf("%s.%dch", label, channels)
	}
	return label
}

// ApplyFileTag appends the mandatory Datei-Tag to the media tag.
func ApplyFileTag(mediaTag, fileTag string) string {
	return mediaTag + "-" + fileTag
}
