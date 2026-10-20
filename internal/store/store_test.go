package store

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// newStore returns an empty store backed by a fresh temporary database.
func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// addSeries inserts one library entry and returns its id.
func addSeries(t *testing.T, s *Store, url string) int64 {
	t.Helper()
	id, err := s.UpsertSeries(url, "Show", KindSeries)
	if err != nil {
		t.Fatalf("UpsertSeries(%q): %v", url, err)
	}
	return id
}

// countRows runs a COUNT(*) query directly against the database.
func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return count
}

func TestOpen(t *testing.T) {
	t.Run("creates a private database file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "library.db")
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", path, err)
		}
		t.Cleanup(func() { _ = s.Close() })

		if got := s.Path(); got != path {
			t.Errorf("Path() = %q, want %q", got, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("database mode = %o, want 600", got)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat %q: %v", filepath.Dir(path), err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Errorf("database directory mode = %o, want 700", got)
		}
	})

	t.Run("rejects an empty path", func(t *testing.T) {
		if _, err := Open(""); err == nil {
			t.Fatal(`Open("") = nil error, want error`)
		}
	})

	t.Run("reopening is idempotent and keeps data", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "library.db")
		first, err := Open(path)
		if err != nil {
			t.Fatalf("first Open: %v", err)
		}
		defer first.Close()

		// A second handle on the same file must survive the schema run.
		second, err := Open(path)
		if err != nil {
			t.Fatalf("second Open: %v", err)
		}
		defer second.Close()

		if err := first.SetSetting(SettingUsername, "alice"); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
		if got, err := second.Setting(SettingUsername); err != nil || got != "alice" {
			t.Fatalf("Setting after reopen = %q, %v; want %q, nil", got, err, "alice")
		}

		if err := first.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		third, err := Open(path)
		if err != nil {
			t.Fatalf("third Open: %v", err)
		}
		defer third.Close()
		if got, err := third.Setting(SettingUsername); err != nil || got != "alice" {
			t.Fatalf("Setting after reopen = %q, %v; want %q, nil", got, err, "alice")
		}
	})
}

func TestCloseNilStore(t *testing.T) {
	var s *Store
	if err := s.Close(); err != nil {
		t.Errorf("Close on nil store = %v, want nil", err)
	}
}

func TestSettings(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		s := newStore(t)
		values := map[string]string{
			SettingUsername: "alice",
			SettingFileTag:  "my.tag",
			SettingLastURL:  "https://www.ardplus.de/details/x",
		}
		for key, value := range values {
			if err := s.SetSetting(key, value); err != nil {
				t.Fatalf("SetSetting(%q, %q): %v", key, value, err)
			}
		}
		for key, value := range values {
			got, err := s.Setting(key)
			if err != nil || got != value {
				t.Errorf("Setting(%q) = %q, %v; want %q, nil", key, got, err, value)
			}
		}
	})

	t.Run("overwrites an existing value", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetSetting(SettingFileTag, "one"); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
		if err := s.SetSetting(SettingFileTag, "two"); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
		got, err := s.Setting(SettingFileTag)
		if err != nil || got != "two" {
			t.Fatalf("Setting = %q, %v; want %q, nil", got, err, "two")
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM settings WHERE key = ?`, SettingFileTag); n != 1 {
			t.Errorf("settings rows = %d, want 1", n)
		}
	})

	t.Run("empty value deletes the setting", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetSetting(SettingUsername, "alice"); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
		if err := s.SetSetting(SettingUsername, ""); err != nil {
			t.Fatalf("SetSetting(empty): %v", err)
		}
		got, err := s.Setting(SettingUsername)
		if err != nil || got != "" {
			t.Fatalf("Setting = %q, %v; want empty, nil", got, err)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM settings WHERE key = ?`, SettingUsername); n != 0 {
			t.Errorf("settings rows = %d, want 0", n)
		}
	})

	t.Run("unknown key returns empty", func(t *testing.T) {
		s := newStore(t)
		got, err := s.Setting("does-not-exist")
		if err != nil || got != "" {
			t.Fatalf("Setting(unknown) = %q, %v; want empty, nil", got, err)
		}
	})
}

func TestUpsertSeries(t *testing.T) {
	t.Run("new URL gets an id and defaults", func(t *testing.T) {
		s := newStore(t)
		const url = "https://www.ardplus.de/details/foo"
		id, err := s.UpsertSeries(url, "Foo", KindMovie)
		if err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
		if id <= 0 {
			t.Fatalf("id = %d, want > 0", id)
		}
		entry, err := s.SeriesByURL(url)
		if err != nil {
			t.Fatalf("SeriesByURL: %v", err)
		}
		if entry == nil {
			t.Fatal("SeriesByURL = nil, want entry")
		}
		if entry.ID != id || entry.Title != "Foo" || entry.Kind != KindMovie {
			t.Errorf("entry = %+v, want id %d title Foo kind %s", entry, id, KindMovie)
		}
		if !entry.Enabled {
			t.Error("Enabled = false, want true by default")
		}
		if entry.Tag != "" || entry.Skip != 0 {
			t.Errorf("Tag = %q, Skip = %d; want empty and 0", entry.Tag, entry.Skip)
		}
		if entry.AddedAt.IsZero() || time.Since(entry.AddedAt) > time.Minute {
			t.Errorf("AddedAt = %v, want a fresh timestamp", entry.AddedAt)
		}
	})

	t.Run("second call updates title and kind but keeps state", func(t *testing.T) {
		s := newStore(t)
		const url = "https://www.ardplus.de/details/foo"
		id, err := s.UpsertSeries(url, "Alt", KindMovie)
		if err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
		if err := s.SetSeriesEnabled(id, false); err != nil {
			t.Fatalf("SetSeriesEnabled: %v", err)
		}
		if err := s.SetSeriesTag(id, "TAG1"); err != nil {
			t.Fatalf("SetSeriesTag: %v", err)
		}
		if err := s.RecordCheck(id, 4, "letzter Fehler"); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}

		again, err := s.UpsertSeries(url, "Neu", KindSeries)
		if err != nil {
			t.Fatalf("second UpsertSeries: %v", err)
		}
		if again != id {
			t.Errorf("second id = %d, want %d", again, id)
		}
		entry, err := s.SeriesByURL(url)
		if err != nil || entry == nil {
			t.Fatalf("SeriesByURL = %+v, %v", entry, err)
		}
		if entry.Title != "Neu" || entry.Kind != KindSeries {
			t.Errorf("Title/Kind = %q/%q, want Neu/%s", entry.Title, entry.Kind, KindSeries)
		}
		if entry.Enabled {
			t.Error("Enabled = true, want false from before")
		}
		if entry.Tag != "TAG1" {
			t.Errorf("Tag = %q, want TAG1", entry.Tag)
		}
		if entry.NewCount != 4 || entry.LastError != "letzter Fehler" || entry.CheckedAt == nil {
			t.Errorf("check history = (%d, %q, %v), want (4, letzter Fehler, set)", entry.NewCount, entry.LastError, entry.CheckedAt)
		}
	})

	t.Run("empty title and kind keep the existing values", func(t *testing.T) {
		s := newStore(t)
		const url = "https://www.ardplus.de/details/foo"
		id, err := s.UpsertSeries(url, "Alt", KindSeries)
		if err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
		if _, err := s.UpsertSeries(url, "", ""); err != nil {
			t.Fatalf("second UpsertSeries: %v", err)
		}
		entry, err := s.SeriesByURL(url)
		if err != nil || entry == nil {
			t.Fatalf("SeriesByURL = %+v, %v", entry, err)
		}
		if entry.ID != id || entry.Title != "Alt" || entry.Kind != KindSeries {
			t.Errorf("entry = %+v, want id %d title Alt kind %s", entry, id, KindSeries)
		}
	})

	t.Run("URL is unique", func(t *testing.T) {
		s := newStore(t)
		const url = "https://www.ardplus.de/details/foo"
		first, err := s.UpsertSeries(url, "Foo", KindMovie)
		if err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
		second, err := s.UpsertSeries(url, "Foo", KindMovie)
		if err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
		if first != second {
			t.Errorf("ids = %d and %d, want the same", first, second)
		}
		list, err := s.SeriesList()
		if err != nil {
			t.Fatalf("SeriesList: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("SeriesList has %d entries, want 1", len(list))
		}
	})

	t.Run("empty URL errors", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.UpsertSeries("", "Foo", KindMovie); err == nil {
			t.Fatal("UpsertSeries(\"\") = nil error, want error")
		}
	})
}

func TestSeriesList(t *testing.T) {
	t.Run("ordered by title case-insensitively", func(t *testing.T) {
		s := newStore(t)
		for _, entry := range []struct{ url, title string }{
			{"https://www.ardplus.de/details/zebra", "zebra"},
			{"https://www.ardplus.de/details/apple", "Apple"},
			{"https://www.ardplus.de/details/banana", "banana"},
		} {
			if _, err := s.UpsertSeries(entry.url, entry.title, KindSeries); err != nil {
				t.Fatalf("UpsertSeries(%q): %v", entry.title, err)
			}
		}
		list, err := s.SeriesList()
		if err != nil {
			t.Fatalf("SeriesList: %v", err)
		}
		titles := make([]string, 0, len(list))
		for _, entry := range list {
			titles = append(titles, entry.Title)
		}
		if want := []string{"Apple", "banana", "zebra"}; !slices.Equal(titles, want) {
			t.Errorf("titles = %v, want %v", titles, want)
		}
	})

	t.Run("equal titles fall back to the URL", func(t *testing.T) {
		s := newStore(t)
		for _, url := range []string{
			"https://www.ardplus.de/details/b",
			"https://www.ardplus.de/details/a",
		} {
			if _, err := s.UpsertSeries(url, "Same", KindSeries); err != nil {
				t.Fatalf("UpsertSeries: %v", err)
			}
		}
		list, err := s.SeriesList()
		if err != nil {
			t.Fatalf("SeriesList: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("SeriesList has %d entries, want 2", len(list))
		}
		if list[0].URL != "https://www.ardplus.de/details/a" || list[1].URL != "https://www.ardplus.de/details/b" {
			t.Errorf("URLs = %q, %q; want .../a, .../b", list[0].URL, list[1].URL)
		}
	})

	t.Run("exposes check state and nil CheckedAt", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		list, err := s.SeriesList()
		if err != nil {
			t.Fatalf("SeriesList: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("SeriesList has %d entries, want 1", len(list))
		}
		if list[0].CheckedAt != nil {
			t.Errorf("CheckedAt = %v, want nil", list[0].CheckedAt)
		}
		if list[0].NewCount != 0 || list[0].LastError != "" {
			t.Errorf("NewCount/LastError = %d/%q, want 0/empty", list[0].NewCount, list[0].LastError)
		}

		before := time.Now().Add(-time.Second)
		if err := s.RecordCheck(id, 7, "kaputt"); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
		list, err = s.SeriesList()
		if err != nil {
			t.Fatalf("SeriesList: %v", err)
		}
		entry := list[0]
		if entry.CheckedAt == nil {
			t.Fatal("CheckedAt = nil, want a timestamp")
		}
		if entry.CheckedAt.Before(before) {
			t.Errorf("CheckedAt = %v, want after %v", entry.CheckedAt, before)
		}
		if entry.NewCount != 7 || entry.LastError != "kaputt" {
			t.Errorf("NewCount/LastError = %d/%q, want 7/kaputt", entry.NewCount, entry.LastError)
		}
	})
}

func TestSeriesByURL(t *testing.T) {
	s := newStore(t)
	const url = "https://www.ardplus.de/details/show"
	id := addSeries(t, s, url)
	if err := s.SetSeriesTag(id, "MEIN.TAG"); err != nil {
		t.Fatalf("SetSeriesTag: %v", err)
	}

	t.Run("hit", func(t *testing.T) {
		entry, err := s.SeriesByURL(url)
		if err != nil || entry == nil {
			t.Fatalf("SeriesByURL = %+v, %v; want entry, nil", entry, err)
		}
		if entry.ID != id || entry.Tag != "MEIN.TAG" || entry.Kind != KindSeries {
			t.Errorf("entry = %+v, want id %d tag MEIN.TAG kind %s", entry, id, KindSeries)
		}
	})

	t.Run("miss", func(t *testing.T) {
		entry, err := s.SeriesByURL("https://www.ardplus.de/details/missing")
		if err != nil {
			t.Fatalf("SeriesByURL: %v", err)
		}
		if entry != nil {
			t.Errorf("SeriesByURL = %+v, want nil", entry)
		}
	})
}

func TestSeriesMutations(t *testing.T) {
	t.Run("SetSeriesEnabled toggles the flag", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		if err := s.SetSeriesEnabled(id, false); err != nil {
			t.Fatalf("SetSeriesEnabled(false): %v", err)
		}
		if got := enabledOf(t, s, id); got {
			t.Error("Enabled = true, want false")
		}
		if err := s.SetSeriesEnabled(id, true); err != nil {
			t.Fatalf("SetSeriesEnabled(true): %v", err)
		}
		if got := enabledOf(t, s, id); !got {
			t.Error("Enabled = false, want true")
		}
	})

	t.Run("SetSeriesTag stores the tag", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		if err := s.SetSeriesTag(id, "SERIE.TAG"); err != nil {
			t.Fatalf("SetSeriesTag: %v", err)
		}
		entry, err := s.SeriesByURL("https://www.ardplus.de/details/show")
		if err != nil || entry == nil {
			t.Fatalf("SeriesByURL = %+v, %v", entry, err)
		}
		if entry.Tag != "SERIE.TAG" {
			t.Errorf("Tag = %q, want SERIE.TAG", entry.Tag)
		}
	})

	t.Run("RecordCheck stores and clears the result", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		if err := s.RecordCheck(id, 3, "boom"); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
		entry, err := s.SeriesByURL("https://www.ardplus.de/details/show")
		if err != nil || entry == nil {
			t.Fatalf("SeriesByURL = %+v, %v", entry, err)
		}
		if entry.NewCount != 3 || entry.LastError != "boom" || entry.CheckedAt == nil {
			t.Errorf("check = (%d, %q, %v), want (3, boom, set)", entry.NewCount, entry.LastError, entry.CheckedAt)
		}
		if err := s.RecordCheck(id, 0, ""); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
		entry, err = s.SeriesByURL("https://www.ardplus.de/details/show")
		if err != nil || entry == nil {
			t.Fatalf("SeriesByURL = %+v, %v", entry, err)
		}
		if entry.NewCount != 0 || entry.LastError != "" || entry.CheckedAt == nil {
			t.Errorf("check = (%d, %q, %v), want (0, empty, set)", entry.NewCount, entry.LastError, entry.CheckedAt)
		}
	})
}

// enabledOf reads the enabled flag of one library entry.
func enabledOf(t *testing.T, s *Store, id int64) bool {
	t.Helper()
	list, err := s.SeriesList()
	if err != nil {
		t.Fatalf("SeriesList: %v", err)
	}
	for _, entry := range list {
		if entry.ID == id {
			return entry.Enabled
		}
	}
	t.Fatalf("series %d not found", id)
	return false
}

// episodeIDs extracts the ids of a slice of episode references.
func episodeIDs(refs []EpisodeRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// seedSeriesWithEpisodes stores one series with four episodes, two per season.
func seedSeriesWithEpisodes(t *testing.T) (*Store, int64) {
	t.Helper()
	s := newStore(t)
	id := addSeries(t, s, "https://www.ardplus.de/details/show")
	if _, err := s.SyncEpisodes(id, []EpisodeRef{
		{ID: "s1e1", Title: "Folge 1", Season: 1, Number: 1},
		{ID: "s1e2", Title: "Folge 2", Season: 1, Number: 2},
		{ID: "s2e1", Title: "Folge 3", Season: 2, Number: 1},
		{ID: "s2e3", Title: "Folge 4", Season: 2, Number: 3},
	}); err != nil {
		t.Fatalf("SyncEpisodes: %v", err)
	}
	return s, id
}

// knownEpisodeIDs extracts the ids of stored episodes.
func knownEpisodeIDs(episodes []KnownEpisode) []string {
	ids := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		ids = append(ids, episode.ID)
	}
	return ids
}

// pendingIDs returns the ids of the pending episodes in query order.
func pendingIDs(t *testing.T, s *Store, seriesID int64) []string {
	t.Helper()
	pending, err := s.Pending(seriesID)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	ids := make([]string, 0, len(pending))
	for _, episode := range pending {
		if episode.DownloadedAt != nil {
			t.Errorf("pending episode %s has DownloadedAt %v", episode.ID, episode.DownloadedAt)
		}
		ids = append(ids, episode.ID)
	}
	return ids
}

func TestSyncEpisodes(t *testing.T) {
	t.Run("first call returns all, second returns none", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		list := []EpisodeRef{
			{ID: "e1", Title: "Folge 1", Season: 1, Number: 1},
			{ID: "e2", Title: "Folge 2", Season: 1, Number: 2},
			{ID: "e3", Title: "Folge 3", Season: 2, Number: 1},
		}
		fresh, err := s.SyncEpisodes(id, list)
		if err != nil {
			t.Fatalf("first SyncEpisodes: %v", err)
		}
		if got, want := episodeIDs(fresh), []string{"e1", "e2", "e3"}; !slices.Equal(got, want) {
			t.Errorf("fresh = %v, want %v", got, want)
		}
		again, err := s.SyncEpisodes(id, list)
		if err != nil {
			t.Fatalf("second SyncEpisodes: %v", err)
		}
		if len(again) != 0 {
			t.Errorf("second sync returned %v, want none", episodeIDs(again))
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes WHERE series_id = ?`, id); n != 3 {
			t.Errorf("episode rows = %d, want 3", n)
		}
	})

	t.Run("one new id returns exactly that episode", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		known := []EpisodeRef{
			{ID: "e1", Title: "Folge 1", Season: 1, Number: 1},
			{ID: "e2", Title: "Folge 2", Season: 1, Number: 2},
		}
		if _, err := s.SyncEpisodes(id, known); err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		newcomer := EpisodeRef{ID: "e3", Title: "Folge 3", Season: 1, Number: 3}
		fresh, err := s.SyncEpisodes(id, append(known, newcomer))
		if err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		if len(fresh) != 1 || fresh[0] != newcomer {
			t.Errorf("fresh = %+v, want only %+v", fresh, newcomer)
		}
	})

	t.Run("updates metadata and keeps first_seen", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		if _, err := s.SyncEpisodes(id, []EpisodeRef{
			{ID: "e1", Title: "Alt", Season: 1, Number: 1},
		}); err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		const stamped = "2020-01-02T03:04:05Z"
		if _, err := s.db.Exec(`UPDATE episodes SET first_seen = ? WHERE series_id = ? AND episode_id = ?`,
			stamped, id, "e1"); err != nil {
			t.Fatalf("pin first_seen: %v", err)
		}
		if _, err := s.SyncEpisodes(id, []EpisodeRef{
			{ID: "e1", Title: "Neu", Season: 3, Number: 7},
		}); err != nil {
			t.Fatalf("second SyncEpisodes: %v", err)
		}

		pending, err := s.Pending(id)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("pending = %d entries, want 1", len(pending))
		}
		if pending[0].Title != "Neu" || pending[0].Season != 3 || pending[0].Number != 7 {
			t.Errorf("pending = %+v, want Neu/3/7", pending[0])
		}
		want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		if !pending[0].FirstSeen.Equal(want) {
			t.Errorf("FirstSeen = %v, want %v", pending[0].FirstSeen, want)
		}
		var stored string
		if err := s.db.QueryRow(`SELECT first_seen FROM episodes WHERE series_id = ? AND episode_id = ?`,
			id, "e1").Scan(&stored); err != nil {
			t.Fatalf("read first_seen: %v", err)
		}
		if stored != stamped {
			t.Errorf("stored first_seen = %q, want %q", stored, stamped)
		}
	})

	t.Run("empty ids are skipped", func(t *testing.T) {
		s := newStore(t)
		id := addSeries(t, s, "https://www.ardplus.de/details/show")
		fresh, err := s.SyncEpisodes(id, []EpisodeRef{
			{ID: "", Title: "ohne id"},
			{ID: "e1", Title: "Folge 1", Season: 1, Number: 1},
			{ID: ""},
		})
		if err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		if got, want := episodeIDs(fresh), []string{"e1"}; !slices.Equal(got, want) {
			t.Errorf("fresh = %v, want %v", got, want)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes WHERE series_id = ?`, id); n != 1 {
			t.Errorf("episode rows = %d, want 1", n)
		}
		empty, err := s.SyncEpisodes(id, []EpisodeRef{{ID: ""}})
		if err != nil {
			t.Fatalf("SyncEpisodes(only empty): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("fresh = %v, want none", episodeIDs(empty))
		}
	})

	t.Run("unknown series fails", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.SyncEpisodes(4242, []EpisodeRef{{ID: "e1", Season: 1, Number: 1}}); err == nil {
			t.Fatal("SyncEpisodes(unknown series) = nil error, want foreign key error")
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes`); n != 0 {
			t.Errorf("episode rows = %d, want 0 after rollback", n)
		}
	})
}

func TestPendingAndMarkDownloaded(t *testing.T) {
	t.Run("pending lists not-downloaded episodes newest first", func(t *testing.T) {
		s, id := seedSeriesWithEpisodes(t)
		pending, err := s.Pending(id)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if got, want := knownEpisodeIDs(pending), []string{"s2e3", "s2e1", "s1e2", "s1e1"}; !slices.Equal(got, want) {
			t.Errorf("pending = %v, want %v", got, want)
		}
		for _, episode := range pending {
			if episode.FirstSeen.IsZero() {
				t.Errorf("episode %s has zero FirstSeen", episode.ID)
			}
			if episode.DownloadedAt != nil {
				t.Errorf("episode %s has DownloadedAt %v, want nil", episode.ID, episode.DownloadedAt)
			}
		}
		if got, err := s.CountPending(id); err != nil || got != 4 {
			t.Fatalf("CountPending = %d, %v; want 4, nil", got, err)
		}
	})

	t.Run("mark downloaded removes the episode from pending", func(t *testing.T) {
		s, id := seedSeriesWithEpisodes(t)
		if err := s.MarkDownloaded(id, "s2e3"); err != nil {
			t.Fatalf("MarkDownloaded: %v", err)
		}
		if got, err := s.CountPending(id); err != nil || got != 3 {
			t.Fatalf("CountPending = %d, %v; want 3, nil", got, err)
		}
		if got, want := pendingIDs(t, s, id), []string{"s2e1", "s1e2", "s1e1"}; !slices.Equal(got, want) {
			t.Errorf("pending = %v, want %v", got, want)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes WHERE series_id = ? AND episode_id = ? AND downloaded_at IS NOT NULL`,
			id, "s2e3"); n != 1 {
			t.Errorf("downloaded rows = %d, want 1", n)
		}
	})

	t.Run("marking every episode leaves nothing pending", func(t *testing.T) {
		s, id := seedSeriesWithEpisodes(t)
		for _, episodeID := range []string{"s1e1", "s1e2", "s2e1", "s2e3"} {
			if err := s.MarkDownloaded(id, episodeID); err != nil {
				t.Fatalf("MarkDownloaded(%q): %v", episodeID, err)
			}
		}
		pending, err := s.Pending(id)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 0 {
			t.Errorf("pending = %+v, want none", pending)
		}
		if got, err := s.CountPending(id); err != nil || got != 0 {
			t.Fatalf("CountPending = %d, %v; want 0, nil", got, err)
		}
	})

	t.Run("marking an unknown episode is a no-op", func(t *testing.T) {
		s, id := seedSeriesWithEpisodes(t)
		if err := s.MarkDownloaded(id, "does-not-exist"); err != nil {
			t.Fatalf("MarkDownloaded(unknown): %v", err)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes WHERE series_id = ? AND downloaded_at IS NOT NULL`, id); n != 0 {
			t.Errorf("downloaded rows = %d, want 0", n)
		}
		if got, err := s.CountPending(id); err != nil || got != 4 {
			t.Fatalf("CountPending = %d, %v; want 4, nil", got, err)
		}
	})

	t.Run("pending is independent per series", func(t *testing.T) {
		s, id := seedSeriesWithEpisodes(t)
		other := addSeries(t, s, "https://www.ardplus.de/details/other")
		if _, err := s.SyncEpisodes(other, []EpisodeRef{{ID: "o1", Season: 1, Number: 1}}); err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		if err := s.MarkDownloaded(id, "s2e3"); err != nil {
			t.Fatalf("MarkDownloaded: %v", err)
		}
		if got, want := pendingIDs(t, s, other), []string{"o1"}; !slices.Equal(got, want) {
			t.Errorf("other pending = %v, want %v", got, want)
		}
		if got, err := s.CountPending(other); err != nil || got != 1 {
			t.Fatalf("CountPending(other) = %d, %v; want 1, nil", got, err)
		}
	})
}

func TestRemoveSeries(t *testing.T) {
	t.Run("cascades to the episodes", func(t *testing.T) {
		s, id := seedSeriesWithEpisodes(t)
		if err := s.RemoveSeries(id); err != nil {
			t.Fatalf("RemoveSeries: %v", err)
		}
		list, err := s.SeriesList()
		if err != nil {
			t.Fatalf("SeriesList: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("SeriesList has %d entries, want 0", len(list))
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes`); n != 0 {
			t.Errorf("episode rows = %d, want 0", n)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes WHERE series_id = ?`, id); n != 0 {
			t.Errorf("episode rows for removed series = %d, want 0", n)
		}
		if entry, err := s.SeriesByURL("https://www.ardplus.de/details/show"); err != nil || entry != nil {
			t.Errorf("SeriesByURL = %+v, %v; want nil, nil", entry, err)
		}
		if got, err := s.CountPending(id); err != nil || got != 0 {
			t.Fatalf("CountPending = %d, %v; want 0, nil", got, err)
		}

		// A new entry for the same URL starts with a clean history, so every
		// episode is reported as new again.
		const url = "https://www.ardplus.de/details/show"
		newID := addSeries(t, s, url)
		if newID == id {
			t.Errorf("id after re-add = %d, want a fresh id", newID)
		}
		fresh, err := s.SyncEpisodes(newID, []EpisodeRef{
			{ID: "s1e1", Season: 1, Number: 1},
			{ID: "s1e2", Season: 1, Number: 2},
			{ID: "s2e1", Season: 2, Number: 1},
			{ID: "s2e3", Season: 2, Number: 3},
		})
		if err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		if len(fresh) != 4 {
			t.Errorf("fresh = %v, want all four episodes", episodeIDs(fresh))
		}
	})

	t.Run("leaves other series untouched", func(t *testing.T) {
		s := newStore(t)
		first := addSeries(t, s, "https://www.ardplus.de/details/first")
		second := addSeries(t, s, "https://www.ardplus.de/details/second")
		if _, err := s.SyncEpisodes(first, []EpisodeRef{{ID: "f1", Season: 1, Number: 1}}); err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		if _, err := s.SyncEpisodes(second, []EpisodeRef{{ID: "s1", Season: 1, Number: 1}}); err != nil {
			t.Fatalf("SyncEpisodes: %v", err)
		}
		if err := s.RemoveSeries(first); err != nil {
			t.Fatalf("RemoveSeries: %v", err)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM episodes`); n != 1 {
			t.Errorf("episode rows = %d, want 1", n)
		}
		if got, want := pendingIDs(t, s, second), []string{"s1"}; !slices.Equal(got, want) {
			t.Errorf("pending = %v, want %v", got, want)
		}
	})

	t.Run("removing an unknown series is a no-op", func(t *testing.T) {
		s := newStore(t)
		if err := s.RemoveSeries(98765); err != nil {
			t.Fatalf("RemoveSeries(unknown): %v", err)
		}
	})
}

func TestEpisodesListsEveryEpisode(t *testing.T) {
	s, id := seedSeriesWithEpisodes(t)
	episodes, err := s.Episodes(id)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if got, want := knownEpisodeIDs(episodes), []string{"s2e3", "s2e1", "s1e2", "s1e1"}; !slices.Equal(got, want) {
		t.Errorf("episodes = %v, want %v", got, want)
	}
	for _, episode := range episodes {
		if episode.Title == "" {
			t.Errorf("episode %s has no title", episode.ID)
		}
		if episode.FirstSeen.IsZero() {
			t.Errorf("episode %s has zero FirstSeen", episode.ID)
		}
	}
}

func TestSetSeriesTagAndSkip(t *testing.T) {
	s := newStore(t)
	id := addSeries(t, s, "https://www.ardplus.de/details/show")

	if err := s.SetSeriesTag(id, "serie"); err != nil {
		t.Fatalf("SetSeriesTag: %v", err)
	}
	if err := s.SetSeriesSkip(id, 3); err != nil {
		t.Fatalf("SetSeriesSkip: %v", err)
	}
	entry, err := s.SeriesByURL("https://www.ardplus.de/details/show")
	if err != nil || entry == nil {
		t.Fatalf("SeriesByURL: %+v (%v)", entry, err)
	}
	if entry.Tag != "serie" {
		t.Errorf("tag = %q, want serie", entry.Tag)
	}
	if entry.Skip != 3 {
		t.Errorf("skip = %d, want 3", entry.Skip)
	}
}
