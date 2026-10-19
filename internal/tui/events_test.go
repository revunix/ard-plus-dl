package tui

import (
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
)

func TestApplyEventMatchesJobByLabel(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.applyEvent(app.Event{Kind: app.EventQueued, Label: "Folge A", Path: "/downloads/a"})
	m.applyEvent(app.Event{Kind: app.EventQueued, Label: "Folge B", Path: "/downloads/b"})

	m.applyEvent(app.Event{Kind: app.EventProgress, Label: "Folge A", Done: 3, Total: 10})
	m.applyEvent(app.Event{Kind: app.EventDone, Label: "Folge A", Path: "/downloads/a.mp4"})

	if len(m.jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(m.jobs))
	}
	first := m.jobs[0]
	if first.status != "fertig" || first.done != 3 || first.total != 10 || first.path != "/downloads/a.mp4" {
		t.Errorf("job A = %+v, want a finished job with progress and final path", first)
	}
	if second := m.jobs[1]; second.status != "wartet" || second.done != 0 || second.path != "/downloads/b" {
		t.Errorf("job B = %+v, want an untouched queued job", second)
	}
}

func TestApplyEventIgnoresLateProgressAfterDone(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.applyEvent(app.Event{Kind: app.EventQueued, Label: "Folge A"})
	m.applyEvent(app.Event{Kind: app.EventProgress, Label: "Folge A", Done: 4, Total: 8})
	m.applyEvent(app.Event{Kind: app.EventDone, Label: "Folge A", Path: "/downloads/a.mp4"})

	m.applyEvent(app.Event{Kind: app.EventProgress, Label: "Folge A", Done: 99, Total: 100})

	if job := m.jobs[0]; job.status != "fertig" || job.done != 4 || job.total != 8 {
		t.Errorf("job = %+v, want the terminal state unchanged by a late progress event", job)
	}
}

func TestApplyEventLogsFailedWithoutLabel(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})
	m.applyEvent(app.Event{Kind: app.EventQueued, Label: "Folge A"})

	m.applyEvent(app.Event{Kind: app.EventFailed, Message: "boom"})

	if job := m.jobs[0]; job.status != "wartet" {
		t.Errorf("job = %+v, want the unrelated job untouched", job)
	}
	logs := strings.Join(m.log, "\n")
	if !strings.Contains(logs, "FEHLER: boom") {
		t.Errorf("log = %q, want the FEHLER line", logs)
	}
}

func TestApplyEventSkippedLogs(t *testing.T) {
	m := newModel(Options{Config: &config.Options{}, Run: noopRun})

	m.applyEvent(app.Event{Kind: app.EventSkipped, Label: "Folge A"})
	m.applyEvent(app.Event{Kind: app.EventSkipped, Message: "ohne Label"})

	logs := strings.Join(m.log, "\n")
	if !strings.Contains(logs, "Übersprungen: Folge A") {
		t.Errorf("log = %q, want the skip line with the label", logs)
	}
	if !strings.Contains(logs, "ohne Label") {
		t.Errorf("log = %q, want the plain skip message", logs)
	}
}
