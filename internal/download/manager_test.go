package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/media"
)

const mediaTag = "German.AAC.1080p.Web-DL.x264"

// recorder collects the log lines a Manager emits.
type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *recorder) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

func (r *recorder) contains(sub string) bool { return strings.Contains(r.all(), sub) }

func TestPrepareSkipsFinishedOutputs(t *testing.T) {
	newItem := func() Item {
		return Item{
			StagingPath: "My Show/Season 01/My Show S01E01 - Erste",
			FinalGlob:   "My.Show.S01.*/My.Show.S01E01.*",
			BaseStem:    "My.Show.S01E01",
			DirStem:     "My.Show.S01",
			DirUsesTag:  true,
		}
	}

	t.Run("an existing output file is skipped", func(t *testing.T) {
		m, rec := newManager(t, false)
		item := newItem()
		final := filepath.Join(m.DownloadsDir, "My.Show.S01."+mediaTag+"-example", "My.Show.S01E01."+mediaTag+"-example.mp4")
		writeFile(t, final, "video", 0o644)

		skip, err := m.Prepare(item)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if !skip {
			t.Fatal("a finished output must be skipped")
		}
		if want := "SKIP (already exists): " + filepath.Join(m.DownloadsDir, filepath.FromSlash(item.FinalGlob)); !rec.contains(want) {
			t.Fatalf("log = %q, want %q", rec.all(), want)
		}
	})

	t.Run("a matching directory is not an output", func(t *testing.T) {
		m, _ := newManager(t, false)
		if err := os.MkdirAll(filepath.Join(m.DownloadsDir, "My.Show.S01.old", "My.Show.S01E01.somedir"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		skip, err := m.Prepare(newItem())
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if skip {
			t.Fatal("a directory must not count as a finished output")
		}
	})

	t.Run("an empty output file is not an output", func(t *testing.T) {
		m, _ := newManager(t, false)
		final := filepath.Join(m.DownloadsDir, "My.Show.S01.old", "My.Show.S01E01.old.mp4")
		writeFile(t, final, "", 0o644)
		skip, err := m.Prepare(newItem())
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if skip {
			t.Fatal("an empty file must not count as a finished output")
		}
	})

	t.Run("a missing output is downloaded", func(t *testing.T) {
		m, _ := newManager(t, false)
		skip, err := m.Prepare(newItem())
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if skip {
			t.Fatal("a missing output must be downloaded")
		}
	})
}

func TestPrepareCleansLegacyArtifacts(t *testing.T) {
	m, rec := newManager(t, false)
	item := Item{StagingPath: "My Show/Season 01/My Show S01E01 - Erste"}
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, staging+".mp4", "video", 0o644)
	writeFile(t, staging+".mp4.ytdl", "meta", 0o644)
	writeFile(t, staging+".f137.mp4", "fragment", 0o644)
	writeFile(t, staging+".part-Frag1", "partial", 0o644)
	if err := os.MkdirAll(staging+".dash", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	skip, err := m.Prepare(item)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if skip {
		t.Fatal("an incomplete item must be downloaded")
	}
	if _, err := os.Stat(staging + ".ytdl"); !os.IsNotExist(err) {
		t.Fatalf("*.ytdl must be removed: %v", err)
	}
	if _, err := os.Stat(staging + ".f137.mp4"); !os.IsNotExist(err) {
		t.Fatalf(".f137.mp4 must be removed: %v", err)
	}
	if _, err := os.Stat(staging + ".part-Frag1"); err != nil {
		t.Fatalf("*.part* must be kept: %v", err)
	}
	if info, err := os.Stat(staging + ".dash"); err != nil || !info.IsDir() {
		t.Fatalf(".dash directory must be kept: %v", err)
	}
	if !rec.contains("Removing yt-dlp metadata file: " + staging + ".mp4.ytdl") {
		t.Fatalf("missing yt-dlp metadata log: %q", rec.all())
	}
	if !rec.contains("Removing yt-dlp fragment: " + staging + ".f137.mp4") {
		t.Fatalf("missing yt-dlp fragment log: %q", rec.all())
	}
	if !rec.contains("Incomplete file, re-downloading: " + staging + ".mp4") {
		t.Fatalf("missing incomplete log: %q", rec.all())
	}
}

func TestPrepareForceRedownload(t *testing.T) {
	m, rec := newManager(t, true)
	item := Item{
		StagingPath: "My Show/Season 01/My Show S01E01 - Erste",
		FinalGlob:   "My.Show.S01.*/My.Show.S01E01.*",
		BaseStem:    "My.Show.S01E01",
		DirStem:     "My.Show.S01",
		DirUsesTag:  true,
	}
	final := filepath.Join(m.DownloadsDir, "My.Show.S01."+mediaTag+"-example", "My.Show.S01E01."+mediaTag+"-example.mp4")
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, final, "video", 0o644)
	writeFile(t, staging+".mp4", "video", 0o644)
	writeFile(t, staging+".part-Frag1", "partial", 0o644)
	writeFile(t, staging+".mp4.ytdl", "meta", 0o644)
	if err := os.MkdirAll(staging+".dash", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	other := filepath.Join(m.DownloadsDir, "Other", "Other.mp4")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, other, "video", 0o644)

	skip, err := m.Prepare(item)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if skip {
		t.Fatal("force redownload must never skip")
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("force redownload must remove the final output: %v", err)
	}
	if _, err := os.Stat(staging + ".mp4"); !os.IsNotExist(err) {
		t.Fatalf("force redownload must remove the staging file: %v", err)
	}
	if _, err := os.Stat(staging + ".part-Frag1"); !os.IsNotExist(err) {
		t.Fatalf("force redownload must remove partial files: %v", err)
	}
	if _, err := os.Stat(staging + ".mp4.ytdl"); !os.IsNotExist(err) {
		t.Fatalf("force redownload must remove yt-dlp metadata: %v", err)
	}
	if _, err := os.Stat(staging + ".dash"); !os.IsNotExist(err) {
		t.Fatalf("force redownload must remove the DASH directory: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated files must survive: %v", err)
	}
	if !rec.contains("Force re-download, removing: " + final) {
		t.Fatalf("missing final output log: %q", rec.all())
	}
	if !rec.contains("Force re-download, removing: " + staging + ".mp4") {
		t.Fatalf("missing staging log: %q", rec.all())
	}
}

func TestFinalizeMovesTheDownload(t *testing.T) {
	m, rec := newManager(t, false)
	item := Item{
		StagingPath: "My Show/Season 01/My Show S01E01 - Erste",
		BaseStem:    "My.Show.S01E01",
		DirStem:     "My.Show.S01",
		DirUsesTag:  true,
	}
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, staging+".mp4", "video", 0o644)

	final, err := m.Finalize(context.Background(), item, staging+".mp4")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	target := filepath.Join(m.DownloadsDir, "My.Show.S01."+mediaTag+"-example", "My.Show.S01E01."+mediaTag+"-example.mp4")
	if final != target {
		t.Fatalf("Finalize = %q, want %q", final, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	if _, err := os.Stat(staging + ".mp4"); !os.IsNotExist(err) {
		t.Fatalf("staging file must be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.DownloadsDir, "My Show", "Season 01")); !os.IsNotExist(err) {
		t.Fatalf("staging directory must be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.DownloadsDir, "My Show")); !os.IsNotExist(err) {
		t.Fatalf("staging parent directory must be gone: %v", err)
	}
	if !rec.contains("Benannt: " + target) {
		t.Fatalf("missing rename log: %q", rec.all())
	}
}

func TestFinalizeFailureKeepsStagingName(t *testing.T) {
	m, rec := newManager(t, false)
	m.Probe = func(context.Context, string) (string, error) { return "", media.ErrNoMediaInfo }
	item := Item{
		StagingPath: "My Show/Season 03/My Show S03E01 - Dritte",
		BaseStem:    "My.Show.S03E01",
		DirStem:     "My.Show.S03",
		DirUsesTag:  true,
	}
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, staging+".mp4", "video", 0o644)
	if err := os.MkdirAll(staging+".dash", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	final, err := m.Finalize(context.Background(), item, staging+".mp4")
	if err != nil {
		t.Fatalf("a failed probe must not be an error: %v", err)
	}
	if final != staging+".mp4" {
		t.Fatalf("Finalize = %q, want the staging path", final)
	}
	if _, err := os.Stat(staging + ".mp4"); err != nil {
		t.Fatalf("the file must keep its staging name: %v", err)
	}
	if _, err := os.Stat(staging + ".dash"); !os.IsNotExist(err) {
		t.Fatalf("the DASH directory must be removed: %v", err)
	}
	if !rec.contains("WARNUNG: Medieninformationen nicht ermittelbar") {
		t.Fatalf("missing warning log: %q", rec.all())
	}
}

func TestFinalizeReusesExistingSeasonDirectory(t *testing.T) {
	m, _ := newManager(t, false)
	existing := filepath.Join(m.DownloadsDir, "My.Show.S02.German.AAC.720p.Web-DL.x264-oldgroup")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	item := Item{
		StagingPath: "My Show/Season 02/My Show S02E01 - Zweite",
		BaseStem:    "My.Show.S02E01",
		DirStem:     "My.Show.S02",
		DirUsesTag:  true,
	}
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, staging+".mp4", "video", 0o644)

	final, err := m.Finalize(context.Background(), item, staging+".mp4")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	want := filepath.Join(existing, "My.Show.S02E01."+mediaTag+"-example.mp4")
	if final != want {
		t.Fatalf("Finalize = %q, want %q", final, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the file must land in the existing season directory: %v", err)
	}
	entries, err := os.ReadDir(m.DownloadsDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one season directory, got %d", len(entries))
	}
}

func TestFinalizeKeepsTheContainerExtension(t *testing.T) {
	m, _ := newManager(t, false)
	item := Item{
		StagingPath: "My Show/Season 05/My Show S05E01 - Fuenfte",
		BaseStem:    "My.Show.S05E01",
		DirStem:     "My.Show.S05",
		DirUsesTag:  true,
	}
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, staging+".mkv", "video", 0o644)

	final, err := m.Finalize(context.Background(), item, staging+".mkv")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	want := filepath.Join(m.DownloadsDir, "My.Show.S05."+mediaTag+"-example", "My.Show.S05E01."+mediaTag+"-example.mkv")
	if final != want {
		t.Fatalf("Finalize = %q, want %q", final, want)
	}
}

func TestFinalizeTatortLandsInTheDownloadsRoot(t *testing.T) {
	m, _ := newManager(t, false)
	item := Item{
		StagingPath: "Tatort Köln - Folge 850 - Der Fall (2019)",
		BaseStem:    "Tatort.Köln.Folge.850.2019",
	}
	staging := m.StagingPath(item)
	writeFile(t, staging+".mp4", "video", 0o644)

	final, err := m.Finalize(context.Background(), item, staging+".mp4")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	want := filepath.Join(m.DownloadsDir, "Tatort.Köln.Folge.850.2019."+mediaTag+"-example.mp4")
	if final != want {
		t.Fatalf("Finalize = %q, want %q", final, want)
	}
}

func TestFinalizeLongNamesStayBelowNameMax(t *testing.T) {
	m, _ := newManager(t, false)
	m.FileTag = "abcdefghijklmnop"
	m.Probe = func(context.Context, string) (string, error) { return mediaTag, nil }

	longTitle := strings.Repeat("Titel ", 60)
	item := Item{
		StagingPath: media.BuildEpisodePath(longTitle, 1, new(1), longTitle),
		BaseStem:    media.EpisodeNameStem(longTitle, 1, new(1), 2),
		DirStem:     media.SeasonNameStem(longTitle, 1),
		DirUsesTag:  true,
	}
	staging := m.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, staging+".mp4", "video", 0o644)

	final, err := m.Finalize(context.Background(), item, staging+".mp4")
	if err != nil {
		t.Fatalf("a long title must still finalize: %v", err)
	}
	if got := len(filepath.Base(final)); got > 255 {
		t.Fatalf("file name is %d bytes, want <= 255", got)
	}
	if got := len(filepath.Base(filepath.Dir(final))); got > 255 {
		t.Fatalf("directory name is %d bytes, want <= 255", got)
	}
}

func TestFinalizeMissingDownloadIsReported(t *testing.T) {
	m, _ := newManager(t, false)
	item := Item{
		StagingPath: "Missing/Season 01/Missing S01E01 - Nope",
		BaseStem:    "Missing.S01E01",
		DirStem:     "Missing.S01",
		DirUsesTag:  true,
	}
	_, err := m.Finalize(context.Background(), item, m.StagingPath(item)+".mp4")
	if err == nil {
		t.Fatal("expected an error for a missing download")
	}
	want := "Download finished without a file: " + m.StagingPath(item)
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestStagingPathAndWorkDir(t *testing.T) {
	m, _ := newManager(t, false)
	item := Item{StagingPath: "Show/Season 01/Episode"}
	if got, want := m.StagingPath(item), filepath.Join(m.DownloadsDir, "Show", "Season 01", "Episode"); got != want {
		t.Fatalf("StagingPath = %q, want %q", got, want)
	}
	if got, want := m.WorkDir(item), m.StagingPath(item)+".dash"; got != want {
		t.Fatalf("WorkDir = %q, want %q", got, want)
	}
}

func TestFirstOutputDir(t *testing.T) {
	m, _ := newManager(t, false)
	t.Run("is empty without a match", func(t *testing.T) {
		if got := m.firstOutputDir("My.Show.S09"); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("ignores plain files", func(t *testing.T) {
		writeFile(t, filepath.Join(m.DownloadsDir, "My.Show.S09.somemedia.mp4"), "video", 0o644)
		if got := m.firstOutputDir("My.Show.S09"); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("returns the existing directory name", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(m.DownloadsDir, "My.Show.S09.German.AAC.1080p.Web-DL.x264-oldgroup"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		want := "My.Show.S09.German.AAC.1080p.Web-DL.x264-oldgroup"
		if got := m.firstOutputDir("My.Show.S09"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

// newManager builds a Manager over a fresh downloads directory.
func newManager(t *testing.T, force bool) (*Manager, *recorder) {
	t.Helper()
	rec := &recorder{}
	m := &Manager{
		DownloadsDir: filepath.Join(t.TempDir(), "downloads"),
		FileTag:      "example",
		Force:        force,
		Probe:        func(context.Context, string) (string, error) { return mediaTag, nil },
		Logf:         rec.logf,
	}
	if err := os.MkdirAll(m.DownloadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return m, rec
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// After a download the staging directories must be gone: the work directory
// with the segments is a child of them, so it has to be removed first.
func TestFinalizeRemovesTheEmptyStagingDirectories(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{
		DownloadsDir: dir,
		FileTag:      "tag",
		Probe: func(context.Context, string) (string, error) {
			return "German.AAC.1080p.Web-DL.x264", nil
		},
	}
	item := Item{
		StagingPath: "Show/Season 01/Show S01E01 - Episode",
		FinalGlob:   "Show.S01/Show.S01E01.*",
		BaseStem:    "Show.S01E01",
		DirStem:     "Show.S01",
		DirUsesTag:  true,
	}
	staging := manager.StagingPath(item)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staging+".mp4", []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manager.WorkDir(item), 0o755); err != nil {
		t.Fatal(err)
	}

	final, err := manager.Finalize(context.Background(), item, staging+".mp4")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("the finished file is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(staging)); !os.IsNotExist(err) {
		t.Errorf("the staging directory %q still exists", filepath.Dir(staging))
	}
	if _, err := os.Stat(manager.WorkDir(item)); !os.IsNotExist(err) {
		t.Errorf("the work directory %q still exists", manager.WorkDir(item))
	}
}
