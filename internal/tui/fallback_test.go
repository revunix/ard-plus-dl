package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestInterfaceWorksWithoutColor checks that the interface still renders
// when the terminal has no colors at all.
func TestInterfaceWorksWithoutColor(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(profile)

	view := populatedScreen("library", 80, 24).View()
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("the view carries color codes without color support:\n%q", view)
	}
	for _, want := range []string{"Serien", "TITEL", "öffnen"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view misses %q:\n%s", want, view)
		}
	}
}

// TestGradientsFollowTheColorProfile checks that the gradients use the
// colors of the terminal instead of raw true color values.
func TestGradientsFollowTheColorProfile(t *testing.T) {
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)

	lipgloss.SetColorProfile(termenv.ANSI256)
	out := gradientText("ard-plus-dl")
	if strings.Contains(out, "38;2;") {
		t.Fatalf("the gradient uses true color in a 256 color terminal: %q", out)
	}

	lipgloss.SetColorProfile(termenv.TrueColor)
	out = gradientText("ard-plus-dl")
	if !strings.Contains(out, "38;2;") {
		t.Fatalf("the gradient has no true color on a true color terminal: %q", out)
	}
}
