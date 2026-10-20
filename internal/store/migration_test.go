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
