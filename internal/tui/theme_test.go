package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// TestThemeModesUseTheirPalette checks that the color schemes differ.
func TestThemeModesUseTheirPalette(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	defer applyTheme(ThemeAuto)

	applyTheme(ThemeLight)
	light := ansi.StringWidth(theme.Accent.Render("x"))
	lightColor := theme.Accent.Render("x")

	applyTheme(ThemeDark)
	darkColor := theme.Accent.Render("x")

	if lightColor == darkColor {
		t.Fatalf("light and dark use the same accent color: %q", lightColor)
	}
	if !strings.Contains(lightColor, "0;52;128") {
		t.Fatalf("light accent = %q, want the light palette", lightColor)
	}
	if !strings.Contains(darkColor, "107;168;255") {
		t.Fatalf("dark accent = %q, want the dark palette", darkColor)
	}
	if light <= 0 {
		t.Fatalf("accent width = %d", light)
	}
}

// TestChosenSchemePaintsTheBackground checks that a scheme brings its own
// surface, no matter whether it was chosen by hand or detected.
func TestChosenSchemePaintsTheBackground(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	defer applyTheme(ThemeAuto)

	applyTheme(ThemeDark)
	view := populatedScreen("library", 60, 16).View()
	if !strings.Contains(view, "48;2;10;14;20") {
		t.Fatalf("the dark scheme does not paint its background:\n%q", view)
	}
	for index, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != 60 {
			t.Errorf("line %d is %d cells wide, want 60", index, got)
		}
	}

	// The automatic mode paints the same surface as the dark scheme.
	applyTheme(ThemeAuto)
	auto := populatedScreen("library", 60, 16).View()
	if !strings.Contains(auto, "48;2;10;14;20") {
		t.Fatalf("the automatic scheme does not paint its background:\n%q", auto)
	}
}

// TestThemeIsStoredAndRead checks that the choice survives a restart.
func TestThemeIsStoredAndRead(t *testing.T) {
	defer applyTheme(ThemeAuto)
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer state.Close()

	model := newModel(Options{Config: &config.Options{}, Store: state})
	model.setTheme(ThemeDark)
	value, err := state.Setting(store.SettingTheme)
	if err != nil {
		t.Fatalf("read theme: %v", err)
	}
	if value != "dark" {
		t.Fatalf("stored theme = %q, want %q", value, "dark")
	}

	other := newModel(Options{Config: &config.Options{}, Store: state})
	if themeMode != ThemeDark {
		t.Fatalf("theme mode = %v, want the stored dark scheme", themeMode)
	}
	if other.opts.Store == nil {
		t.Fatal("the store is missing")
	}
}

// TestSettingsSwitchTheTheme checks the color scheme in the settings.
func TestSettingsSwitchTheTheme(t *testing.T) {
	defer applyTheme(ThemeAuto)
	model := populatedScreen("start", 100, 24)
	model.formPurpose = formSettings
	model.focusField(model.themeFieldIndex())

	view := model.View()
	if !strings.Contains(view, "Erscheinungsbild") {
		t.Fatalf("settings miss the color scheme:\n%s", view)
	}
	for _, want := range []string{"Erscheinungsbild", "h/d/t"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings miss %q:\n%s", want, view)
		}
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(*Model)
	if themeMode != ThemeDark {
		t.Fatalf("theme mode = %v, want dark", themeMode)
	}
	if !strings.Contains(model.View(), "Dunkel") {
		t.Fatalf("settings do not show the new scheme:\n%s", model.View())
	}

	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if themeMode != ThemeLight {
		t.Fatalf("theme mode = %v, want light", themeMode)
	}
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if themeMode != ThemeAuto {
		t.Fatalf("theme mode = %v, want auto", themeMode)
	}
}

// TestSplashScreenHandsOver checks the splash screen and its handover.
func TestSplashScreenHandsOver(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}, Splash: true, Version: "1.2.3"})
	if model.screen != ScreenSplash {
		t.Fatalf("screen = %v, want the splash screen", model.screen)
	}
	model.width = 100
	model.height = 24
	view := model.View()
	for _, want := range []string{"ard-plus-dl", "ARD Plus Downloader", "Version 1.2.3"} {
		if !strings.Contains(view, want) {
			t.Errorf("splash misses %q:\n%s", want, view)
		}
	}

	updated, _ := model.Update(splashDoneMsg{})
	model = updated.(*Model)
	if model.screen != model.splashTarget {
		t.Fatalf("screen = %v, want the target %v", model.screen, model.splashTarget)
	}
	if model.busy {
		t.Fatal("the app is still busy after the splash screen")
	}
}

// TestSplashScreenFitsTheTerminal checks the size of the splash screen.
func TestSplashScreenFitsTheTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{40, 12}, {80, 24}, {120, 30}} {
		model := newModel(Options{Config: &config.Options{}, Splash: true})
		model.width = size.width
		model.height = size.height
		view := model.View()
		for index, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > size.width {
				t.Errorf("line %d is %d cells wide, want <= %d", index, got, size.width)
			}
		}
	}
}
