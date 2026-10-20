package tui

import "github.com/revunix/ard-plus-dl/internal/app"

// applyEvent folds one app event into the queue and the log.
func (m *Model) applyEvent(event app.Event) {
	switch event.Kind {
	case app.EventLog:
		m.appendLog(event.Message)
	case app.EventQueued:
		m.jobs = append(m.jobs, job{label: event.Label, path: event.Path, status: "wartet"})
	case app.EventProgress:
		if index := m.jobIndex(event.Label); index >= 0 && !terminalJobStatus(m.jobs[index].status) {
			current := &m.jobs[index]
			current.done = event.Done
			current.total = event.Total
			current.status = "lädt"
		}
	case app.EventDone:
		if index := m.jobIndex(event.Label); index >= 0 && !terminalJobStatus(m.jobs[index].status) {
			current := &m.jobs[index]
			current.status = "fertig"
			if event.Path != "" {
				current.path = event.Path
			}
		}
	case app.EventFailed:
		if index := m.jobIndex(event.Label); index >= 0 && !terminalJobStatus(m.jobs[index].status) {
			m.jobs[index].status = "fehlgeschlagen"
		} else if event.Message != "" {
			m.appendLog("FEHLER: " + event.Message)
		}
	case app.EventSkipped:
		if event.Label != "" {
			m.appendLog("Übersprungen: " + event.Label)
		} else {
			m.appendLog(event.Message)
		}
	}
}

// jobIndex resolves the job an event belongs to: the last job with a matching
// label, or -1 when the event carries no label or none matches.
func (m *Model) jobIndex(label string) int {
	if label == "" {
		return -1
	}
	for index := len(m.jobs) - 1; index >= 0; index-- {
		if m.jobs[index].label == label {
			return index
		}
	}
	return -1
}

// terminalJobStatus reports whether a job has already reached a final state.
func terminalJobStatus(status string) bool {
	return status == "fertig" || status == "fehlgeschlagen"
}

func (m *Model) appendLog(line string) {
	if line == "" {
		return
	}
	m.log = append(m.log, line)
	if len(m.log) > 200 {
		m.log = m.log[len(m.log)-200:]
	}
}
