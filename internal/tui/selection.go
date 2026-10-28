// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/store"
)

func (m *Model) toggleSeason(index int) {
	if index < 0 || index >= len(m.seasons) {
		return
	}
	number := m.seasons[index].Number
	m.selection[number] = !m.selection[number]
	m.selectionErr = ""
}

func (m *Model) selectedCategories() []string {
	var slugs []string
	for _, season := range m.seasons {
		if m.selection[season.Number] {
			slugs = append(slugs, season.ID)
		}
	}
	return slugs
}

func (m *Model) selectedSeasons() []int {
	var selected []int
	for _, season := range m.seasons {
		if m.selection[season.Number] {
			selected = append(selected, season.Number)
		}
	}
	sort.Ints(selected)
	return selected
}

func (m *Model) applyStartInput() error {
	url := strings.TrimSpace(m.fields[0].Value())
	if url != "" {
		url = media.NormalizeURL(url)
		if err := config.ValidateContentURL(url); err != nil {
			return fmt.Errorf("Invalid URL: %s", err)
		}
	}
	m.cfg.URL = url
	switch m.formPurpose {
	case formAddURL:
		if url == "" {
			return errors.New("Bitte eine URL eingeben.")
		}
	case formStart:
		if url == "" && len(m.library) == 0 && !m.hasCatalog() {
			return config.ErrNoURL
		}
	}

	m.cfg.Username = strings.TrimSpace(m.fields[1].Value())
	m.cfg.Password = m.fields[2].Value()

	if m.needsLogin && (m.cfg.Username == "" || m.cfg.Password == "") {
		return fmt.Errorf("Credentials missing! Set ARD_PLUS_USER/ARD_PLUS_PASSWORD or enter them above.")
	}
	return nil
}

// persistStart keeps what the user typed, so the next start has it.
func (m *Model) persistStart() {
	if m.store != nil {
		_ = m.store.SetSetting(store.SettingUsername, m.cfg.Username)
		_ = m.store.SetSetting(store.SettingLastURL, m.cfg.URL)
	}
	if m.secrets != nil && m.cfg.Password != "" {
		_ = m.secrets.Set(m.cfg.Password)
	}
}

func entryTitle(entry store.Series) string {
	if entry.Title != "" {
		return entry.Title
	}
	return entry.URL
}
