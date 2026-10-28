// Package download implements the staging pipeline: skip checks, legacy
// artifact cleanup and renaming finished downloads into the final names.
package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Item describes one download target.
type Item struct {
	// StagingPath is the relative path (without extension) the downloader
	// writes to, as returned by the media path builders.
	StagingPath string
	// FinalGlob is the relative glob of already finished outputs.
	FinalGlob string
	// BaseStem is the final file name stem, e.g. "My.Show.S01E01".
	BaseStem string
	// DirStem is the final directory stem, e.g. "My.Show.S01" (empty for
	// Tatort, which lives in the downloads root).
	DirStem string
	// DirUsesTag appends the media tag to a newly created directory.
	DirUsesTag bool
}

// Manager performs the skip, cleanup and finalize steps.
type Manager struct {
	DownloadsDir string
	Force        bool
	// Probe reads the media information of a finished file.
	Probe func(ctx context.Context, path string) (string, error)
	Logf  func(format string, args ...any)
}

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// StagingPath returns the absolute staging path without extension.
func (m *Manager) StagingPath(item Item) string {
	return filepath.Join(m.DownloadsDir, filepath.FromSlash(item.StagingPath))
}

// WorkDir returns the directory used for the DASH segments of an item.
func (m *Manager) WorkDir(item Item) string {
	return m.StagingPath(item) + ".dash"
}

// legacyFragmentPattern matches the yt-dlp fragment suffix after the staging
// base name, e.g. ".f137" in "<base>.f137.mp4".
var legacyFragmentPattern = regexp.MustCompile(`^\.f[0-9]+`)

// CleanupLegacy removes yt-dlp bookkeeping leftovers of previous versions.
// The DASH segment files are kept: a segment that was downloaded completely
// is not fetched again, so an interrupted download continues where it
// stopped. A half written segment (the "*.part" file) is not resumable and
// is therefore fetched again from the start.
func (m *Manager) CleanupLegacy(stagingPath string) {
	dir := filepath.Dir(stagingPath)
	base := filepath.Base(stagingPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		full := filepath.Join(dir, name)
		rest := strings.TrimPrefix(name, base)
		switch {
		case strings.HasSuffix(name, ".ytdl"):
			m.logf("Removing yt-dlp metadata file: %s", full)
			_ = os.Remove(full)
		case legacyFragmentPattern.MatchString(rest):
			m.logf("Removing yt-dlp fragment: %s", full)
			_ = os.Remove(full)
		}
	}
}

// Prepare decides whether an item must be downloaded. It returns true when a
// finished output already exists (the item is skipped).
func (m *Manager) Prepare(item Item) (bool, error) {
	stagingPath := m.StagingPath(item)

	if !m.Force {
		if m.hasOutput(item.FinalGlob) {
			m.logf("SKIP (already exists): %s", m.absGlob(item.FinalGlob))
			return true, nil
		}
		m.CleanupLegacy(stagingPath)
		if info, err := os.Stat(stagingPath + ".mp4"); err == nil && info.Size() > 0 {
			m.logf("Incomplete file, re-downloading: %s", stagingPath+".mp4")
		}
		return false, nil
	}

	for _, match := range m.glob(item.FinalGlob) {
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() {
			m.logf("Force re-download, removing: %s", match)
			_ = os.Remove(match)
		}
	}
	// The staging file itself is a previous partial download, like the
	// leftovers below it.
	if info, err := os.Stat(stagingPath + ".mp4"); err == nil && info.Mode().IsRegular() {
		m.logf("Force re-download, removing: %s", stagingPath+".mp4")
		_ = os.Remove(stagingPath + ".mp4")
	}
	_ = os.RemoveAll(m.WorkDir(item))
	for _, match := range m.glob(stagingPath + "*.part*") {
		_ = os.Remove(match)
	}
	m.CleanupLegacy(stagingPath)
	return false, nil
}

func (m *Manager) absGlob(pattern string) string {
	if pattern == "" {
		return ""
	}
	return filepath.Join(m.DownloadsDir, filepath.FromSlash(pattern))
}

func (m *Manager) glob(pattern string) []string {
	if pattern == "" {
		return nil
	}
	if !filepath.IsAbs(pattern) {
		pattern = m.absGlob(pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

// hasOutput reports whether the glob matches a regular, non-empty file.
func (m *Manager) hasOutput(pattern string) bool {
	for _, match := range m.glob(pattern) {
		info, err := os.Stat(match)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

// Finalize probes the finished download and moves it to its final name. When
// ffprobe reports no usable streams the file keeps the staging name.
func (m *Manager) Finalize(ctx context.Context, item Item, stagingFile string) (string, error) {
	info, err := os.Stat(stagingFile)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Download finished without a file: %s", m.StagingPath(item))
	}
	ext := filepath.Ext(stagingFile)

	if m.Probe == nil {
		return "", errors.New("no media probe configured")
	}
	tag, probeErr := m.Probe(ctx, stagingFile)
	if probeErr != nil {
		m.logf("WARNUNG: Medieninformationen nicht ermittelbar, Datei behaelt den bisherigen Namen: %s", stagingFile)
		_ = os.RemoveAll(m.WorkDir(item))
		return stagingFile, nil
	}

	dir := ""
	if item.DirStem != "" {
		if item.DirUsesTag {
			dir = m.firstOutputDir(item.DirStem)
			if dir == "" {
				dir = item.DirStem + "." + tag
			}
		} else {
			dir = item.DirStem
		}
	}

	var target string
	if dir != "" {
		target = filepath.Join(m.DownloadsDir, dir, item.BaseStem+"."+tag+ext)
	} else {
		target = filepath.Join(m.DownloadsDir, item.BaseStem+"."+tag+ext)
	}
	if target == stagingFile {
		_ = os.RemoveAll(m.WorkDir(item))
		return target, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(stagingFile, target); err != nil {
		return "", err
	}
	m.logf("Benannt: %s", target)

	// The work directory with the downloaded segments must be gone before the
	// empty staging directories are removed: it is a child of them, and a
	// directory that still has content cannot be removed.
	_ = os.RemoveAll(m.WorkDir(item))

	// Remove the now empty staging directories, from the deepest one up to
	// the downloads directory.
	stale := filepath.Dir(stagingFile)
	for stale != m.DownloadsDir {
		rel, err := filepath.Rel(m.DownloadsDir, stale)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			break
		}
		if err := os.Remove(stale); err != nil {
			break
		}
		stale = filepath.Dir(stale)
	}
	return target, nil
}

// firstOutputDir returns an existing directory matching "<stem>.*".
func (m *Manager) firstOutputDir(stem string) string {
	for _, match := range m.glob(stem + ".*") {
		info, err := os.Stat(match)
		if err == nil && info.IsDir() {
			return filepath.Base(match)
		}
	}
	return ""
}
