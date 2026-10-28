package tui

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// The splash screen shows the logo of ARD Plus: the white "ARD" with the
// "1" in its circle and the blue box with "PLUS" cut out of it. The image
// is the SVG of the website, rendered once to a PNG. Every cell of the
// terminal holds two pixels, the upper and the lower half of a block glyph,
// so the pixels are square.

//go:embed ardplus-logo.png
var logoPNG []byte

// The colors of the logo. The blue of the box is the one of the website;
// the white of the lettering turns into the ARD blue on light pages, so it
// stays visible.
var (
	logoInk  = lipgloss.AdaptiveColor{Light: "#001E50", Dark: "#FFFFFF"}
	logoBlue = lipgloss.AdaptiveColor{Light: "#589FFF", Dark: "#589FFF"}
)

const (
	// logoMinCoverage is the share of its color a pixel has to take after
	// the sharpening to be drawn at all; below it the page shows through.
	logoMinCoverage = 0.2
	// logoMinWidth is the smallest width in cells that still shows the
	// "PLUS" in the box.
	logoMinWidth = 40
	// logoMaxWidth is the widest the logo gets.
	logoMaxWidth = 96
)

var (
	logoOnce  sync.Once
	logoImage image.Image
)

// logoSource returns the decoded logo; nil when the image cannot be read.
func logoSource() image.Image {
	logoOnce.Do(func() {
		decoded, err := png.Decode(bytes.NewReader(logoPNG))
		if err == nil {
			logoImage = decoded
		}
	})
	return logoImage
}

// logoRows returns the number of lines the logo takes at a width.
func logoRows(width int) int {
	source := logoSource()
	if source == nil || width <= 0 {
		return 0
	}
	bounds := source.Bounds()
	pixels := (width*bounds.Dy() + bounds.Dx()/2) / bounds.Dx()
	return (pixels + 1) / 2
}

// logoWidthFor returns the width of the logo that fits into the room. Zero
// means the room is too small for a readable logo.
func logoWidthFor(width, rows int) int {
	source := logoSource()
	if source == nil {
		return 0
	}
	bounds := source.Bounds()
	fit := min(width, logoMaxWidth)
	// The rows hold two pixels each.
	if byHeight := rows * 2 * bounds.Dx() / bounds.Dy(); byHeight < fit {
		fit = byHeight
	}
	if fit < logoMinWidth {
		return 0
	}
	return fit
}

// logoPixel is one square pixel of the scaled logo.
type logoPixel struct {
	// coverage is the share of the pixel the logo covers.
	coverage float64
	// blue tells the box from the lettering.
	blue bool
	// x and y are the place of the pixel in the grid.
	x, y int
}

// logoLines renders the logo at a width in cells.
func logoLines(width int) []string {
	source := logoSource()
	rows := logoRows(width)
	if source == nil || rows == 0 {
		return nil
	}
	pixels := scaleLogo(source, width, rows*2)
	box := blueBox(pixels)
	page := themeHex(colorBackground)
	ink, blue := themeHex(logoInk), themeHex(logoBlue)

	// The edges blend into the page, so the shapes stay smooth.
	color := func(pixel logoPixel) string {
		if pixel.blue {
			// Outside the box no blue pixel is drawn: a half covered pixel
			// along its straight edges would draw a dark seam. Inside, the
			// letters cut out of it keep their soft edges.
			if !box.holds(pixel) {
				return ""
			}
			return blendHex(page, blue, sharpen(pixel.coverage))
		}
		share := sharpen(pixel.coverage)
		if share < logoMinCoverage {
			return ""
		}
		return blendHex(page, ink, share)
	}

	lines := make([]string, rows)
	for row := range rows {
		var builder strings.Builder
		for column := range width {
			top := color(pixels[row*2][column])
			bottom := color(pixels[row*2+1][column])
			switch {
			case top == "" && bottom == "":
				builder.WriteByte(' ')
			case bottom == "":
				builder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(top)).Render("▀"))
			case top == "":
				builder.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(bottom)).Render("▄"))
			default:
				builder.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(top)).
					Background(lipgloss.Color(bottom)).
					Render("▀"))
			}
		}
		lines[row] = builder.String()
	}
	return lines
}

// logoBox is the area of the blue box in the pixel grid.
type logoBox struct {
	top, left, bottom, right int
}

// holds reports whether a pixel lies inside the box.
func (b logoBox) holds(pixel logoPixel) bool {
	return pixel.y >= b.top && pixel.y <= b.bottom && pixel.x >= b.left && pixel.x <= b.right
}

// blueBox finds the box: the rows and columns the blue covers to more than
// half. The edges that it covers less belong to the page.
func blueBox(pixels [][]logoPixel) logoBox {
	box := logoBox{top: len(pixels), left: 1 << 30, bottom: -1, right: -1}
	for y, row := range pixels {
		for x, pixel := range row {
			if !pixel.blue || pixel.coverage < 0.5 {
				continue
			}
			box.top, box.bottom = min(box.top, y), max(box.bottom, y)
			box.left, box.right = min(box.left, x), max(box.right, x)
		}
	}
	return box
}

// scaleLogo shrinks the image to the pixel grid. Every target pixel
// averages the source pixels it covers, so thin strokes keep their weight.
func scaleLogo(source image.Image, width, height int) [][]logoPixel {
	bounds := source.Bounds()
	grid := make([][]logoPixel, height)
	for y := range height {
		grid[y] = make([]logoPixel, width)
		top := bounds.Min.Y + y*bounds.Dy()/height
		bottom := max(top+1, bounds.Min.Y+(y+1)*bounds.Dy()/height)
		for x := range width {
			left := bounds.Min.X + x*bounds.Dx()/width
			right := max(left+1, bounds.Min.X+(x+1)*bounds.Dx()/width)
			var alpha, blueAlpha float64
			for sy := top; sy < bottom; sy++ {
				for sx := left; sx < right; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					if a == 0 {
						continue
					}
					weight := float64(a) / 0xffff
					alpha += weight
					// The box is clearly bluer than it is red; the white
					// lettering is not.
					if b > r+0x3000 && b > g {
						blueAlpha += weight
					}
				}
			}
			area := float64((bottom - top) * (right - left))
			grid[y][x] = logoPixel{coverage: alpha / area, blue: blueAlpha*2 > alpha, x: x, y: y}
		}
	}
	return grid
}

// sharpen steepens the edge of the shapes: pixels the logo barely touches
// fall back to the page, pixels it mostly covers take its full color. A
// soft edge would draw a dark seam along the box.
func sharpen(coverage float64) float64 {
	share := min(max((coverage-0.25)/0.5, 0), 1)
	return share * share * (3 - 2*share)
}

// blendHex mixes two hex colors; share is the part of the second one.
func blendHex(from, to string, share float64) string {
	share = min(max(share, 0), 1)
	if share > 0.97 {
		// Nearly full pixels take the exact color.
		return to
	}
	fromR, fromG, fromB := parseHexColor(from)
	toR, toG, toB := parseHexColor(to)
	mix := func(a, b int) int {
		return a + int(float64(b-a)*share+0.5)
	}
	return formatHexColor(mix(fromR, toR), mix(fromG, toG), mix(fromB, toB))
}
