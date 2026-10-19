// Package dash implements a native MPEG-DASH downloader: manifest parsing,
// track selection, segment download with resume and muxing through ffmpeg.
package dash

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrDRM marks a manifest that declares content protection.
var ErrDRM = errors.New("DRM-geschützter Stream wird nicht unterstützt")

// Kind is the media type of a track.
type Kind int

// Track kinds.
const (
	KindVideo Kind = iota
	KindAudio
	KindSubtitle
)

func (k Kind) String() string {
	switch k {
	case KindVideo:
		return "video"
	case KindAudio:
		return "audio"
	default:
		return "subtitle"
	}
}

// ByteRange is an inclusive byte range inside a URL.
type ByteRange struct {
	Start int64
	End   int64
}

// Segment is one downloadable unit.
type Segment struct {
	URL   string
	Range *ByteRange
}

// Track is one representation of the manifest.
type Track struct {
	ID        string
	Kind      Kind
	Lang      string
	Role      string
	MimeType  string
	Codecs    string
	Width     int
	Height    int
	Bandwidth int
	Channels  int
	URL       string

	Init     *Segment
	Segments []Segment

	// pending, when set, means the segment list is known only after the sidx
	// box has been fetched.
	pending *pendingSegments

	// baseQuery is the raw query of the manifest URL. Segment URLs without a
	// query of their own get it appended: the ARD playback token lives there.
	baseQuery string
}

// pendingSegments describes how to resolve a segment list from a sidx box.
type pendingSegments struct {
	// initURL holds the sidx; in byte-range mode it also holds the segments.
	initURL   string
	initRange *ByteRange
	sidxRange *ByteRange
	// mediaTemplate is empty in byte-range mode.
	mediaTemplate    string
	startNumber      int64
	representationID string
	bandwidth        int
	timescale        int64
	byteRangeMode    bool
	base             *url.URL
}

type mpdXML struct {
	XMLName                   xml.Name    `xml:"MPD"`
	Type                      string      `xml:"type,attr"`
	MediaPresentationDuration string      `xml:"mediaPresentationDuration,attr"`
	ContentProtection         []struct{}  `xml:"ContentProtection"`
	BaseURLs                  []string    `xml:"BaseURL"`
	Periods                   []periodXML `xml:"Period"`
}

type periodXML struct {
	Duration          string             `xml:"duration,attr"`
	ContentProtection []struct{}         `xml:"ContentProtection"`
	BaseURLs          []string           `xml:"BaseURL"`
	AdaptationSets    []adaptationSetXML `xml:"AdaptationSet"`
}

type adaptationSetXML struct {
	ID                string              `xml:"id,attr"`
	Lang              string              `xml:"lang,attr"`
	ContentType       string              `xml:"contentType,attr"`
	MimeType          string              `xml:"mimeType,attr"`
	Codecs            string              `xml:"codecs,attr"`
	Width             int                 `xml:"width,attr"`
	Height            int                 `xml:"height,attr"`
	Roles             []roleXML           `xml:"Role"`
	ContentProtection []struct{}          `xml:"ContentProtection"`
	BaseURLs          []string            `xml:"BaseURL"`
	SegmentTemplate   *segmentTemplateXML `xml:"SegmentTemplate"`
	SegmentList       *segmentListXML     `xml:"SegmentList"`
	SegmentBase       *segmentBaseXML     `xml:"SegmentBase"`
	Representations   []representationXML `xml:"Representation"`
}

type roleXML struct {
	Value string `xml:"value,attr"`
}

type representationXML struct {
	ID                        string              `xml:"id,attr"`
	Bandwidth                 int                 `xml:"bandwidth,attr"`
	Width                     int                 `xml:"width,attr"`
	Height                    int                 `xml:"height,attr"`
	Codecs                    string              `xml:"codecs,attr"`
	MimeType                  string              `xml:"mimeType,attr"`
	AudioChannelConfiguration []channelsXML       `xml:"AudioChannelConfiguration"`
	BaseURLs                  []string            `xml:"BaseURL"`
	ContentProtection         []struct{}          `xml:"ContentProtection"`
	SegmentTemplate           *segmentTemplateXML `xml:"SegmentTemplate"`
	SegmentList               *segmentListXML     `xml:"SegmentList"`
	SegmentBase               *segmentBaseXML     `xml:"SegmentBase"`
}

type channelsXML struct {
	Value string `xml:"value,attr"`
}

type segmentTemplateXML struct {
	Media                  string `xml:"media,attr"`
	Initialization         string `xml:"initialization,attr"`
	StartNumber            *int64 `xml:"startNumber,attr"`
	Duration               *int64 `xml:"duration,attr"`
	Timescale              *int64 `xml:"timescale,attr"`
	PresentationTimeOffset *int64 `xml:"presentationTimeOffset,attr"`
}

type segmentListXML struct {
	Duration       *int64          `xml:"duration,attr"`
	Timescale      *int64          `xml:"timescale,attr"`
	Initialization *initXML        `xml:"Initialization"`
	SegmentURLs    []segmentURLXML `xml:"SegmentURL"`
}

type initXML struct {
	SourceURL string `xml:"sourceURL,attr"`
	Range     string `xml:"range,attr"`
}

type segmentURLXML struct {
	Media string `xml:"media,attr"`
	Range string `xml:"range,attr"`
}

type segmentBaseXML struct {
	IndexRange     string   `xml:"indexRange,attr"`
	Initialization *initXML `xml:"Initialization"`
}

// ParseManifest parses an MPD document. Segment URLs are resolved against the
// manifest URL; templates with a known duration are expanded immediately,
// index-based addressing stays pending until the downloader fetches the sidx.
func ParseManifest(r io.Reader, manifestURL *url.URL) ([]Track, error) {
	if manifestURL == nil {
		return nil, errors.New("no manifest URL")
	}
	var mpd mpdXML
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&mpd); err != nil {
		return nil, fmt.Errorf("parse MPD: %w", err)
	}
	if len(mpd.ContentProtection) > 0 {
		return nil, ErrDRM
	}

	baseQuery := manifestURL.RawQuery
	mpdDuration := parseISODuration(mpd.MediaPresentationDuration)
	mpdBase := resolveBase(manifestURL, mpd.BaseURLs)

	var tracks []Track
	for periodIdx, period := range mpd.Periods {
		if len(period.ContentProtection) > 0 {
			return nil, ErrDRM
		}
		duration := parseISODuration(period.Duration)
		if duration == 0 {
			duration = mpdDuration
		}
		periodBase := resolveBase(mpdBase, period.BaseURLs)

		for setIdx, set := range period.AdaptationSets {
			if len(set.ContentProtection) > 0 {
				return nil, ErrDRM
			}
			for repIdx, rep := range set.Representations {
				if len(rep.ContentProtection) > 0 {
					return nil, ErrDRM
				}
				base := resolveBase(periodBase, set.BaseURLs)
				base = resolveBase(base, rep.BaseURLs)

				track := Track{
					ID:        representationID(set, rep, periodIdx, setIdx, repIdx),
					Lang:      set.Lang,
					Role:      firstRole(set.Roles),
					MimeType:  firstNonEmpty(rep.MimeType, set.MimeType),
					Codecs:    firstNonEmpty(rep.Codecs, set.Codecs),
					Width:     firstNonZero(rep.Width, set.Width),
					Height:    firstNonZero(rep.Height, set.Height),
					Bandwidth: rep.Bandwidth,
					Channels:  parseChannels(rep.AudioChannelConfiguration),
					URL:       base.String(),
					baseQuery: baseQuery,
				}
				track.Kind = trackKind(set.ContentType, track.MimeType, track.Codecs)

				baseDeclared := hasBaseURL(mpd.BaseURLs) || hasBaseURL(period.BaseURLs) ||
					hasBaseURL(set.BaseURLs) || hasBaseURL(rep.BaseURLs)
				if err := applyAddressing(&track, set, rep, base, duration, baseDeclared); err != nil {
					return nil, err
				}
				tracks = append(tracks, track)
			}
		}
	}
	if len(tracks) == 0 {
		return nil, errors.New("no representation in DASH manifest")
	}
	return tracks, nil
}

func representationID(set adaptationSetXML, rep representationXML, periodIdx, setIdx, repIdx int) string {
	if rep.ID != "" {
		return rep.ID
	}
	if set.ID != "" {
		return fmt.Sprintf("%s-%d", set.ID, repIdx)
	}
	return fmt.Sprintf("p%d-as%d-r%d", periodIdx, setIdx, repIdx)
}

func firstRole(roles []roleXML) string {
	for _, role := range roles {
		if role.Value != "" {
			return role.Value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func parseChannels(configs []channelsXML) int {
	for _, c := range configs {
		if c.Value == "" {
			continue
		}
		if n, err := strconv.Atoi(c.Value); err == nil {
			return n
		}
	}
	return 0
}

// trackKind derives the media kind from contentType, mimeType and codecs.
func trackKind(contentType, mimeType, codecs string) Kind {
	combined := strings.ToLower(contentType + " " + mimeType + " " + codecs)
	switch {
	case strings.Contains(combined, "video"):
		return KindVideo
	case strings.Contains(combined, "audio"), strings.Contains(combined, "mp4a"), strings.Contains(combined, "ac-3"), strings.Contains(combined, "ec-3"):
		return KindAudio
	case strings.Contains(combined, "text"), strings.Contains(combined, "ttml"),
		strings.Contains(combined, "wvtt"), strings.Contains(combined, "stpp"):
		return KindSubtitle
	}
	return KindAudio
}

// hasBaseURL reports whether refs contains a usable BaseURL.
func hasBaseURL(refs []string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			return true
		}
	}
	return false
}

// resolveBase resolves BaseURL references against the parent URL; the closest
// element to the representation wins.
func resolveBase(base *url.URL, refs []string) *url.URL {
	for i := len(refs) - 1; i >= 0; i-- {
		ref := strings.TrimSpace(refs[i])
		if ref == "" {
			continue
		}
		parsed, err := url.Parse(ref)
		if err != nil {
			continue
		}
		return base.ResolveReference(parsed)
	}
	return base
}

func parseISODuration(value string) time.Duration {
	if value == "" {
		return 0
	}
	s := strings.TrimPrefix(value, "P")
	if s == value {
		return 0
	}
	var total time.Duration
	datePart, timePart, hasTime := strings.Cut(s, "T")
	parseUnits := func(part string, units map[byte]time.Duration) {
		number := strings.Builder{}
		for i := range len(part) {
			c := part[i]
			if (c >= '0' && c <= '9') || c == '.' {
				number.WriteByte(c)
				continue
			}
			if unit, ok := units[c]; ok && number.Len() > 0 {
				if v, err := strconv.ParseFloat(number.String(), 64); err == nil {
					total += time.Duration(v * float64(unit))
				}
			}
			number.Reset()
		}
	}
	parseUnits(datePart, map[byte]time.Duration{'D': 24 * time.Hour})
	if hasTime {
		parseUnits(timePart, map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second})
	}
	return total
}

var templateVarPattern = regexp.MustCompile(`\$(RepresentationID|Number|Bandwidth|Time)(?:%0(\d+)d)?\$`)

// expandTemplate substitutes the SegmentTemplate placeholders.
func expandTemplate(tmpl, representationID string, bandwidth int, number, timeValue int64) string {
	return templateVarPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		groups := templateVarPattern.FindStringSubmatch(match)
		name, width := groups[1], groups[2]
		var value int64
		switch name {
		case "RepresentationID":
			return representationID
		case "Bandwidth":
			value = int64(bandwidth)
		case "Number":
			value = number
		case "Time":
			value = timeValue
		}
		if width != "" {
			if w, err := strconv.Atoi(width); err == nil {
				return fmt.Sprintf("%0*d", w, value)
			}
		}
		return strconv.FormatInt(value, 10)
	})
}

// applyAddressing fills the track's segment list from the addressing elements.
// mergeSegmentTemplate overlays the representation's SegmentTemplate on the
// AdaptationSet's: DASH allows splitting the attributes between both levels,
// which is exactly what ARD does (timescale on the set, media on the
// representation).
func mergeSegmentTemplate(set, rep *segmentTemplateXML) *segmentTemplateXML {
	if rep == nil {
		return set
	}
	if set == nil {
		return rep
	}
	merged := *rep
	if merged.Media == "" {
		merged.Media = set.Media
	}
	if merged.Initialization == "" {
		merged.Initialization = set.Initialization
	}
	if merged.StartNumber == nil {
		merged.StartNumber = set.StartNumber
	}
	if merged.Duration == nil {
		merged.Duration = set.Duration
	}
	if merged.Timescale == nil {
		merged.Timescale = set.Timescale
	}
	if merged.PresentationTimeOffset == nil {
		merged.PresentationTimeOffset = set.PresentationTimeOffset
	}
	return &merged
}

func applyAddressing(track *Track, set adaptationSetXML, rep representationXML, base *url.URL, periodDuration time.Duration, baseDeclared bool) error {
	tmpl := mergeSegmentTemplate(set.SegmentTemplate, rep.SegmentTemplate)
	if tmpl != nil && tmpl.Media != "" {
		timescale := int64(1)
		if tmpl.Timescale != nil && *tmpl.Timescale > 0 {
			timescale = *tmpl.Timescale
		}
		startNumber := int64(1)
		if tmpl.StartNumber != nil {
			startNumber = *tmpl.StartNumber
		}
		var pto int64
		if tmpl.PresentationTimeOffset != nil {
			pto = *tmpl.PresentationTimeOffset
		}
		if tmpl.Initialization != "" {
			media := expandTemplate(tmpl.Initialization, track.ID, track.Bandwidth, startNumber, pto)
			if u := resolveSegmentURL(base, media); u != "" {
				track.Init = &Segment{URL: u}
			}
		}
		if tmpl.Duration != nil && *tmpl.Duration > 0 && periodDuration > 0 {
			segmentDuration := *tmpl.Duration
			ticks := int64(periodDuration.Seconds() * float64(timescale))
			count := (ticks - pto + segmentDuration - 1) / segmentDuration
			if count < 1 {
				count = 1
			}
			segments := make([]Segment, 0, count)
			for i := range count {
				media := expandTemplate(tmpl.Media, track.ID, track.Bandwidth, startNumber+i, pto+i*segmentDuration)
				segments = append(segments, Segment{URL: resolveSegmentURL(base, media)})
			}
			track.Segments = segments
			return nil
		}
		// Duration unknown: resolve the list through the sidx box.
		initURL := track.URL
		if track.Init != nil {
			initURL = track.Init.URL
		}
		track.pending = &pendingSegments{
			initURL:          initURL,
			mediaTemplate:    tmpl.Media,
			startNumber:      startNumber,
			representationID: track.ID,
			bandwidth:        track.Bandwidth,
			timescale:        timescale,
			base:             base,
		}
		return nil
	}

	// SegmentList and SegmentBase, like SegmentTemplate, may be declared on the
	// AdaptationSet and are then inherited by every representation.
	list := rep.SegmentList
	if list == nil {
		list = set.SegmentList
	}
	if list != nil {
		if list.Initialization != nil {
			u := resolveSegmentURL(base, list.Initialization.SourceURL)
			if u != "" {
				track.Init = &Segment{URL: u, Range: parseByteRange(list.Initialization.Range)}
			}
		}
		for _, entry := range list.SegmentURLs {
			u := resolveSegmentURL(base, entry.Media)
			if u == "" {
				continue
			}
			track.Segments = append(track.Segments, Segment{URL: u, Range: parseByteRange(entry.Range)})
		}
		return nil
	}

	sb := rep.SegmentBase
	if sb == nil {
		sb = set.SegmentBase
	}
	if sb != nil {
		if sb.IndexRange == "" {
			return fmt.Errorf("unsupported DASH addressing for representation %s: SegmentBase without indexRange", track.ID)
		}
		pending := &pendingSegments{
			initURL:       track.URL,
			byteRangeMode: true,
			timescale:     1,
			sidxRange:     parseByteRange(sb.IndexRange),
		}
		if sb.Initialization != nil {
			pending.initRange = parseByteRange(sb.Initialization.Range)
		}
		track.pending = pending
		return nil
	}

	// A representation with a BaseURL but no segment index is a single-file
	// media resource: the BaseURL itself is the complete media file.
	if baseDeclared && track.URL != "" && len(track.Segments) == 0 && track.pending == nil {
		track.Segments = []Segment{{URL: track.URL}}
		return nil
	}

	return fmt.Errorf("unsupported DASH addressing for representation %s", track.ID)
}

// parseByteRange parses "start-end" as used by DASH. DASH ranges are
// end-exclusive; the stored range is inclusive for HTTP Range requests.
func parseByteRange(value string) *ByteRange {
	if value == "" {
		return nil
	}
	start, end, ok := strings.Cut(value, "-")
	if !ok {
		return nil
	}
	startN, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(end) == "" {
		return &ByteRange{Start: startN, End: -1}
	}
	endN, err := strconv.ParseInt(strings.TrimSpace(end), 10, 64)
	if err != nil {
		return nil
	}
	return &ByteRange{Start: startN, End: endN - 1}
}

func resolveSegmentURL(base *url.URL, ref string) string {
	if ref == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	return base.ResolveReference(parsed).String()
}

// sidx parsing (ISO/IEC 14496-12, section 8.16.3).

type sidxReference struct {
	ReferencedSize     int64
	SubsegmentDuration int64
}

type sidxInfo struct {
	Timescale                int64
	EarliestPresentationTime int64
	FirstOffset              int64
	References               []sidxReference
}

// ParseSidx extracts the segment index from the first sidx box in data.
func ParseSidx(data []byte) (*sidxInfo, error) {
	box := findBox(data, "sidx")
	if box == nil {
		return nil, errors.New("no sidx box found")
	}
	return parseSidxBox(box)
}

func parseSidxBox(box []byte) (*sidxInfo, error) {
	if len(box) < 20 {
		return nil, errors.New("truncated sidx box")
	}
	version := box[0]
	offset := 4 // version + flags
	offset += 4 // reference_ID
	info := &sidxInfo{Timescale: int64(binary.BigEndian.Uint32(box[offset : offset+4]))}
	offset += 4
	if version == 0 {
		if offset+8 > len(box) {
			return nil, errors.New("truncated sidx box")
		}
		info.EarliestPresentationTime = int64(binary.BigEndian.Uint32(box[offset : offset+4]))
		offset += 4
		info.FirstOffset = int64(binary.BigEndian.Uint32(box[offset : offset+4]))
		offset += 4
	} else {
		if offset+16 > len(box) {
			return nil, errors.New("truncated sidx box")
		}
		info.EarliestPresentationTime = int64(binary.BigEndian.Uint64(box[offset : offset+8]))
		offset += 8
		info.FirstOffset = int64(binary.BigEndian.Uint64(box[offset : offset+8]))
		offset += 8
	}
	offset += 2 // reserved
	if offset+2 > len(box) {
		return nil, errors.New("truncated sidx box")
	}
	count := int(binary.BigEndian.Uint16(box[offset : offset+2]))
	offset += 2
	for range count {
		if offset+12 > len(box) {
			break
		}
		size := int64(binary.BigEndian.Uint32(box[offset+4 : offset+8]))
		duration := int64(binary.BigEndian.Uint32(box[offset+8 : offset+12]))
		info.References = append(info.References, sidxReference{ReferencedSize: size, SubsegmentDuration: duration})
		offset += 12
	}
	if len(info.References) == 0 {
		return nil, errors.New("sidx box without references")
	}
	return info, nil
}

// findBox walks the top-level ISO-BMFF boxes and returns the payload of the
// first box with the wanted type.
func findBox(data []byte, wanted string) []byte {
	offset := 0
	for offset+8 <= len(data) {
		size := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		headerSize := int64(8)
		switch {
		case size == 1:
			if offset+16 > len(data) {
				return nil
			}
			size = int64(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
			headerSize = 16
		case size == 0:
			size = int64(len(data) - offset)
		}
		if size < headerSize || offset+int(size) > len(data) {
			return nil
		}
		if boxType == wanted {
			return data[offset+int(headerSize) : offset+int(size)]
		}
		offset += int(size)
	}
	return nil
}
