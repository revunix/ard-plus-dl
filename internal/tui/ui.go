package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
)

// UI adapts the Bubble Tea program to the app.UI interface.
type UI struct {
	prog *tea.Program
}

// NewUI builds the program.
func NewUI(opts Options) *UI {
	model := newModel(opts)
	return &UI{prog: tea.NewProgram(model, tea.WithAltScreen())}
}

// Program returns the underlying Bubble Tea program.
func (u *UI) Program() *tea.Program { return u.prog }

// Start is a no-op: the model collects the inputs before the run begins.
func (u *UI) Start(context.Context, *config.Options) error { return nil }

// SelectSeasons blocks until the user confirms a season selection.
func (u *UI) SelectSeasons(ctx context.Context, show string, seasons []app.Season) ([]int, error) {
	reply := make(chan []int, 1)
	done := make(chan struct{})
	u.prog.Send(SeasonsRequest{Show: show, Seasons: seasons, Reply: reply, Done: done})
	select {
	case selected := <-reply:
		return selected, nil
	case <-done:
		return nil, errQuit
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SelectCategories blocks until the user confirms a category selection.
func (u *UI) SelectCategories(ctx context.Context, title string, categories []app.Category) ([]string, error) {
	reply := make(chan []string, 1)
	done := make(chan struct{})
	u.prog.Send(CategoriesRequest{Title: title, Categories: categories, Reply: reply, Done: done})
	select {
	case selected := <-reply:
		return selected, nil
	case <-done:
		return nil, errQuit
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AskTatortSkip blocks until the user confirms the number of skipped episodes.
func (u *UI) AskTatortSkip(ctx context.Context, city string, count int) (int, error) {
	reply := make(chan int, 1)
	done := make(chan struct{})
	u.prog.Send(SkipRequest{City: city, Count: count, Reply: reply, Done: done})
	select {
	case skip := <-reply:
		return skip, nil
	case <-done:
		return 0, errQuit
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Event forwards one app event into the program loop.
func (u *UI) Event(event app.Event) {
	u.prog.Send(EventMsg{Event: event})
}
