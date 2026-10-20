package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// update drives one message through the model and returns the concrete model.
func update(t *testing.T, m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	model, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", next)
	}
	return model, cmd
}

func typeText(t *testing.T, m *Model, text string) *Model {
	t.Helper()
	for _, r := range text {
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		m, _ = update(t, m, key)
	}
	return m
}

// runCommands executes a command tree and feeds every resulting message into
// the model until the update loop is idle.
func runCommands(t *testing.T, m *Model, cmd tea.Cmd) *Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		var follow tea.Cmd
		m, follow = update(t, m, msg)
		if follow != nil {
			queue = append(queue, follow)
		}
	}
	return m
}

func noopRun(context.Context) error { return nil }

func TestStartScreenValidation(t *testing.T) {
	cfg := &config.Options{}
	m := newModel(Options{Config: cfg, StartScreen: true, Run: noopRun})
	if m.screen != ScreenStart {
		t.Fatalf("screen = %v, want ScreenStart", m.screen)
	}

	// An empty URL is rejected.
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.startErr == nil || m.startErr.Error() != "URL or --links-file required" {
		t.Fatalf("startErr = %v, want URL or --links-file required", m.startErr)
	}

	// A foreign host is rejected by the URL validation.
	m.fields[0].SetValue("https://example.com/details/x")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.startErr == nil || m.startErr.Error() != "Invalid URL: unsupported host: example.com" {
		t.Fatalf("startErr = %v, want the unsupported host reason", m.startErr)
	}

	// A valid URL without a tag is rejected.
	m.fields[0].SetValue("https://www.ardplus.de/details/abc")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.startErr == nil || m.startErr.Error() != config.ValidateFileTag("").Error() {
		t.Fatalf("startErr = %v, want the missing tag reason", m.startErr)
	}

	// A valid URL with a valid tag starts the run.
	m.fields[3].SetValue("example")
	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.startErr != nil {
		t.Fatalf("startErr = %v, want nil", m.startErr)
	}
	if cfg.URL != "https://www.ardplus.de/details/abc" {
		t.Errorf("cfg.URL = %q, want the normalized URL", cfg.URL)
	}
	if cfg.FileTag != "example" {
		t.Errorf("cfg.FileTag = %q, want example", cfg.FileTag)
	}
	if m.screen != ScreenLoading {
		t.Errorf("screen = %v, want ScreenLoading", m.screen)
	}
	if cmd == nil {
		t.Fatal("starting the run must return a command")
	}
	if msg, ok := cmd().(RunDoneMsg); !ok || msg.Err != nil {
		t.Errorf("run command message = %#v, want RunDoneMsg without error", cmd())
	}
}

func TestStartScreenRequiresCredentials(t *testing.T) {
	cfg := &config.Options{}
	m := newModel(Options{Config: cfg, StartScreen: true, NeedsLogin: true, Run: noopRun})
	m.fields[0].SetValue("https://www.ardplus.de/details/abc")
	m.fields[3].SetValue("example")

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.startErr == nil || m.startErr.Error() != "Credentials missing! Set ARD_PLUS_USER/ARD_PLUS_PASSWORD or enter them above." {
		t.Fatalf("startErr = %v, want the credentials reason", m.startErr)
	}
	if m.screen != ScreenStart {
		t.Errorf("screen = %v, want to stay on ScreenStart", m.screen)
	}

	m.fields[1].SetValue("user")
	m.fields[2].SetValue("pass")
	if _, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("credentials must let the run start")
	}
}

func seasonsRequest(t *testing.T, m *Model, seasons []app.Season) (*Model, chan []int) {
	t.Helper()
	reply := make(chan []int, 1)
	m, _ = update(t, m, SeasonsRequest{Show: "Test Show", Seasons: seasons, Reply: reply})
	return m, reply
}

func TestSeasonsRequestPreselectsAndToggles(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	seasons := []app.Season{
		{Number: 1, Title: "Staffel 1", ID: "s1"},
		{Number: 2, Title: "Staffel 2", ID: "s2"},
		{Number: 3, Title: "Staffel 3", ID: "s3"},
	}
	m, reply := seasonsRequest(t, m, seasons)
	if m.screen != ScreenSeries {
		t.Fatalf("screen = %v, want ScreenSeries", m.screen)
	}
	if m.showName != "Test Show" {
		t.Errorf("showName = %q, want Test Show", m.showName)
	}
	for _, season := range seasons {
		if !m.selection[season.Number] {
			t.Errorf("season %d must be preselected", season.Number)
		}
	}
	if got := m.selectedSeasons(); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("selectedSeasons() = %v, want all seasons in order", got)
	}

	// Space toggles the season under the cursor.
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.selection[1] {
		t.Error("space must deselect the first season")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after j", m.cursor)
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.selection[2] {
		t.Error("space must deselect the season under the cursor")
	}
	if got := m.selectedSeasons(); len(got) != 1 || got[0] != 3 {
		t.Fatalf("selectedSeasons() = %v, want [3]", got)
	}

	// "a" selects everything again, then deselects everything.
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if got := m.selectedSeasons(); len(got) != 3 {
		t.Fatalf("selectedSeasons() = %v, want all after the first a", got)
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if got := m.selectedSeasons(); len(got) != 0 {
		t.Fatalf("selectedSeasons() = %v, want none after the second a", got)
	}

	// Enter without a selection does not answer.
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-reply:
		t.Fatalf("reply = %v, want no reply for an empty selection", got)
	default:
	}
	if m.screen != ScreenSeries {
		t.Errorf("screen = %v, want to stay on ScreenSeries", m.screen)
	}

	// Select all and confirm: the reply carries the numbers in order.
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-reply:
		if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
			t.Errorf("reply = %v, want [1 2 3]", got)
		}
	default:
		t.Fatal("enter must answer the seasons request")
	}
	if m.screen != ScreenLoading {
		t.Errorf("screen = %v, want ScreenLoading after the confirmation", m.screen)
	}
}

func TestSkipRequestParsing(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	reply := make(chan int, 1)
	m, _ = update(t, m, SkipRequest{City: "Koeln", Count: 5, Reply: reply})
	if m.screen != ScreenTatort {
		t.Fatalf("screen = %v, want ScreenTatort", m.screen)
	}
	if m.tatortCity != "Koeln" {
		t.Errorf("tatortCity = %q, want Koeln", m.tatortCity)
	}
	if m.tatortCount != 5 {
		t.Errorf("tatortCount = %d, want 5", m.tatortCount)
	}
	if view := m.View(); !strings.Contains(view, "0 bis 5") {
		t.Errorf("tatort view misses the valid range:\n%s", view)
	}

	// A value above the available episodes is rejected without a reply.
	m.skipField.SetValue("")
	m = typeText(t, m, "34")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != ScreenTatort {
		t.Errorf("screen = %v, want to stay on ScreenTatort", m.screen)
	}
	if m.skipErr != "Bitte eine Zahl zwischen 0 und 5 eingeben." {
		t.Errorf("skipErr = %q, want the range hint", m.skipErr)
	}
	if view := m.View(); !strings.Contains(view, "Bitte eine Zahl zwischen 0 und 5 eingeben.") {
		t.Errorf("tatort view misses the input error:\n%s", view)
	}
	select {
	case got := <-reply:
		t.Fatalf("reply = %d, want no reply for an out-of-range value", got)
	default:
	}

	// Non-numeric input is rejected as well; typing clears the error.
	m.skipField.SetValue("")
	m = typeText(t, m, "x")
	if m.skipErr != "" {
		t.Errorf("skipErr = %q, want it cleared while typing", m.skipErr)
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.skipErr != "Bitte eine Zahl zwischen 0 und 5 eingeben." {
		t.Errorf("skipErr = %q, want the range hint", m.skipErr)
	}
	select {
	case got := <-reply:
		t.Fatalf("reply = %d, want no reply for non-numeric input", got)
	default:
	}

	// A valid value answers the request.
	m.skipField.SetValue("")
	m = typeText(t, m, "3")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-reply:
		if got != 3 {
			t.Errorf("reply = %d, want 3", got)
		}
	default:
		t.Fatal("enter must answer the skip request")
	}
	if m.skipErr != "" {
		t.Errorf("skipErr = %q, want it cleared after a valid value", m.skipErr)
	}
	if m.screen != ScreenLoading {
		t.Errorf("screen = %v, want ScreenLoading", m.screen)
	}
}

func TestEventMsgUpdatesJob(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})

	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventLog, Message: "erste Zeile"}})
	if len(m.log) != 1 || m.log[0] != "erste Zeile" {
		t.Fatalf("log = %v, want the event message", m.log)
	}

	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventQueued, Label: "Lade X", Path: "/downloads/X"}})
	if len(m.jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(m.jobs))
	}
	if m.jobs[0].status != "wartet" || m.jobs[0].label != "Lade X" || m.jobs[0].path != "/downloads/X" {
		t.Errorf("job = %+v, want a queued job", m.jobs[0])
	}

	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventProgress, Label: "Lade X", Done: 2, Total: 4}})
	if m.jobs[0].status != "lädt" || m.jobs[0].done != 2 || m.jobs[0].total != 4 {
		t.Errorf("job = %+v, want progress 2/4", m.jobs[0])
	}

	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventDone, Label: "Lade X", Path: "/downloads/final.mp4"}})
	if m.jobs[0].status != "fertig" || m.jobs[0].path != "/downloads/final.mp4" {
		t.Errorf("job = %+v, want a finished job with the final path", m.jobs[0])
	}

	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventQueued, Label: "Lade Y", Path: "/downloads/Y"}})
	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventFailed, Label: "Lade Y", Message: "boom"}})
	if len(m.jobs) != 2 || m.jobs[1].status != "fehlgeschlagen" {
		t.Errorf("jobs = %+v, want the second job failed", m.jobs)
	}
}

func TestEventMsgProgressWithoutJobIsIgnored(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, EventMsg{Event: app.Event{Kind: app.EventProgress, Done: 1, Total: 2}})
	if len(m.jobs) != 0 {
		t.Errorf("jobs = %v, want none", m.jobs)
	}
}

func TestRunDoneMsgSetsExitCode(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, RunDoneMsg{})
	if m.exitCode != 0 || m.screen != ScreenSummary || m.doneErr != nil {
		t.Errorf("exitCode = %d, screen = %v, doneErr = %v; want 0/Summary/nil", m.exitCode, m.screen, m.doneErr)
	}

	failed := newModel(Options{Config: &config.Options{}, Run: noopRun})
	failed, _ = update(t, failed, RunDoneMsg{Err: errors.New("kaputt")})
	if failed.exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", failed.exitCode)
	}
	if failed.doneErr == nil || failed.doneErr.Error() != "kaputt" {
		t.Errorf("doneErr = %v, want kaputt", failed.doneErr)
	}
}

func TestCtrlCCancelsTheRunningDownload(t *testing.T) {
	started := make(chan struct{})
	run := func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	m := newModel(Options{Config: &config.Options{}, Run: run})
	cmd := m.startRun()
	if cmd == nil {
		t.Fatal("startRun must return a command")
	}
	go func() { _ = cmd() }()
	<-started

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.cancelled {
		t.Error("ctrl+c must mark the run as cancelled")
	}
	select {
	case <-m.ctx.Done():
	default:
		t.Fatal("ctrl+c must cancel the model context")
	}

	// The cancelled run reports exit code 130 when it finishes.
	m, finalCmd := update(t, m, RunDoneMsg{Err: context.Canceled})
	if m.exitCode != 130 {
		t.Errorf("exitCode = %d, want 130", m.exitCode)
	}
	if finalCmd == nil {
		t.Error("a cancelled run must quit the program")
	}
	if m.screen != ScreenSummary {
		t.Errorf("screen = %v, want ScreenSummary", m.screen)
	}
}

func TestCtrlCCancelsFromQueue(t *testing.T) {
	run := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	m := newModel(Options{Config: &config.Options{}, Run: run})
	m.screen = ScreenQueue
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case <-m.ctx.Done():
	default:
		t.Fatal("ctrl+c in the queue must cancel the context")
	}
	if !m.cancelled {
		t.Error("ctrl+c in the queue must mark the run as cancelled")
	}
}

func TestQuitFromSummary(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.screen = ScreenSummary
	_, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Error("q on the summary must quit the program")
	}
}

func TestRunDoneClearsRunningFlag(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.running = true
	m.screen = ScreenLoading
	m, _ = update(t, m, RunDoneMsg{})
	if m.running {
		t.Fatal("RunDoneMsg must clear the running flag")
	}
	if cmd := m.startRun(); cmd == nil {
		t.Error("startRun must run again after RunDoneMsg")
	}
}

func TestLibrarySettingsDoesNotStartRun(t *testing.T) {
	called := false
	cfg := &config.Options{URL: "https://www.ardplus.de/details/alt", FileTag: "example"}
	m := newModel(Options{
		Config: cfg,
		Run: func(context.Context) error {
			called = true
			return nil
		},
	})
	m.library = []store.Series{{ID: 1}}
	m.screen = ScreenLibrary

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.formPurpose != formSettings || m.screen != ScreenStart {
		t.Fatalf("s opened formPurpose %v on screen %v, want settings on the start form", m.formPurpose, m.screen)
	}
	if got := m.fields[0].Value(); got != cfg.URL {
		t.Errorf("URL field = %q, want the configured URL", got)
	}
	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("saving settings must not start a run")
	}
	if called {
		t.Error("the run was started while saving settings")
	}
	if m.screen != ScreenLibrary || m.toast != "Gespeichert." {
		t.Errorf("screen = %v, message = %q; want the library with a save confirmation", m.screen, m.toast)
	}
}

func TestLibraryAddRequiresURL(t *testing.T) {
	called := false
	m := newModel(Options{
		Config: &config.Options{FileTag: "example"},
		Run: func(context.Context) error {
			called = true
			return nil
		},
	})
	m.library = []store.Series{{ID: 1}}
	m.screen = ScreenLibrary

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.formPurpose != formAddURL || m.screen != ScreenStart {
		t.Fatalf("a opened formPurpose %v on screen %v, want add URL on the start form", m.formPurpose, m.screen)
	}
	if got := m.fields[0].Value(); got != "" {
		t.Errorf("URL field = %q, want it empty", got)
	}

	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || called {
		t.Fatal("an empty add URL must not start a run")
	}
	if m.startErr == nil || m.startErr.Error() != "Bitte eine URL eingeben." {
		t.Fatalf("startErr = %v, want the empty URL hint", m.startErr)
	}
	if m.screen != ScreenStart {
		t.Errorf("screen = %v, want to stay on ScreenStart", m.screen)
	}

	m.fields[0].SetValue("https://www.ardplus.de/details/neu")
	m, cmd = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("a new URL must start a run")
	}
	if _, ok := cmd().(RunDoneMsg); !ok || !called {
		t.Errorf("run command did not run the configured action")
	}
	if got := m.cfg.URL; got != "https://www.ardplus.de/details/neu" {
		t.Errorf("cfg.URL = %q, want the new URL", got)
	}
}

func TestLibraryDownloadMissingTagContinuesDownload(t *testing.T) {
	called := false
	entry := store.Series{ID: 7, URL: "https://www.ardplus.de/details/x", Title: "Test Show", Kind: store.KindSeries, NewCount: 1}
	m := newModel(Options{
		Config: &config.Options{},
		DownloadNew: func(context.Context, store.Series) error {
			called = true
			return nil
		},
		Run: func(context.Context) error {
			t.Error("a pending library download must not start a URL run")
			return nil
		},
	})
	m.library = []store.Series{entry}
	m.screen = ScreenLibrary

	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("d must count the pending episodes")
	}
	m = runCommands(t, m, cmd)
	if m.pendingDownload == nil || m.pendingDownload.ID != entry.ID {
		t.Fatal("an incomplete download must remember the library entry")
	}
	if m.formPurpose != formStart || m.screen != ScreenStart || m.startErr == nil {
		t.Fatalf("formPurpose = %v, screen = %v, startErr = %v; want the start form with an error", m.formPurpose, m.screen, m.startErr)
	}
	if got := m.fields[0].Value(); got != entry.URL {
		t.Errorf("URL field = %q, want the entry URL", got)
	}

	m.fields[3].SetValue("example")
	m, cmd = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("the pending library download must return a command")
	}
	if m.pendingDownload != nil {
		t.Error("the pending download must be cleared once it starts")
	}
	if msg, ok := cmd().(downloadNewDoneMsg); !ok || msg.entry.ID != entry.ID {
		t.Errorf("command message = %#v, want a download of the library entry", msg)
	}
	if !called {
		t.Error("the library download did not run")
	}
}

func TestStartWithConfiguredURLRunsImmediately(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/saved", "Saved Show", store.KindSeries); err != nil {
		t.Fatalf("save series: %v", err)
	}
	called := false
	model := newModel(Options{
		Config:    &config.Options{URL: "https://www.ardplus.de/details/neu", FileTag: "example"},
		AutoStart: true,
		Store:     state,
		Run: func(context.Context) error {
			called = true
			return nil
		},
	})
	if model.screen != ScreenLoading {
		t.Fatalf("screen = %v, want ScreenLoading for a configured URL", model.screen)
	}
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Init must start the configured run")
	}
	if _, ok := cmd().(RunDoneMsg); !ok || !called {
		t.Errorf("Init command = %#v, run called = %v; want a run", cmd(), called)
	}
}

func TestPrefilledURLDoesNotAutoStart(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/saved", "Saved Show", store.KindSeries); err != nil {
		t.Fatalf("save series: %v", err)
	}
	called := false
	model := newModel(Options{
		Config: &config.Options{URL: "https://www.ardplus.de/details/last", FileTag: "example"},
		Store:  state,
		Run: func(context.Context) error {
			called = true
			return nil
		},
	})
	if model.screen != ScreenLibrary {
		t.Fatalf("screen = %v, want the library for a prefilled URL", model.screen)
	}
	if cmd := model.Init(); cmd != nil {
		t.Fatalf("Init returned %v, want no automatic run", cmd)
	}
	if called {
		t.Fatal("a prefilled URL must not start a run")
	}
}

func TestQuitClosesPendingRequests(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.screen = ScreenSeries
	m.seasonsDone = make(chan struct{})
	m.categoriesDone = make(chan struct{})
	m.skipDone = make(chan struct{})
	seasonsDone, categoriesDone, skipDone := m.seasonsDone, m.categoriesDone, m.skipDone

	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.exitCode != 130 {
		t.Errorf("exitCode = %d, want 130", m.exitCode)
	}
	if m.ctx.Err() == nil {
		t.Error("quitting must cancel the model context")
	}
	for name, done := range map[string]chan struct{}{"seasons": seasonsDone, "categories": categoriesDone, "skip": skipDone} {
		select {
		case <-done:
		default:
			t.Errorf("the %s done channel was not closed", name)
		}
	}
	if cmd == nil {
		t.Fatal("quitting must return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("command message = %#v, want tea.QuitMsg", cmd())
	}
}

func TestCheckDoneReportsFailures(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, checkDoneMsg{err: errors.New("offline")})
	if m.exitCode != 1 {
		t.Errorf("exitCode = %d, want 1 for a failed check", m.exitCode)
	}

	m = newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, checkDoneMsg{results: []app.CheckResult{
		{New: []store.EpisodeRef{{ID: "a"}}},
		{Err: errors.New("kaputt")},
	}})
	if m.exitCode != 1 {
		t.Errorf("exitCode = %d, want 1 when one entry fails", m.exitCode)
	}
	if !strings.Contains(m.toast, "1 fehlgeschlagen") || !strings.Contains(m.toast, "kaputt") {
		t.Errorf("toast = %q, want the failure count and first error", m.toast)
	}
}

func TestDownloadFailuresSetExitCode(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, downloadNewDoneMsg{entry: store.Series{ID: 1}, err: errors.New("kaputt")})
	if m.exitCode != 1 {
		t.Errorf("exitCode = %d, want 1 after a failed download", m.exitCode)
	}

	refresh := newModel(Options{Config: &config.Options{}, Run: noopRun})
	refresh, _ = update(t, refresh, detailsRefreshDoneMsg{entryID: 1, err: errors.New("offline")})
	if refresh.exitCode != 1 {
		t.Errorf("exitCode = %d, want 1 after a failed refresh", refresh.exitCode)
	}
}

func seriesFixtures(count int) []store.Series {
	entries := make([]store.Series, count)
	for i := range entries {
		entries[i] = store.Series{ID: int64(i + 1), Title: "Serie " + strconv.Itoa(i+1)}
	}
	return entries
}

func seasonFixtures(count int) []app.Season {
	seasons := make([]app.Season, count)
	for i := range seasons {
		seasons[i] = app.Season{Number: i + 1, Title: "Staffel " + strconv.Itoa(i+1)}
	}
	return seasons
}

func assertVisible(t *testing.T, name string, cursor, offset, visible int) {
	t.Helper()
	if cursor < offset || cursor >= offset+visible {
		t.Fatalf("%s cursor %d is outside the window [%d, %d)", name, cursor, offset, offset+visible)
	}
	if offset < 0 {
		t.Fatalf("%s offset = %d, want it non-negative", name, offset)
	}
}

func TestWindowSizeClampsListOffsets(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.library = seriesFixtures(100)
	m.libCursor = 99
	m.seasons = seasonFixtures(100)
	m.cursor = 99

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	// The lists use the space between header, card and footer.
	if m.libVisible() < m.height-8 {
		t.Fatalf("libVisible() = %d, want the free height", m.libVisible())
	}
	if m.libOffset != 100-m.libVisible() {
		t.Errorf("libOffset = %d, want %d", m.libOffset, 100-m.libVisible())
	}
	assertVisible(t, "library", m.libCursor, m.libOffset, m.libVisible())
	if m.seriesVisible() < m.height-6 {
		t.Fatalf("seriesVisible() = %d, want the free height", m.seriesVisible())
	}
	if m.seriesOffset != 100-m.seriesVisible() {
		t.Errorf("seriesOffset = %d, want %d", m.seriesOffset, 100-m.seriesVisible())
	}
	assertVisible(t, "series", m.cursor, m.seriesOffset, m.seriesVisible())
}

func TestLibraryPagingKeys(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.library = seriesFixtures(100)
	m.screen = ScreenLibrary
	m.height = 30

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.libCursor != m.libVisible()-1 {
		t.Fatalf("pgdown cursor = %d, want %d", m.libCursor, m.libVisible()-1)
	}
	assertVisible(t, "library", m.libCursor, m.libOffset, m.libVisible())

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.libCursor != 2*m.libVisible()-2 {
		t.Fatalf("second pgdown cursor = %d, want %d", m.libCursor, 2*m.libVisible()-2)
	}
	assertVisible(t, "library", m.libCursor, m.libOffset, m.libVisible())

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if m.libCursor != 0 || m.libOffset != 0 {
		t.Fatalf("home cursor/offset = %d/%d, want 0/0", m.libCursor, m.libOffset)
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.libCursor != 99 || m.libOffset != 100-m.libVisible() {
		t.Fatalf("end cursor/offset = %d/%d, want 99/%d", m.libCursor, m.libOffset, 100-m.libVisible())
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.libCursor != 100-m.libVisible() {
		t.Fatalf("pgup cursor = %d, want %d", m.libCursor, 100-m.libVisible())
	}
	assertVisible(t, "library", m.libCursor, m.libOffset, m.libVisible())
}

func TestSeriesPagingKeys(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.seasons = seasonFixtures(50)
	m.screen = ScreenSeries
	m.height = 30

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor != m.seriesVisible()-1 {
		t.Fatalf("pgdown cursor = %d, want %d", m.cursor, m.seriesVisible()-1)
	}
	assertVisible(t, "series", m.cursor, m.seriesOffset, m.seriesVisible())

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor != 2*m.seriesVisible()-2 || m.seriesOffset != m.seriesVisible()-1 {
		t.Fatalf("second pgdown cursor/offset = %d/%d, want %d/%d", m.cursor, m.seriesOffset, 2*m.seriesVisible()-2, m.seriesVisible()-1)
	}
	assertVisible(t, "series", m.cursor, m.seriesOffset, m.seriesVisible())

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if m.cursor != 0 || m.seriesOffset != 0 {
		t.Fatalf("home cursor/offset = %d/%d, want 0/0", m.cursor, m.seriesOffset)
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != 49 || m.seriesOffset != 50-m.seriesVisible() {
		t.Fatalf("end cursor/offset = %d/%d, want 49/%d", m.cursor, m.seriesOffset, 50-m.seriesVisible())
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.cursor != 50-m.seriesVisible() {
		t.Fatalf("pgup cursor = %d, want %d", m.cursor, 50-m.seriesVisible())
	}
	assertVisible(t, "series", m.cursor, m.seriesOffset, m.seriesVisible())
}

func TestDetailVisibleSmallHeight(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.height = 12
	if got := m.detailVisible(); got < 1 || got > m.height-5 {
		t.Errorf("detailVisible() at height 12 = %d, want a positive part of the height", got)
	}
	m.height = 20
	if got := m.detailVisible(); got < m.height-9 || got > m.height-5 {
		t.Errorf("detailVisible() at height 20 = %d, want the free height", got)
	}
	m.height = 0
	if got := m.detailVisible(); got != 12 {
		t.Errorf("detailVisible() at height 0 = %d, want 12", got)
	}
}

func TestHelpToggle(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.library = seriesFixtures(3)
	m.screen = ScreenLibrary

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !m.helpVisible {
		t.Fatal("? must show the help")
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.helpVisible {
		t.Fatal("? must hide the help again")
	}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.helpVisible {
		t.Fatal("any other key must close the help")
	}
	if m.libCursor != 0 {
		t.Errorf("libCursor = %d, want 0; the closing key must be swallowed", m.libCursor)
	}

	m.helpVisible = true
	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || m.exitCode != 130 {
		t.Error("ctrl+c must quit even while the help is visible")
	}
}

func TestResizeFields(t *testing.T) {
	cases := []struct {
		width int
		want  [4]int
	}{
		{120, [4]int{100, 60, 60, 40}},
		{60, [4]int{40, 40, 40, 40}},
		{30, [4]int{10, 10, 10, 10}},
		{0, [4]int{60, 60, 60, 40}},
	}
	for _, tc := range cases {
		m := newModel(Options{Config: &config.Options{}, Run: noopRun})
		m, _ = update(t, m, tea.WindowSizeMsg{Width: tc.width, Height: 24})
		for i, want := range tc.want {
			if got := m.fields[i].Width; got != want {
				t.Errorf("width %d: field %d width = %d, want %d", tc.width, i, got, want)
			}
		}
	}
}

func TestBusyBlocksConflictingKeys(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.library = seriesFixtures(1)
	m.screen = ScreenLibrary
	m.busy = true

	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'n'}},
		{Type: tea.KeyRunes, Runes: []rune{'c'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'o'}},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune{'x'}},
	}
	for _, key := range keys {
		next, cmd := update(t, m, key)
		if cmd != nil {
			t.Errorf("%q returned a command while busy", key.String())
		}
		if next.toast != "Bitte warten, eine Aktion läuft noch." {
			t.Errorf("%q toast = %q, want the busy hint", key.String(), next.toast)
		}
		if next.pendingRemoval != 0 {
			t.Errorf("%q started a removal while busy", key.String())
		}
	}
}

func TestRunDoneResetsJobsAndLog(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.jobs = []job{{label: "alt"}}
	m.log = []string{"alte Zeile"}

	cmd := m.startRun()
	if cmd == nil {
		t.Fatal("startRun must return a command")
	}
	if len(m.jobs) != 0 || len(m.log) != 0 {
		t.Fatalf("jobs = %v, log = %v; want both empty at the start of a run", m.jobs, m.log)
	}
	_ = cmd()

	m, _ = update(t, m, RunDoneMsg{})
	m.jobs = []job{{label: "alt"}}
	m.log = []string{"alte Zeile"}
	if cmd = m.startRun(); cmd == nil {
		t.Fatal("startRun must work again after RunDoneMsg")
	}
	if len(m.jobs) != 0 || len(m.log) != 0 {
		t.Fatalf("jobs = %v, log = %v; want both empty for the next run", m.jobs, m.log)
	}
}

func TestCheckDoneNotes(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, checkDoneMsg{results: []app.CheckResult{{Note: "Archiv leer"}}})
	if !strings.Contains(m.toast, "1 Hinweis: Archiv leer") {
		t.Errorf("toast = %q, want the singular note", m.toast)
	}

	m = newModel(Options{Config: &config.Options{}, Run: noopRun})
	m, _ = update(t, m, checkDoneMsg{results: []app.CheckResult{
		{Note: "erster Hinweis"},
		{Note: "zweiter Hinweis"},
	}})
	if !strings.Contains(m.toast, "2 Hinweise: erster Hinweis") {
		t.Errorf("toast = %q, want the plural note count and the first note", m.toast)
	}
}

func TestFormEnterBlockedWhileBusy(t *testing.T) {
	called := false
	m := newModel(Options{
		Config:      &config.Options{FileTag: "example"},
		StartScreen: true,
		Run: func(context.Context) error {
			called = true
			return nil
		},
	})
	m.fields[0].SetValue("https://www.ardplus.de/details/abc")
	m.busy = true

	m, cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || called {
		t.Fatalf("command = %v, run called = %v; want neither while busy", cmd != nil, called)
	}
	if m.startErr == nil || m.startErr.Error() != "Eine Aktion läuft noch. Bitte warten." {
		t.Fatalf("startErr = %v, want the busy hint", m.startErr)
	}
	if m.screen != ScreenStart {
		t.Errorf("screen = %v, want to stay on the start form", m.screen)
	}
}

func TestCloseCancelsContext(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newModel(Options{Config: &config.Options{}, Context: root})
	m.seasonsDone = make(chan struct{})
	m.categoriesDone = make(chan struct{})
	m.skipDone = make(chan struct{})
	seasonsDone, categoriesDone, skipDone := m.seasonsDone, m.categoriesDone, m.skipDone

	m.Close()

	select {
	case <-m.ctx.Done():
	default:
		t.Fatal("Close must cancel the model context")
	}
	for name, done := range map[string]chan struct{}{"seasons": seasonsDone, "categories": categoriesDone, "skip": skipDone} {
		select {
		case <-done:
		default:
			t.Errorf("the %s done channel was not closed", name)
		}
	}
}

func TestSeasonOffsetClampedOnResize(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.details = store.Series{ID: 1, Kind: store.KindSeries}
	m.episodes = make([]store.KnownEpisode, 30)
	for i := range m.episodes {
		m.episodes[i] = store.KnownEpisode{EpisodeRef: store.EpisodeRef{ID: strconv.Itoa(i + 1), Season: i + 1, Number: 1}}
	}
	m.rebuildDetailSeasons()
	m.detailSeasonCursor = 29
	m.detailOffset = 0
	m.screen = ScreenDetails

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	if m.detailVisible() < 9 {
		t.Fatalf("detailVisible() = %d, want the free height", m.detailVisible())
	}
	if m.detailOffset != 30-m.detailVisible() {
		t.Fatalf("detailOffset = %d, want %d after resize", m.detailOffset, 30-m.detailVisible())
	}
	assertVisible(t, "seasons", m.detailSeasonCursor, m.detailOffset, m.detailVisible())

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	if m.detailOffset != 30-m.detailVisible() {
		t.Fatalf("detailOffset = %d, want %d at height 12", m.detailOffset, 30-m.detailVisible())
	}
	assertVisible(t, "seasons", m.detailSeasonCursor, m.detailOffset, m.detailVisible())
}

// Init starts the run and marks the model as running. The flag must survive:
// the model is a pointer, so the change is not thrown away with a copy.
func TestInitStartsTheRunAndMarksTheModelRunning(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun, AutoStart: true})
	if m.screen != ScreenLoading {
		t.Fatalf("screen = %v, want ScreenLoading", m.screen)
	}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must start the run")
	}
	if !m.running {
		t.Error("Init must mark the run as running")
	}
}

// A second run must not start while the first one is still going.
func TestStartRunIsBlockedWhileTheRunIsGoing(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun, AutoStart: true})
	m.Init()
	if cmd := m.startRun(); cmd != nil {
		t.Error("startRun must not start a second run")
	}
}

// Pressing enter without a selection must tell the user what is missing and
// keep the screen open.
func TestEnterWithoutSelectionShowsAHint(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	seasons := []app.Season{{Number: 1, Title: "Staffel 1", ID: "s1"}}
	m, reply := seasonsRequest(t, m, seasons)
	m.selection = map[int]bool{}

	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.selectionErr == "" {
		t.Error("the model must report that nothing is selected")
	}
	if m.screen != ScreenSeries {
		t.Errorf("screen = %v, want the season screen to stay open", m.screen)
	}
	select {
	case selected := <-reply:
		t.Errorf("the request must not be answered yet, got %v", selected)
	default:
	}
	if !strings.Contains(m.View(), "mindestens eine Staffel") {
		t.Errorf("the hint must be visible:\n%s", m.View())
	}
}

// The library check must call the check of the application and report the
// result.
func TestCheckLibraryCallsTheApplication(t *testing.T) {
	var called int
	check := func(context.Context) ([]app.CheckResult, error) {
		called++
		return []app.CheckResult{
			{Series: store.Series{ID: 1, Title: "Serie A"}, New: []store.EpisodeRef{{ID: "e1"}}},
		}, nil
	}
	m := newModel(Options{Config: &config.Options{}, Run: noopRun, Check: check})

	cmd := m.checkLibrary()
	if cmd == nil {
		t.Fatal("checkLibrary must start the check")
	}
	if !m.checking {
		t.Error("the model must show that the check is running")
	}
	m = runCommands(t, m, cmd)
	if called != 1 {
		t.Errorf("the check was called %d times, want 1", called)
	}
	if m.checking {
		t.Error("the check must be finished")
	}
	if !strings.Contains(m.toast, "1 neue Folge(n)") {
		t.Errorf("toast = %q, want the result of the check", m.toast)
	}
}

// Without a check function the model does nothing.
func TestCheckLibraryWithoutACheckFunction(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	if cmd := m.checkLibrary(); cmd != nil {
		t.Error("checkLibrary must do nothing without a check function")
	}
}
