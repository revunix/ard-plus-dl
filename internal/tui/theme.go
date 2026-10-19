package tui

import "github.com/charmbracelet/lipgloss"

// Theme colors follow the ARD Plus palette: deep ARD blue, bright blue and
// cyan accents on dark terminals, with darker blue counterparts on light ones.
var (
	colorAccent   = lipgloss.AdaptiveColor{Light: "#003480", Dark: "#589FFF"}
	colorText     = lipgloss.AdaptiveColor{Light: "#001E50", Dark: "#F1F6FF"}
	colorMuted    = lipgloss.AdaptiveColor{Light: "#4A6B9A", Dark: "#8FB4E8"}
	colorBorder   = lipgloss.AdaptiveColor{Light: "#B8D2F2", Dark: "#1F4C8C"}
	colorSuccess  = lipgloss.AdaptiveColor{Light: "#00796B", Dark: "#45DECF"}
	colorWarning  = lipgloss.AdaptiveColor{Light: "#7A6A00", Dark: "#C5DF4E"}
	colorDanger   = lipgloss.AdaptiveColor{Light: "#B3122A", Dark: "#FF4859"}
	colorInfo     = lipgloss.AdaptiveColor{Light: "#005EC8", Dark: "#589FFF"}
	colorAccentBg = lipgloss.AdaptiveColor{Light: "#D6E8FF", Dark: "#003480"}
	colorNeutral  = lipgloss.AdaptiveColor{Light: "#E8F1FF", Dark: "#132F57"}
	colorOpenBg   = lipgloss.AdaptiveColor{Light: "#E1ECFD", Dark: "#06489B"}
	colorDoneBg   = lipgloss.AdaptiveColor{Light: "#DDF6F2", Dark: "#0B4F4A"}
	colorErrorBg  = lipgloss.AdaptiveColor{Light: "#FFE1E5", Dark: "#5A1620"}

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
	Badge            lipgloss.Style
	PillNeutral      lipgloss.Style
	PillOpen         lipgloss.Style
	PillDone         lipgloss.Style
	PillError        lipgloss.Style
	PanelTitle       lipgloss.Style
	InputPrompt      lipgloss.Style
	InputText        lipgloss.Style
	InputPlaceholder lipgloss.Style
	InputCursor      lipgloss.Style
	Key              lipgloss.Style
	KeyLabel         lipgloss.Style
	Divider          lipgloss.Style
}

// theme is the active design system.
var theme = newTheme()

func newTheme() appTheme {
	return appTheme{
		Title:            lipgloss.NewStyle().Bold(true).Foreground(colorText),
		Subtitle:         lipgloss.NewStyle().Foreground(colorMuted),
		Muted:            lipgloss.NewStyle().Foreground(colorMuted),
		Text:             lipgloss.NewStyle().Foreground(colorText),
		Accent:           lipgloss.NewStyle().Foreground(colorAccent),
		Label:            lipgloss.NewStyle().Foreground(colorMuted),
		Success:          lipgloss.NewStyle().Foreground(colorSuccess).Bold(true),
		Warning:          lipgloss.NewStyle().Foreground(colorWarning).Bold(true),
		Danger:           lipgloss.NewStyle().Foreground(colorDanger).Bold(true),
		Info:             lipgloss.NewStyle().Foreground(colorInfo),
		Selected:         lipgloss.NewStyle().Foreground(colorAccent).Background(colorAccentBg).Bold(true),
		Badge:            lipgloss.NewStyle().Foreground(colorAccent).Background(colorNeutral).Padding(0, 1),
		PillNeutral:      lipgloss.NewStyle().Foreground(colorMuted).Background(colorNeutral).Padding(0, 1),
		PillOpen:         lipgloss.NewStyle().Foreground(colorInfo).Background(colorOpenBg).Padding(0, 1),
		PillDone:         lipgloss.NewStyle().Foreground(colorSuccess).Background(colorDoneBg).Padding(0, 1),
		PillError:        lipgloss.NewStyle().Foreground(colorDanger).Background(colorErrorBg).Padding(0, 1),
		PanelTitle:       lipgloss.NewStyle().Foreground(colorAccent).Bold(true),
		InputPrompt:      lipgloss.NewStyle().Foreground(colorAccent),
		InputText:        lipgloss.NewStyle().Foreground(colorText),
		InputPlaceholder: lipgloss.NewStyle().Foreground(colorMuted),
		InputCursor:      lipgloss.NewStyle().Foreground(colorAccent),
		Key:              lipgloss.NewStyle().Foreground(colorAccent).Bold(true),
		KeyLabel:         lipgloss.NewStyle().Foreground(colorMuted),
		Divider:          lipgloss.NewStyle().Foreground(colorBorder),
	}
}
