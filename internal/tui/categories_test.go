package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
)

// The Tatort archive is large, so nothing may be preselected there.
func TestCategoriesScreenPreselectsNothing(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}})
	model.seriesOffset = 5
	reply := make(chan []string, 1)
	updated, _ := model.Update(CategoriesRequest{
		Title: "Tatort-Archiv",
		Categories: []app.Category{
			{Slug: "tatort-koeln", Title: "Tatort Koeln"},
			{Slug: "tatort-hamburg", Title: "Tatort Hamburg"},
		},
		Reply: reply,
	})
	model = updated.(*Model)
	if model.seriesOffset != 0 {
		t.Fatalf("seriesOffset = %d, want it reset for the category list", model.seriesOffset)
	}

	view := model.View()
	if !strings.Contains(view, "Tatort Koeln") || !strings.Contains(view, "Tatort Hamburg") {
		t.Fatalf("categories are not listed:\n%s", view)
	}
	if strings.Contains(view, "[x]") {
		t.Fatalf("categories must start unselected:\n%s", view)
	}

	// Enter without a selection must not answer.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	select {
	case slugs := <-reply:
		t.Fatalf("enter without a selection replied %v", slugs)
	default:
	}

	// Space toggles the highlighted category, "a" all of them.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)
	if !strings.Contains(model.View(), "[x]") {
		t.Fatalf("space did not select the entry:\n%s", model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(*Model)
	if got := strings.Count(model.View(), "[x]"); got != 2 {
		t.Fatalf("'a' selected %d entries, want 2:\n%s", got, model.View())
	}

	// Back to a single selection, then confirm.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	select {
	case slugs := <-reply:
		if len(slugs) != 1 || slugs[0] != "tatort-koeln" {
			t.Fatalf("replied slugs = %v, want [tatort-koeln]", slugs)
		}
	default:
		t.Fatal("enter did not reply")
	}
	if model.screen != ScreenLoading {
		t.Fatalf("screen = %v, want loading", model.screen)
	}
}

// A season request still preselects everything and answers with numbers.
func TestSeasonSelectionStillPreselectsAll(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}})
	reply := make(chan []int, 1)
	updated, _ := model.Update(SeasonsRequest{
		Show:    "Test Show",
		Seasons: []app.Season{{Number: 1, Title: "Erste"}, {Number: 2, Title: "Zweite"}},
		Reply:   reply,
	})
	model = updated.(*Model)
	if got := strings.Count(model.View(), "[x]"); got != 2 {
		t.Fatalf("seasons preselected = %d, want 2:\n%s", got, model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated
	select {
	case numbers := <-reply:
		if len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
			t.Fatalf("replied seasons = %v", numbers)
		}
	default:
		t.Fatal("enter did not reply")
	}
}
