package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrationAddsThePathColumn checks that a database of an older version
// gets the column with the file path.
func TestMigrationAddsThePathColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")

	// The schema of the version without the path column.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS episodes (
		series_id     INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
		episode_id    TEXT NOT NULL,
		title         TEXT NOT NULL DEFAULT '',
		season        INTEGER NOT NULL DEFAULT 0,
		number        INTEGER NOT NULL DEFAULT 0,
		first_seen    TEXT NOT NULL,
		downloaded_at TEXT,
		PRIMARY KEY (series_id, episode_id)
	)`); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	db.Close()

	state, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer state.Close()

	rows, err := state.db.Query(`PRAGMA table_info(episodes)`)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			t.Fatalf("scan columns: %v", err)
		}
		if name == "path" {
			found = true
		}
	}
	if !found {
		t.Fatal("the migration did not add the path column")
	}
}

// TestMigrationDropsTheOldSeriesColumn checks that a library of an older
// version loses the column and the setting it no longer needs, and keeps
// its entries.
func TestMigrationDropsTheOldSeriesColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE series (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			url        TEXT NOT NULL UNIQUE,
			title      TEXT NOT NULL DEFAULT '',
			kind       TEXT NOT NULL DEFAULT '',
			enabled    INTEGER NOT NULL DEFAULT 1,
			tag        TEXT NOT NULL DEFAULT '',
			skip       INTEGER NOT NULL DEFAULT 0,
			added_at   TEXT NOT NULL,
			checked_at TEXT,
			new_count  INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO series(url, title, kind, tag, skip, added_at) VALUES('https://www.ardplus.de/details/x', 'Show', 'series', 'old', 3, '2026-01-01T00:00:00Z')`,
		`INSERT INTO settings(key, value) VALUES('file_tag', 'old'), ('username', 'alice')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create old library: %v", err)
		}
	}
	db.Close()

	// Opening twice runs the migrations on an already migrated library.
	for range 2 {
		state, err := Open(path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		entries, err := state.SeriesList()
		if err != nil || len(entries) != 1 || entries[0].Title != "Show" || entries[0].Skip != 3 {
			t.Fatalf("entries = %+v, %v", entries, err)
		}
		var columns int
		if err := state.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('series') WHERE name = 'tag'`).Scan(&columns); err != nil || columns != 0 {
			t.Fatalf("tag column still there: %d, %v", columns, err)
		}
		if value, _ := state.Setting("file_tag"); value != "" {
			t.Fatalf("old setting still there: %q", value)
		}
		if value, _ := state.Setting(SettingUsername); value != "alice" {
			t.Fatalf("username lost: %q", value)
		}
		state.Close()
	}
}
