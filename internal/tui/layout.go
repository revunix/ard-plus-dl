package tui

// The layout of a screen has three parts: a header with the title and the
// context, a body that fills the free height and a footer with the key
// hints. The chrome always spans the full width of the terminal, the text
// inside the cards keeps a readable width.

// defaultFrameWidth is the width used before the terminal reports its size.
const defaultFrameWidth = 80

// frameWidth is the full width of the terminal.
func (m *Model) frameWidth() int {
	if m.width > 0 {
		return m.width
	}
	return defaultFrameWidth
}

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
	return min(cardInner(width), maxContentWidth)
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
