package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// cliEnv is an isolated HOME/XDG environment plus a temp downloads directory.
type cliEnv struct {
	root      string
	home      string
	state     string
	config    string
	downloads string
}

// isolatedEnv isolates every environment variable the CLI reads and returns the
// resolved paths. Dependencies are stubbed because the dependency check only
// looks for a non-empty value.
func isolatedEnv(t *testing.T) *cliEnv {
	t.Helper()
	root := t.TempDir()
	env := &cliEnv{
		root:      root,
		home:      filepath.Join(root, "home"),
		state:     filepath.Join(root, "state"),
		config:    filepath.Join(root, "config"),
		downloads: filepath.Join(root, "downloads"),
	}
	for _, dir := range []string{env.home, env.state, env.config} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", env.home)
	t.Setenv("XDG_STATE_HOME", env.state)
	t.Setenv("XDG_CONFIG_HOME", env.config)
	t.Setenv("ARD_PLUS_CREDENTIALS_FILE", filepath.Join(env.config, "ard-plus-dl", "credentials"))
	t.Setenv("ARD_PLUS_USER", "")
	t.Setenv("ARD_PLUS_PASSWORD", "")
	t.Setenv("ARD_PLUS_TAG", "")
	t.Setenv("ARD_PLUS_FFMPEG", filepath.Join(root, "ffmpeg-stub"))
	t.Setenv("ARD_PLUS_FFPROBE", filepath.Join(root, "ffprobe-stub"))
	t.Setenv("DOWNLOADS_DIR", env.downloads)
	t.Setenv("ARD_PLUS_LIBRARY", "")
	return env
}

// storeToken drops a valid session token into the state directory, so the CLI
// skips the credentials check.
func (e *cliEnv) storeToken(t *testing.T) {
	t.Helper()
	dir := filepath.Join(e.state, "ard-plus-dl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("stored-token"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestHelpExitsZero(t *testing.T) {
	isolatedEnv(t)
	for _, arg := range []string{"--help", "-h"} {
		code, stdout, stderr := runCLI(t, arg)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout, "Usage: ard-plus-dl [options] <url>") {
			t.Errorf("%s: stdout missing usage:\n%s", arg, stdout)
		}
		if stderr != "" {
			t.Errorf("%s: stderr = %q, want empty", arg, stderr)
		}
	}
}

func TestVersionExitsZero(t *testing.T) {
	isolatedEnv(t)
	for _, arg := range []string{"--version", "-v"} {
		code, stdout, _ := runCLI(t, arg)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if stdout != "ard-plus-dl dev\n" {
			t.Errorf("%s: stdout = %q, want the version line", arg, stdout)
		}
	}
}

func TestUnknownOptionExitsOne(t *testing.T) {
	isolatedEnv(t)
	code, _, stderr := runCLI(t, "--bogus")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Unknown option: --bogus") {
		t.Errorf("stderr = %q, want the unknown option message", stderr)
	}
}

func TestMissingURLExitsOne(t *testing.T) {
	isolatedEnv(t)
	code, _, stderr := runCLI(t)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "URL or --links-file required") {
		t.Errorf("stderr = %q, want the missing URL message", stderr)
	}
}

func TestLinksFileRequiresArgument(t *testing.T) {
	isolatedEnv(t)
	code, _, stderr := runCLI(t, "--links-file")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Error: --links-file requires a file argument") {
		t.Errorf("stderr = %q, want the links-file message", stderr)
	}
}

func TestInvalidURLIsCheckedBeforeDependencies(t *testing.T) {
	env := isolatedEnv(t)
	// No ffmpeg/ffprobe anywhere: the URL must still be rejected first.
	t.Setenv("ARD_PLUS_FFMPEG", "")
	t.Setenv("ARD_PLUS_FFPROBE", "")
	t.Setenv("PATH", filepath.Join(env.root, "empty-bin"))

	code, _, stderr := runCLI(t, "https://example.com/details/x")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Invalid URL: unsupported host: example.com") {
		t.Errorf("stderr = %q, want the URL reason", stderr)
	}
	if strings.Contains(stderr, "Missing required dependencies") {
		t.Errorf("stderr = %q, dependencies must be checked after the URL", stderr)
	}
}

func TestMissingDependencies(t *testing.T) {
	env := isolatedEnv(t)
	t.Setenv("ARD_PLUS_FFMPEG", "")
	t.Setenv("ARD_PLUS_FFPROBE", "")
	t.Setenv("PATH", filepath.Join(env.root, "empty-bin"))
	t.Setenv("ARD_PLUS_TAG", "example")

	code, _, stderr := runCLI(t, "https://www.ardplus.de/details/x")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Missing required dependencies: ffmpeg ffprobe") {
		t.Errorf("stderr = %q, want the dependency message", stderr)
	}
}

func TestMissingFileTag(t *testing.T) {
	isolatedEnv(t)
	code, _, stderr := runCLI(t, "https://www.ardplus.de/details/x")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Datei-Tag missing!") {
		t.Errorf("stderr = %q, want the missing tag message", stderr)
	}
}

func TestInvalidFileTag(t *testing.T) {
	isolatedEnv(t)
	t.Setenv("ARD_PLUS_TAG", "bad/tag")
	code, _, stderr := runCLI(t, "https://www.ardplus.de/details/x")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Invalid Datei-Tag: bad/tag (allowed: letters, digits, '.', '_', '-')") {
		t.Errorf("stderr = %q, want the invalid tag message", stderr)
	}
}

func TestMissingCredentials(t *testing.T) {
	isolatedEnv(t)
	t.Setenv("ARD_PLUS_TAG", "example")
	code, _, stderr := runCLI(t, "https://www.ardplus.de/details/x")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Credentials missing!") {
		t.Errorf("stderr = %q, want the credentials message", stderr)
	}
	if !strings.Contains(stderr, "ARD_PLUS_USER and ARD_PLUS_PASSWORD") {
		t.Errorf("stderr = %q, want the credentials help", stderr)
	}
}

func TestDownloadsDirectoryNotWritable(t *testing.T) {
	env := isolatedEnv(t)
	blocker := filepath.Join(env.root, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(blocker, "downloads")
	t.Setenv("DOWNLOADS_DIR", blocked)
	t.Setenv("ARD_PLUS_TAG", "example")

	code, _, stderr := runCLI(t, "https://www.ardplus.de/details/x")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Downloads directory is not writable: "+blocked) {
		t.Errorf("stderr = %q, want the unwritable directory message", stderr)
	}
}

func TestBatchLinksFileMissing(t *testing.T) {
	env := isolatedEnv(t)
	missing := filepath.Join(env.root, "missing.txt")
	code, _, stderr := runCLI(t, "--links-file", missing)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Links file missing or not found: "+missing) {
		t.Errorf("stderr = %q, want the missing links file message", stderr)
	}
}

// batchLogFiles returns the single successful/failed/download log contents.
func batchLogFiles(t *testing.T, linksPath string) (success, failed, download string) {
	t.Helper()
	logsDir := filepath.Join(filepath.Dir(linksPath), "logs")
	read := func(pattern string) string {
		matches, err := filepath.Glob(filepath.Join(logsDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("%s matched %v, want exactly one log file", pattern, matches)
		}
		data, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	return read("successful_links_*.txt"), read("failed_links_*.txt"), read("download_log_*.txt")
}

func TestBatchFailureExitsOne(t *testing.T) {
	env := isolatedEnv(t)
	env.storeToken(t)
	t.Setenv("ARD_PLUS_TAG", "example")

	linksPath := filepath.Join(env.root, "links.txt")
	if err := os.WriteFile(linksPath, []byte("https://example.com/details/ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "--links-file", linksPath)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "1 link(s) failed") {
		t.Errorf("stderr = %q, want the batch failure summary", stderr)
	}
	want := "Recorded failure: https://example.com/details/ignored (invalid URL: unsupported host: example.com)"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	success, failed, download := batchLogFiles(t, linksPath)
	if success != "" {
		t.Errorf("success log = %q, want empty", success)
	}
	if failed != "https://example.com/details/ignored\tinvalid URL: unsupported host: example.com\n" {
		t.Errorf("failed log = %q, want the tab separated failure", failed)
	}
	if !strings.Contains(download, "Batch download started at") || !strings.Contains(download, "Batch download finished.") {
		t.Errorf("download log = %q, want the batch framing", download)
	}
}

func TestBatchWithoutLinksExitsZero(t *testing.T) {
	env := isolatedEnv(t)
	env.storeToken(t)
	t.Setenv("ARD_PLUS_TAG", "example")

	linksPath := filepath.Join(env.root, "links.txt")
	if err := os.WriteFile(linksPath, []byte("# nothing to do\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "--links-file", linksPath)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "Batch download finished.") {
		t.Errorf("stdout = %q, want the batch footer", stdout)
	}
	if !strings.Contains(stdout, "Downloads directory: "+env.downloads) {
		t.Errorf("stdout = %q, want the downloads directory banner", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	_, _, download := batchLogFiles(t, linksPath)
	if !strings.Contains(download, "Batch download started at") {
		t.Errorf("download log = %q, want the batch framing", download)
	}
}

// useTempLibrary puts the SQLite database below the isolated HOME so a test
// can inspect it without ever touching the real library.
func (e *cliEnv) useTempLibrary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(e.home, ".ard-plus-dl", "library.db")
	t.Setenv("ARD_PLUS_LIBRARY", path)
	return path
}

// assertLibraryBelowHome verifies that the database lives in the isolated tree.
func (e *cliEnv) assertLibraryBelowHome(t *testing.T, path string) {
	t.Helper()
	if !strings.HasPrefix(path, e.home+string(filepath.Separator)) {
		t.Fatalf("library path %q is not below the temp HOME %q", path, e.home)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("library database %q was not created: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("library database %q is not a regular file", path)
	}
}

func TestAddThenListLibrary(t *testing.T) {
	env := isolatedEnv(t)
	libraryPath := env.useTempLibrary(t)
	const url = "https://www.ardplus.de/details/m1-film"

	code, stdout, stderr := runCLI(t, "--add", url)
	if code != 0 {
		t.Fatalf("--add exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "Zur Bibliothek hinzugefügt: "+url) {
		t.Errorf("stdout = %q, want the add confirmation", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	env.assertLibraryBelowHome(t, libraryPath)

	code, stdout, stderr = runCLI(t, "--library")
	if code != 0 {
		t.Fatalf("--library exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	want := url + " [unbekannt] 0 neu, noch nicht geprüft"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want the library row %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestAddInvalidURLExitsOne(t *testing.T) {
	env := isolatedEnv(t)
	libraryPath := env.useTempLibrary(t)

	code, stdout, stderr := runCLI(t, "--add", "https://example.com/details/x")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Invalid URL: unsupported host: example.com") {
		t.Errorf("stderr = %q, want the URL reason", stderr)
	}
	if strings.Contains(stdout, "Zur Bibliothek hinzugefügt") {
		t.Errorf("stdout = %q, want no confirmation for an invalid URL", stdout)
	}
	// The database is opened before the URL validation, but must stay empty.
	env.assertLibraryBelowHome(t, libraryPath)
	code, stdout, _ = runCLI(t, "--library")
	if code != 0 {
		t.Fatalf("--library exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Bibliothek ist leer") {
		t.Errorf("stdout = %q, want the empty library message", stdout)
	}
}

func TestAddRequiresURL(t *testing.T) {
	isolatedEnv(t)
	code, _, stderr := runCLI(t, "--add")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Error: --add requires a URL") {
		t.Errorf("stderr = %q, want the --add message", stderr)
	}
}

func TestLibraryFlagsAreExclusive(t *testing.T) {
	isolatedEnv(t)
	code, _, stderr := runCLI(t, "--library", "--check")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Only one of --library, --check, --download-new, --add may be used") {
		t.Errorf("stderr = %q, want the exclusivity message", stderr)
	}
}

func TestEmptyLibraryModesExitZero(t *testing.T) {
	for _, arg := range []string{"--library", "--check", "--download-new"} {
		t.Run(arg, func(t *testing.T) {
			env := isolatedEnv(t)
			libraryPath := env.useTempLibrary(t)

			code, stdout, stderr := runCLI(t, arg)
			if code != 0 {
				t.Fatalf("%s exit code = %d, want 0 (stderr %q)", arg, code, stderr)
			}
			if !strings.Contains(stdout, "Bibliothek ist leer") {
				t.Errorf("%s stdout = %q, want the empty library message", arg, stdout)
			}
			// No credentials are configured and no token exists, so reaching
			// the session would fail; the empty library short-circuits first.
			if strings.Contains(stderr, "Credentials missing!") {
				t.Errorf("%s stderr = %q, must not ask for credentials", arg, stderr)
			}
			env.assertLibraryBelowHome(t, libraryPath)
		})
	}
}

func TestLogRouterReroutesComponentOutput(t *testing.T) {
	var console bytes.Buffer
	logs := newLogRouter(&console)
	logs.Logf("erste %d", 1)

	var routed []string
	logs.SetSink(func(line string) { routed = append(routed, line) })
	logs.Logf("zweite %s", "Zeile")

	if got := console.String(); got != "erste 1\n" {
		t.Fatalf("console output = %q, want only the pre-UI line", got)
	}
	if len(routed) != 1 || routed[0] != "zweite Zeile" {
		t.Fatalf("routed lines = %v, want the TUI line", routed)
	}
}

func TestResolveDebugDirCreatesTheDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "debug")

	env := func(key string) string {
		if key == "ARD_PLUS_DEBUG_DIR" {
			return dir
		}
		return ""
	}
	got := resolveDebugDir(env)
	if got != dir {
		t.Fatalf("resolveDebugDir = %q, want %q", got, dir)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("the debug directory must exist: %v", err)
	}
}

func TestResolveDebugDirIsOffWithoutTheVariable(t *testing.T) {
	if got := resolveDebugDir(func(string) string { return "" }); got != "" {
		t.Fatalf("resolveDebugDir = %q, want an empty string", got)
	}
}

// The debug directory must reach the downloader, the pipeline and the API
// client, otherwise the documented diagnostic files never appear.
func TestBuildAppWiresTheDebugDirectory(t *testing.T) {
	dir := t.TempDir()
	opts := &config.Options{DownloadsDir: dir, FileTag: "tag", TokenFile: filepath.Join(dir, "token")}
	logs := newLogRouter(io.Discard)
	session := buildSession(opts, logs)
	application := buildApp(opts, session, "ffmpeg", "ffprobe", io.Discard, logs, nil, dir)

	pipeline, ok := application.Fetcher.(*app.Pipeline)
	if !ok {
		t.Fatalf("Fetcher = %T, want *app.Pipeline", application.Fetcher)
	}
	if pipeline.DebugDir != dir {
		t.Errorf("Pipeline.DebugDir = %q, want %q", pipeline.DebugDir, dir)
	}
	if pipeline.Downloader == nil || pipeline.Downloader.DebugDir != dir {
		t.Errorf("Downloader.DebugDir = %q, want %q", pipeline.Downloader.DebugDir, dir)
	}
	if pipeline.HTTP == nil {
		t.Error("the pipeline must have an HTTP client with a timeout")
	} else if pipeline.HTTP.Timeout == 0 {
		t.Error("the manifest request must have a timeout")
	}
}

// The prompts must react to a cancelled context: Ctrl+C is caught by the
// signal handler, so a prompt that ignores the context would hang.
func TestReadLineGivesUpOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	if got := readLine(ctx, reader); got != "" {
		t.Fatalf("readLine = %q, want an empty string", got)
	}
}

func TestHeadlessSelectSeasonsReportsTheCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ui := &headlessUI{out: io.Discard, in: strings.NewReader(""), interactive: true}
	_, err := ui.SelectSeasons(ctx, "Test Show", []app.Season{{Number: 1, Title: "Staffel 1"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SelectSeasons error = %v, want context.Canceled", err)
	}
}

func TestHeadlessSelectCategoriesReportsTheCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ui := &headlessUI{out: io.Discard, in: strings.NewReader(""), interactive: true}
	_, err := ui.SelectCategories(ctx, "Tatort-Archiv", []app.Category{{Slug: "tatort-koeln", Title: "Köln"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SelectCategories error = %v, want context.Canceled", err)
	}
}

func TestHeadlessSelectSeasonsReadsTheAnswer(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("2\n"), interactive: true}
	seasons := []app.Season{
		{Number: 1, Title: "Staffel 1"},
		{Number: 2, Title: "Staffel 2"},
		{Number: 3, Title: "Staffel 3"},
	}
	selected, err := ui.SelectSeasons(context.Background(), "Test Show", seasons)
	if err != nil {
		t.Fatalf("SelectSeasons: %v", err)
	}
	if len(selected) != 1 || selected[0] != 2 {
		t.Fatalf("selected = %v, want [2]", selected)
	}
}

func TestHeadlessSelectSeasonsAcceptsSeveralNumbers(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("1, 3\n"), interactive: true}
	seasons := []app.Season{{Number: 1}, {Number: 2}, {Number: 3}}
	selected, err := ui.SelectSeasons(context.Background(), "Test Show", seasons)
	if err != nil {
		t.Fatalf("SelectSeasons: %v", err)
	}
	if len(selected) != 2 || selected[0] != 1 || selected[1] != 3 {
		t.Fatalf("selected = %v, want [1 3]", selected)
	}
}

func TestHeadlessSelectSeasonsRefusesAnUnknownNumber(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("9\n2\n"), interactive: true}
	seasons := []app.Season{{Number: 1}, {Number: 2}}
	selected, err := ui.SelectSeasons(context.Background(), "Test Show", seasons)
	if err != nil {
		t.Fatalf("SelectSeasons: %v", err)
	}
	if len(selected) != 1 || selected[0] != 2 {
		t.Fatalf("selected = %v, want [2]", selected)
	}
}

func TestHeadlessSelectSeasonsNeedsATerminal(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("1\n"), interactive: false}
	if _, err := ui.SelectSeasons(context.Background(), "Test Show", []app.Season{{Number: 1}}); err == nil {
		t.Fatal("SelectSeasons must fail without a terminal")
	}
}

func TestHeadlessSelectCategoriesReadsTheAnswer(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("2\n"), interactive: true}
	categories := []app.Category{
		{Slug: "tatort-koeln", Title: "Köln"},
		{Slug: "tatort-muenster", Title: "Münster"},
	}
	slugs, err := ui.SelectCategories(context.Background(), "Tatort-Archiv", categories)
	if err != nil {
		t.Fatalf("SelectCategories: %v", err)
	}
	if len(slugs) != 1 || slugs[0] != "tatort-muenster" {
		t.Fatalf("slugs = %v, want [tatort-muenster]", slugs)
	}
}

func TestHeadlessSelectCategoriesTakesAll(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("alle\n"), interactive: true}
	categories := []app.Category{
		{Slug: "tatort-koeln", Title: "Köln"},
		{Slug: "tatort-muenster", Title: "Münster"},
	}
	slugs, err := ui.SelectCategories(context.Background(), "Tatort-Archiv", categories)
	if err != nil {
		t.Fatalf("SelectCategories: %v", err)
	}
	if len(slugs) != 2 {
		t.Fatalf("slugs = %v, want both categories", slugs)
	}
}

func TestHeadlessAskTatortSkipReadsTheAnswer(t *testing.T) {
	ui := &headlessUI{out: io.Discard, in: strings.NewReader("4\n"), interactive: true}
	skip, err := ui.AskTatortSkip(context.Background(), "Köln", 12)
	if err != nil {
		t.Fatalf("AskTatortSkip: %v", err)
	}
	if skip != 4 {
		t.Fatalf("skip = %d, want 4", skip)
	}
}

func TestParseSeasonNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"0", 0, true},
		{"12", 12, true},
		{"", 0, false},
		{"a", 0, false},
		{"1a", 0, false},
		{"-1", 0, false},
	}
	for _, tc := range cases {
		got, err := parseSeasonNumber(tc.in)
		if tc.ok && err != nil {
			t.Errorf("parseSeasonNumber(%q) = %v, want no error", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("parseSeasonNumber(%q) = %d, want an error", tc.in, got)
		}
		if tc.ok && got != tc.want {
			t.Errorf("parseSeasonNumber(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestHasSeason(t *testing.T) {
	seasons := []app.Season{{Number: 1}, {Number: 3}}
	if !hasSeason(seasons, 3) {
		t.Error("hasSeason(3) must be true")
	}
	if hasSeason(seasons, 2) {
		t.Error("hasSeason(2) must be false")
	}
}

func TestPrintCheckResultsCountsTheNewEpisodes(t *testing.T) {
	var out bytes.Buffer
	results := []app.CheckResult{
		{Series: store.Series{URL: "https://www.ardplus.de/details/a"}, Title: "Serie A", Kind: store.KindSeries,
			New: []store.EpisodeRef{{ID: "e1"}}, Total: 3},
		{Series: store.Series{URL: "https://www.ardplus.de/details/b"}, Title: "Film B", Kind: store.KindMovie},
		{Series: store.Series{URL: "https://www.ardplus.de/details/c"}, Title: "Serie C", Err: errors.New("kaputt")},
	}
	if failed := printCheckResults(&out, results); !failed {
		t.Error("a failed check must be reported")
	}
	text := out.String()
	if !strings.Contains(text, "Serie A: 1 neue Folge(n), 3 insgesamt") {
		t.Errorf("missing the count of the series:\n%s", text)
	}
	if !strings.Contains(text, "Film B: Film") {
		t.Errorf("missing the movie line:\n%s", text)
	}
	if !strings.Contains(text, "Serie C: Fehler: kaputt") {
		t.Errorf("missing the error line:\n%s", text)
	}
	if !strings.Contains(text, "3 Serie(n) geprüft, 1 neue Folge(n).") {
		t.Errorf("missing the summary:\n%s", text)
	}
}

func TestDisplayTitleFallsBackToTheURL(t *testing.T) {
	result := app.CheckResult{Series: store.Series{URL: "https://www.ardplus.de/details/a"}}
	if got := displayTitle(result); got != "https://www.ardplus.de/details/a" {
		t.Fatalf("displayTitle = %q, want the URL", got)
	}
	result.Title = "Serie A"
	if got := displayTitle(result); got != "Serie A" {
		t.Fatalf("displayTitle = %q, want the title", got)
	}
}
