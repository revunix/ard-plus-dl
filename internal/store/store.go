// Package store keeps the application state in a local SQLite database:
// settings, the series library and the episode history used for new-episode
// detection.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Setting keys.
const (
	SettingUsername = "username"
	SettingFileTag  = "file_tag"
	SettingLastURL  = "last_url"
	SettingTheme    = "theme"
)

// Series kinds.
const (
	KindMovie  = "movie"
	KindSeries = "series"
	KindTatort = "tatort"
)

// Series is one entry of the library.
type Series struct {
	ID        int64
	URL       string
	Title     string
	Kind      string
	Enabled   bool
	Tag       string
	Skip      int
	AddedAt   time.Time
	CheckedAt *time.Time
	NewCount  int
	LastError string
}

// EpisodeRef identifies one episode as the API reports it.
type EpisodeRef struct {
	ID     string
	Title  string
	Season int
	Number int
}

// KnownEpisode is a stored episode reference with its history.
type KnownEpisode struct {
	EpisodeRef
	FirstSeen    time.Time
	DownloadedAt *time.Time
	// Path is the file the episode was downloaded to, empty when it was
	// never downloaded.
	Path string
}

// Store is the SQLite backed application state.
type Store struct {
	db   *sql.DB
	path string
}

const schema = `
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS series (
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
);
CREATE TABLE IF NOT EXISTS episodes (
    series_id     INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    episode_id    TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    season        INTEGER NOT NULL DEFAULT 0,
    number        INTEGER NOT NULL DEFAULT 0,
    first_seen    TEXT NOT NULL,
    downloaded_at TEXT,
    path          TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (series_id, episode_id)
);
CREATE INDEX IF NOT EXISTS episodes_open ON episodes(series_id, downloaded_at);
`

// migrations run after the schema was created. They add the columns that
// later versions introduced.
var migrations = []string{
	`ALTER TABLE episodes ADD COLUMN path TEXT NOT NULL DEFAULT ''`,
}

// Open opens (and creates) the database at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("no database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// The path is escaped: a "?" or "#" in it would otherwise cut the DSN
	// short.
	dsn := (&url.URL{
		Scheme:   "file",
		Opaque:   path,
		RawQuery: "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	for _, statement := range migrations {
		// A column that already exists is not an error.
		if _, err := db.Exec(statement); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate schema: %w", err)
		}
	}
	// The database holds the library and the username, so keep it private.
	_ = os.Chmod(path, 0o600)
	return &Store{db: db, path: path}, nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

func parseStamp(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}

// Setting returns a stored setting, or "" when it is not set.
func (s *Store) Setting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSetting stores a setting; an empty value removes it.
func (s *Store) SetSetting(key, value string) error {
	if value == "" {
		_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// UpsertSeries adds a URL to the library or updates its title and kind. The
// existing enabled flag and check history are kept.
func (s *Store) UpsertSeries(url, title, kind string) (int64, error) {
	if url == "" {
		return 0, errors.New("no URL")
	}
	_, err := s.db.Exec(`INSERT INTO series(url, title, kind, added_at) VALUES(?, ?, ?, ?)
        ON CONFLICT(url) DO UPDATE SET
            title = CASE WHEN excluded.title != '' THEN excluded.title ELSE series.title END,
            kind  = CASE WHEN excluded.kind  != '' THEN excluded.kind  ELSE series.kind  END`,
		url, title, kind, nowStamp())
	if err != nil {
		return 0, err
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM series WHERE url = ?`, url).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// SeriesList returns the library ordered by title.
func (s *Store) SeriesList() ([]Series, error) {
	rows, err := s.db.Query(`SELECT id, url, title, kind, enabled, tag, skip, added_at,
        checked_at, new_count, last_error FROM series ORDER BY title COLLATE NOCASE, url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Series
	for rows.Next() {
		var (
			entry     Series
			enabled   int
			added     string
			checkedAt sql.NullString
		)
		if err := rows.Scan(&entry.ID, &entry.URL, &entry.Title, &entry.Kind, &enabled, &entry.Tag,
			&entry.Skip, &added, &checkedAt, &entry.NewCount, &entry.LastError); err != nil {
			return nil, err
		}
		entry.Enabled = enabled != 0
		if parsed := parseStamp(added); parsed != nil {
			entry.AddedAt = *parsed
		}
		entry.CheckedAt = parseStamp(checkedAt.String)
		result = append(result, entry)
	}
	return result, rows.Err()
}

// SeriesByURL returns one library entry.
func (s *Store) SeriesByURL(url string) (*Series, error) {
	list, err := s.SeriesList()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].URL == url {
			return &list[i], nil
		}
	}
	return nil, nil
}

// RemoveSeries deletes a library entry together with its episode history.
func (s *Store) RemoveSeries(id int64) error {
	_, err := s.db.Exec(`DELETE FROM series WHERE id = ?`, id)
	return err
}

// SetSeriesEnabled toggles whether a library entry is checked and downloaded.
func (s *Store) SetSeriesEnabled(id int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.Exec(`UPDATE series SET enabled = ? WHERE id = ?`, value, id)
	return err
}

// SetSeriesTag stores the per-series Datei-Tag override.
func (s *Store) SetSeriesTag(id int64, tag string) error {
	_, err := s.db.Exec(`UPDATE series SET tag = ? WHERE id = ?`, tag, id)
	return err
}

// SetSeriesSkip stores the number of leading episodes of the first season
// that are skipped for the entry.
func (s *Store) SetSeriesSkip(id int64, skip int) error {
	_, err := s.db.Exec(`UPDATE series SET skip = ? WHERE id = ?`, skip, id)
	return err
}

// SyncEpisodes stores the episodes the API currently reports and returns the
// ones that were not known before.
func (s *Store) SyncEpisodes(seriesID int64, episodes []EpisodeRef) ([]EpisodeRef, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	known := map[string]bool{}
	rows, err := tx.Query(`SELECT episode_id FROM episodes WHERE series_id = ?`, seriesID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		known[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var fresh []EpisodeRef
	stamp := nowStamp()
	for _, episode := range episodes {
		if episode.ID == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO episodes(series_id, episode_id, title, season, number, first_seen)
            VALUES(?, ?, ?, ?, ?, ?)
            ON CONFLICT(series_id, episode_id) DO UPDATE SET
                title = excluded.title, season = excluded.season, number = excluded.number`,
			seriesID, episode.ID, episode.Title, episode.Season, episode.Number, stamp); err != nil {
			return nil, err
		}
		if !known[episode.ID] {
			fresh = append(fresh, episode)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return fresh, nil
}

// Pending returns the stored episodes that were not downloaded yet, newest
// season and episode first.
func (s *Store) Pending(seriesID int64) ([]KnownEpisode, error) {
	rows, err := s.db.Query(`SELECT episode_id, title, season, number, first_seen, downloaded_at, path
        FROM episodes WHERE series_id = ? AND downloaded_at IS NULL
        ORDER BY season DESC, number DESC`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

func scanEpisodes(rows *sql.Rows) ([]KnownEpisode, error) {
	var result []KnownEpisode
	for rows.Next() {
		var (
			episode   KnownEpisode
			firstSeen string
			downl     sql.NullString
		)
		if err := rows.Scan(&episode.ID, &episode.Title, &episode.Season, &episode.Number, &firstSeen, &downl, &episode.Path); err != nil {
			return nil, err
		}
		if parsed := parseStamp(firstSeen); parsed != nil {
			episode.FirstSeen = *parsed
		}
		episode.DownloadedAt = parseStamp(downl.String)
		result = append(result, episode)
	}
	return result, rows.Err()
}

// Episodes returns every stored episode of one entry, newest first.
func (s *Store) Episodes(seriesID int64) ([]KnownEpisode, error) {
	rows, err := s.db.Query(`SELECT episode_id, title, season, number, first_seen, downloaded_at, path
        FROM episodes WHERE series_id = ?
        ORDER BY season DESC, number DESC, first_seen DESC`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

// CountPending counts the episodes that are known but not downloaded.
func (s *Store) CountPending(seriesID int64) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM episodes WHERE series_id = ? AND downloaded_at IS NULL`, seriesID).Scan(&count)
	return count, err
}

// MarkDownloaded records that one episode was downloaded and where the file
// ended up.
func (s *Store) MarkDownloaded(seriesID int64, episodeID, path string) error {
	_, err := s.db.Exec(`UPDATE episodes SET downloaded_at = ?, path = ? WHERE series_id = ? AND episode_id = ?`,
		nowStamp(), path, seriesID, episodeID)
	return err
}

// ClearDownloaded forgets that one episode was downloaded, so it is offered
// again. The path of the file is kept for the cleanup.
func (s *Store) ClearDownloaded(seriesID int64, episodeID string) error {
	_, err := s.db.Exec(`UPDATE episodes SET downloaded_at = NULL WHERE series_id = ? AND episode_id = ?`,
		seriesID, episodeID)
	return err
}

// ClearEpisodes forgets the history of one entry, so every episode is offered
// again.
func (s *Store) ClearEpisodes(seriesID int64) error {
	_, err := s.db.Exec(`DELETE FROM episodes WHERE series_id = ?`, seriesID)
	return err
}

// RecordCheck stores the result of the last new-episode check.
func (s *Store) RecordCheck(seriesID int64, newCount int, lastError string) error {
	_, err := s.db.Exec(`UPDATE series SET checked_at = ?, new_count = ?, last_error = ? WHERE id = ?`,
		nowStamp(), newCount, lastError, seriesID)
	return err
}
