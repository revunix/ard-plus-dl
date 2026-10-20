package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
)

// testUI is a UI whose program runs without a terminal, so the tests can
// send messages into it.
type testUI struct {
	*UI
	prog *tea.Program
	done chan struct{}
}

// newTestUI starts the program in the background.
func newTestUI(t *testing.T) *testUI {
	t.Helper()
	model := newModel(Options{Config: &config.Options{}, Run: noopRun})
	prog := tea.NewProgram(model, tea.WithInput(nil), tea.WithoutRenderer())
	ui := &UI{prog: prog}
	tu := &testUI{UI: ui, prog: prog, done: make(chan struct{})}
	go func() {
		defer close(tu.done)
		_, _ = prog.Run()
	}()
	t.Cleanup(func() {
		prog.Quit()
		select {
		case <-tu.done:
		case <-time.After(2 * time.Second):
		}
	})
	return tu
}

// pressKey sends a key until the program has handled it. The request of the
// adapter and the key come from different goroutines, so the key can arrive
// before the request; sending it again is harmless.
func (tu *testUI) pressKey(t *testing.T, key tea.KeyMsg, answered func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if answered() {
			return
		}
		tu.prog.Send(key)
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSelectSeasonsAnswersTheRequest checks that the adapter hands the
// chosen seasons back to the caller.
func TestSelectSeasonsAnswersTheRequest(t *testing.T) {
	tu := newTestUI(t)
	result := make(chan []int, 1)
	errs := make(chan error, 1)
	var answered atomic.Bool

	go func() {
		selected, err := tu.SelectSeasons(context.Background(), "Test Show", []app.Season{
			{Number: 1, Title: "Staffel 1", ID: "s1"},
			{Number: 2, Title: "Staffel 2", ID: "s2"},
		})
		if err != nil {
			errs <- err
			answered.Store(true)
			return
		}
		result <- selected
		answered.Store(true)
	}()

	tu.pressKey(t, tea.KeyMsg{Type: tea.KeyEnter}, answered.Load)
	if !answered.Load() {
		t.Fatal("the adapter did not answer the request")
	}

	select {
	case err := <-errs:
		t.Fatalf("SelectSeasons: %v", err)
	case selected := <-result:
		if len(selected) != 2 || selected[0] != 1 || selected[1] != 2 {
			t.Fatalf("selected seasons = %v, want [1 2]", selected)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the adapter did not answer the request")
	}
}

// TestSelectSeasonsGivesUpOnACancelledContext checks that a cancelled run
// does not leave the caller waiting.
func TestSelectSeasonsGivesUpOnACancelledContext(t *testing.T) {
	tu := newTestUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tu.SelectSeasons(ctx, "Test Show", []app.Season{{Number: 1, Title: "Staffel 1"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SelectSeasons error = %v, want context.Canceled", err)
	}
}

// TestSelectCategoriesGivesUpWhenTheProgramQuits checks that a quit releases
// the waiting caller.
func TestSelectCategoriesGivesUpWhenTheProgramQuits(t *testing.T) {
	tu := newTestUI(t)
	result := make(chan error, 1)

	go func() {
		_, err := tu.SelectCategories(context.Background(), "Tatort-Archiv", []app.Category{
			{Slug: "tatort-koeln", Title: "Köln"},
		})
		result <- err
	}()

	// Give the request time to reach the model, then quit the program.
	time.Sleep(100 * time.Millisecond)
	tu.prog.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	select {
	case err := <-result:
		if !errors.Is(err, errQuit) {
			t.Fatalf("SelectCategories error = %v, want errQuit", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the adapter did not release the waiting caller")
	}
}

// TestAskTatortSkipAnswersTheRequest checks the skip answer of the adapter.
func TestAskTatortSkipAnswersTheRequest(t *testing.T) {
	tu := newTestUI(t)
	result := make(chan int, 1)
	errs := make(chan error, 1)
	var answered atomic.Bool

	go func() {
		skip, err := tu.AskTatortSkip(context.Background(), "Köln", 12)
		if err != nil {
			errs <- err
			answered.Store(true)
			return
		}
		result <- skip
		answered.Store(true)
	}()

	// The screen starts with 0, so the answer is 0.
	tu.pressKey(t, tea.KeyMsg{Type: tea.KeyEnter}, answered.Load)
	if !answered.Load() {
		t.Fatal("the adapter did not answer the request")
	}

	select {
	case err := <-errs:
		t.Fatalf("AskTatortSkip: %v", err)
	case skip := <-result:
		if skip != 0 {
			t.Fatalf("skip = %d, want 0", skip)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the adapter did not answer the request")
	}
}

// TestEventReachesTheModel checks that the events of the app arrive in the
// program.
func TestEventReachesTheModel(t *testing.T) {
	model := newModel(Options{Config: &config.Options{}, Run: noopRun})
	prog := tea.NewProgram(model, tea.WithInput(nil), tea.WithoutRenderer())
	ui := &UI{prog: prog}
	done := make(chan tea.Model, 1)
	go func() {
		final, _ := prog.Run()
		done <- final
	}()

	ui.Event(app.Event{Kind: app.EventFailed, Message: "kaputt"})
	prog.Quit()

	var final tea.Model
	select {
	case final = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the program did not stop")
	}
	finished, ok := final.(*Model)
	if !ok {
		t.Fatalf("the program returned %T, want *Model", final)
	}
	if len(finished.log) == 0 {
		t.Fatal("the event did not reach the model")
	}
	if !strings.Contains(finished.log[len(finished.log)-1], "kaputt") {
		t.Fatalf("log = %v, want the message of the event", finished.log)
	}
}
