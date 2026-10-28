package tui

// The layout of a screen has three parts: a header with the title and the
// context, a body that fills the free height and a footer with the key
// hints. The chrome always spans the full width of the terminal, the text
// inside the cards keeps a readable width.

// defaultFrameWidth is the width used before the terminal reports its size.
const defaultFrameWidth = 80

// frameWidth is the full width of the terminal.
// One column stays free on both sides, so the frames do not touch the edge
// of the terminal; narrow terminals need every column.
func (m *Model) frameWidth() int {
	if m.width >= marginMinWidth {
		return m.width - 2*pageMargin
	}
	if m.width > 0 {
		return m.width
	}
	return defaultFrameWidth
}

const (
	// pageMargin is the free column on the left and the right of the screen.
	pageMargin = 1
	// marginMinWidth is the terminal width from which the margin shows.
	marginMinWidth = 60
)

// contentWidth is the readable width for text that stands on its own, for
// example the input fields.
func (m *Model) contentWidth() int {
	return min(m.frameWidth(), maxContentWidth)
}

// cardTextWidth is the width of the text inside a card of the given width.
func (m *Model) cardTextWidth(width int) int {
	return cardInner(width)
}

// flowWidth is the width for running text. Long messages stay readable
// instead of stretching over the whole terminal.
func (m *Model) flowWidth(width int) int {
	return min(max(1, width-2), maxContentWidth)
}

// splitAt returns the width of two columns for a screen of the given width.
// The second return value is zero when the screen is too narrow for two
// columns.
func (m *Model) splitAt(width int, share float64) (int, int) {
	if width < minSplitWidth {
		return width, 0
	}
	return splitWidths(width, share)
}
