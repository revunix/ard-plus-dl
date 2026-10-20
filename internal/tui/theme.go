package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ThemeMode selects the color scheme of the interface.
type ThemeMode int

// The available color schemes.
const (
	// ThemeAuto follows the background of the terminal.
	ThemeAuto ThemeMode = iota
	// ThemeLight always uses the light palette.
	ThemeLight
	// ThemeDark always uses the dark palette.
	ThemeDark
)

// themeMode is the color scheme in use.
var themeMode = ThemeAuto

// themeLabel names the color scheme for the user interface.
func themeLabel(mode ThemeMode) string {
	switch mode {
	case ThemeLight:
		return "Hell"
	case ThemeDark:
		return "Dunkel"
	default:
		return "Automatisch"
	}
}

// parseTheme reads a stored color scheme.
func parseTheme(value string) ThemeMode {
	switch value {
	case "light":
		return ThemeLight
	case "dark":
		return ThemeDark
	default:
		return ThemeAuto
	}
}

// themeValue writes the color scheme for the store.
func themeValue(mode ThemeMode) string {
	switch mode {
	case ThemeLight:
		return "light"
	case ThemeDark:
		return "dark"
	default:
		return "auto"
	}
}

// nextTheme cycles through the color schemes.
func nextTheme(mode ThemeMode) ThemeMode {
	switch mode {
	case ThemeAuto:
		return ThemeLight
	case ThemeLight:
		return ThemeDark
	default:
		return ThemeAuto
	}
}

// previousTheme cycles backwards through the color schemes.
func previousTheme(mode ThemeMode) ThemeMode {
	switch mode {
	case ThemeAuto:
		return ThemeDark
	case ThemeDark:
		return ThemeLight
	default:
		return ThemeAuto
	}
}

// isDark reports whether the dark palette is in use.
func isDark() bool {
	switch themeMode {
	case ThemeLight:
		return false
	case ThemeDark:
		return true
	default:
		return lipgloss.HasDarkBackground()
	}
}

// colorOf returns the color of the active scheme. In automatic mode the
// terminal decides, otherwise the chosen palette is used.
func colorOf(color lipgloss.AdaptiveColor) lipgloss.TerminalColor {
	switch themeMode {
	case ThemeLight:
		return lipgloss.Color(color.Light)
	case ThemeDark:
		return lipgloss.Color(color.Dark)
	default:
		return color
	}
}

// themeHex returns the hex value of the active scheme.
func themeHex(color lipgloss.AdaptiveColor) string {
	if isDark() {
		return color.Dark
	}
	return color.Light
}

// applyTheme switches the color scheme and rebuilds the styles.
func applyTheme(mode ThemeMode) {
	themeMode = mode
	theme = newTheme()
	titleStyle = theme.Title
	selectedStyle = theme.Selected
}

// Theme colors follow the ARD Plus palette: deep ARD blue, bright blue and
// cyan accents on dark terminals, with darker blue counterparts on light ones.
var (
	colorAccent   = lipgloss.AdaptiveColor{Light: "#003480", Dark: "#6BA8FF"}
	colorText     = lipgloss.AdaptiveColor{Light: "#001E50", Dark: "#F4F8FF"}
	colorMuted    = lipgloss.AdaptiveColor{Light: "#41618F", Dark: "#A9C7F0"}
	colorBorder   = lipgloss.AdaptiveColor{Light: "#6E9BD0", Dark: "#2E62AC"}
	colorSuccess  = lipgloss.AdaptiveColor{Light: "#00796B", Dark: "#4FE6D6"}
	colorWarning  = lipgloss.AdaptiveColor{Light: "#7A6A00", Dark: "#D3E85E"}
	colorDanger   = lipgloss.AdaptiveColor{Light: "#B3122A", Dark: "#FF5C6B"}
	colorInfo     = lipgloss.AdaptiveColor{Light: "#005EC8", Dark: "#6BA8FF"}
	colorAccentBg = lipgloss.AdaptiveColor{Light: "#D6E8FF", Dark: "#11477F"}
	colorNeutral  = lipgloss.AdaptiveColor{Light: "#E4EEFC", Dark: "#1F3D67"}
	colorOpenBg   = lipgloss.AdaptiveColor{Light: "#E1ECFD", Dark: "#0A3F82"}
	colorDoneBg   = lipgloss.AdaptiveColor{Light: "#DDF6F2", Dark: "#0A4A44"}
	colorErrorBg  = lipgloss.AdaptiveColor{Light: "#FFE1E5", Dark: "#5A1620"}
	// colorPopup is the surface of the help window.
	colorPopup = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#16233A"}
	// colorBackground paints the surface of the interface.
	colorBackground = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0F141B"}

	// ARD Plus brand gradient: deep blue to cyan.
	colorGradientStartLight = "#003480"
	colorGradientEndLight   = "#009688"
	colorGradientStartDark  = "#589FFF"
	colorGradientEndDark    = "#45DECF"
)

// appTheme bundles the styles shared by all views.
type appTheme struct {
	Title            lipgloss.Style
	Subtitle         lipgloss.Style
	Muted            lipgloss.Style
	Text             lipgloss.Style
	Accent           lipgloss.Style
	Label            lipgloss.Style
	Success          lipgloss.Style
	Warning          lipgloss.Style
	Danger           lipgloss.Style
	Info             lipgloss.Style
	Selected         lipgloss.Style
	Cursor           lipgloss.Style
	Badge            lipgloss.Style
	Keycap           lipgloss.Style
	PillNeutral      lipgloss.Style
	PillOpen         lipgloss.Style
	PillDone         lipgloss.Style
	PillError        lipgloss.Style
	CardTitle        lipgloss.Style
	InputPrompt      lipgloss.Style
	InputText        lipgloss.Style
	InputPlaceholder lipgloss.Style
	InputCursor      lipgloss.Style
	KeyLabel         lipgloss.Style
	Divider          lipgloss.Style
	Background       lipgloss.Style
	Popup            lipgloss.Style
}

// theme is the active design system.
var theme = newTheme()

func newTheme() appTheme {
	return appTheme{
		Title:            lipgloss.NewStyle().Bold(true).Foreground(colorOf(colorText)),
		Subtitle:         lipgloss.NewStyle().Foreground(colorOf(colorMuted)),
		Muted:            lipgloss.NewStyle().Foreground(colorOf(colorMuted)),
		Text:             lipgloss.NewStyle().Foreground(colorOf(colorText)),
		Accent:           lipgloss.NewStyle().Foreground(colorOf(colorAccent)),
		Label:            lipgloss.NewStyle().Foreground(colorOf(colorMuted)),
		Success:          lipgloss.NewStyle().Foreground(colorOf(colorSuccess)).Bold(true),
		Warning:          lipgloss.NewStyle().Foreground(colorOf(colorWarning)).Bold(true),
		Danger:           lipgloss.NewStyle().Foreground(colorOf(colorDanger)).Bold(true),
		Info:             lipgloss.NewStyle().Foreground(colorOf(colorInfo)),
		Selected:         lipgloss.NewStyle().Foreground(colorOf(colorAccent)).Bold(true),
		Cursor:           lipgloss.NewStyle().Foreground(colorOf(colorText)).Background(colorOf(colorAccentBg)).Bold(true),
		Badge:            lipgloss.NewStyle().Foreground(colorOf(colorAccent)).Background(colorOf(colorNeutral)).Padding(0, 1),
		Keycap:           lipgloss.NewStyle().Foreground(colorOf(colorAccent)).Background(colorOf(colorNeutral)).Bold(true).Padding(0, 1),
		PillNeutral:      lipgloss.NewStyle().Foreground(colorOf(colorMuted)).Background(colorOf(colorNeutral)).Padding(0, 1),
		PillOpen:         lipgloss.NewStyle().Foreground(colorOf(colorInfo)).Background(colorOf(colorOpenBg)).Padding(0, 1),
		PillDone:         lipgloss.NewStyle().Foreground(colorOf(colorSuccess)).Background(colorOf(colorDoneBg)).Padding(0, 1),
		PillError:        lipgloss.NewStyle().Foreground(colorOf(colorDanger)).Background(colorOf(colorErrorBg)).Padding(0, 1),
		CardTitle:        lipgloss.NewStyle().Foreground(colorOf(colorAccent)).Bold(true),
		InputPrompt:      lipgloss.NewStyle().Foreground(colorOf(colorAccent)),
		InputText:        lipgloss.NewStyle().Foreground(colorOf(colorText)),
		InputPlaceholder: lipgloss.NewStyle().Foreground(colorOf(colorMuted)),
		InputCursor:      lipgloss.NewStyle().Foreground(colorOf(colorAccent)),
		KeyLabel:         lipgloss.NewStyle().Foreground(colorOf(colorMuted)),
		Divider:          lipgloss.NewStyle().Foreground(colorOf(colorBorder)),
		Background:       lipgloss.NewStyle().Foreground(colorOf(colorText)).Background(colorOf(colorBackground)),
		Popup:            lipgloss.NewStyle().Foreground(colorOf(colorText)).Background(colorOf(colorPopup)),
	}
}

// backgroundSequence returns the escape sequence that starts a line with the
// surface color of the interface. The scheme always brings its own
// background, so the view looks the same in every terminal.
func backgroundSequence() string {
	rendered := theme.Background.Render(" ")
	index := strings.Index(rendered, "m")
	if index <= 0 {
		return ""
	}
	return rendered[:index+1]
}
