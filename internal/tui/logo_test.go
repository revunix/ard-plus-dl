package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/revunix/ard-plus-dl/internal/config"
)

func TestLogoKeepsItsProportions(t *testing.T) {
	if logoSource() == nil {
		t.Fatal("the embedded logo cannot be decoded")
	}
	for _, width := range []int{40, 64, 96} {
		lines := logoLines(width)
		if len(lines) != logoRows(width) || len(lines) == 0 {
			t.Fatalf("width %d: %d lines, want %d", width, len(lines), logoRows(width))
		}
		// The logo is about 3.65 times as wide as high; a line holds two
		// pixels.
		if ratio := float64(width) / float64(len(lines)*2); ratio < 3.2 || ratio > 4.1 {
			t.Errorf("width %d: %d lines give a ratio of %.2f", width, len(lines), ratio)
		}
		for index, line := range lines {
			if got := ansi.StringWidth(line); got != width {
				t.Fatalf("width %d: line %d is %d cells wide", width, index, got)
			}
		}
	}
}

func TestLogoFitsTheRoom(t *testing.T) {
	if got := logoWidthFor(200, 40); got != logoMaxWidth {
		t.Errorf("wide room gives %d, want the largest logo", got)
	}
	if got := logoWidthFor(200, 8); got == 0 || logoRows(got) > 8 {
		t.Errorf("low room gives %d cells with %d rows, want at most 8 rows", got, logoRows(got))
	}
	if got := logoWidthFor(30, 20); got != 0 {
		t.Errorf("narrow room gives %d, want no logo", got)
	}
}

func TestLogoColorsFollowTheScheme(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	defer applyTheme(ThemeAuto)

	applyTheme(ThemeDark)
	dark := strings.Join(logoLines(80), "\n")
	if !strings.Contains(dark, "38;2;255;255;255") || !strings.Contains(dark, "2;88;159;255") {
		t.Fatal("the dark logo misses the white lettering or the blue box")
	}
	applyTheme(ThemeLight)
	light := strings.Join(logoLines(80), "\n")
	if strings.Contains(light, "38;2;255;255;255") || !strings.Contains(light, "38;2;0;30;80") {
		t.Fatal("on a light page the lettering should turn ARD blue")
	}
}

func TestSplashShowsTheLogo(t *testing.T) {
	for _, size := range []struct {
		width, height int
		logo          bool
	}{{40, 12, false}, {80, 24, true}, {120, 30, true}} {
		model := newModel(Options{Config: &config.Options{}, Splash: true, Version: "1.2.3"})
		model.width, model.height = size.width, size.height
		view := model.View()
		assertFitsWidth(t, view, size.width)
		assertFitsHeight(t, view, size.height)
		if got := strings.ContainsAny(view, "▀▄"); got != size.logo {
			t.Errorf("%dx%d: logo shown = %v, want %v:\n%s", size.width, size.height, got, size.logo, view)
		}
		if !strings.Contains(view, "Version 1.2.3") {
			t.Errorf("%dx%d: splash misses the version", size.width, size.height)
		}
	}
}
