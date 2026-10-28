package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revunix/ard-plus-dl/internal/ardplus"
	"github.com/revunix/ard-plus-dl/internal/auth"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/dash"
	"github.com/revunix/ard-plus-dl/internal/download"
)

// testJWT has the header {"typ":"JWT"}, so auth.JWTType accepts it.
const testJWT = "eyJ0eXAiOiJKV1QifQ.eyJzdWIiOiJ0ZXN0In0.signature"

// backend is an httptest server that answers the GraphQL metadata API, the
// playback session API and the Tatort category pages.
type backend struct {
	t   *testing.T
	srv *httptest.Server

	mu             sync.Mutex
	graphQL        func(operation string, variables map[string]any) (string, int)
	category       func(slug string) (string, int)
	hub            func() (string, int)
	loginToken     string
	playbackParam  string
	rejectPlayback bool

	graphQLCalls int
	sessionCalls int
	loginCalls   int
	sessionIDs   []string
	deleteIDs    []string
}

func newBackend(t *testing.T) *backend {
	t.Helper()
	b := &backend{t: t, loginToken: testJWT, playbackParam: "hdnts=fake-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", b.handleGraphQL)
	mux.HandleFunc("/token/session/playback/delete", b.handleDelete)
	mux.HandleFunc("/token/session", b.handleTokenSession)
	mux.HandleFunc("/kategorie/tatort-cities", b.handleHub)
	mux.HandleFunc("/kategorie/", b.handleCategory)
	mux.HandleFunc("/tatort", b.handleHub)
	mux.HandleFunc("/login", b.handleLogin)
	b.srv = httptest.NewServer(mux)
	t.Cleanup(b.srv.Close)
	return b
}

func (b *backend) setGraphQL(handler func(operation string, variables map[string]any) (string, int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.graphQL = handler
}

func (b *backend) setHub(handler func() (string, int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hub = handler
}

func (b *backend) setCategory(handler func(slug string) (string, int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.category = handler
}

// handleHub answers the Tatort hub page.
func (b *backend) handleHub(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	handler := b.hub
	b.mu.Unlock()
	if handler == nil {
		http.Error(w, "no hub handler", http.StatusInternalServerError)
		return
	}
	body, status := handler()
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (b *backend) setPlayback(param string, reject bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.playbackParam = param
	b.rejectPlayback = reject
}

func (b *backend) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GraphQL must use GET", http.StatusMethodNotAllowed)
		return
	}
	request.OperationName = r.URL.Query().Get("operationName")
	if err := json.Unmarshal([]byte(r.URL.Query().Get("variables")), &request.Variables); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	b.graphQLCalls++
	handler := b.graphQL
	b.mu.Unlock()
	if handler == nil {
		http.Error(w, "no graphql handler", http.StatusInternalServerError)
		return
	}
	payload, status := handler(request.OperationName, request.Variables)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, payload)
}

func (b *backend) handleTokenSession(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var request struct {
		ContentID string `json:"contentId"`
	}
	_ = json.Unmarshal(body, &request)
	b.mu.Lock()
	b.sessionCalls++
	b.sessionIDs = append(b.sessionIDs, request.ContentID)
	reject := b.rejectPlayback
	param := b.playbackParam
	b.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if reject {
		_, _ = io.WriteString(w, `{"authorizationParams":null}`)
		return
	}
	_, _ = io.WriteString(w, fmt.Sprintf(`{"authorizationParams":%q}`, param))
}

func (b *backend) handleDelete(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var request struct {
		ContentID string `json:"contentId"`
	}
	_ = json.Unmarshal(body, &request)
	b.mu.Lock()
	b.deleteIDs = append(b.deleteIDs, request.ContentID)
	b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{}`)
}

func (b *backend) handleCategory(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/kategorie/")
	b.mu.Lock()
	handler := b.category
	b.mu.Unlock()
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	body, status := handler(slug)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (b *backend) handleLogin(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.loginCalls++
	token := b.loginToken
	b.mu.Unlock()
	if token != "" {
		w.Header().Set("Authorization", "Bearer "+token)
	}
	w.WriteHeader(http.StatusOK)
}

func (b *backend) client() *ardplus.Client {
	return &ardplus.Client{
		HTTP:        b.srv.Client(),
		GraphQLURL:  b.srv.URL + "/graphql",
		TokenURL:    b.srv.URL,
		CategoryURL: b.srv.URL + "/kategorie",
		Sleep:       func(context.Context, time.Duration) {},
	}
}

// session returns a session with a stored token, so Ensure never logs in.
func (b *backend) session(t *testing.T, playback *ardplus.Client) *auth.Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("stored-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &auth.Session{
		Store:    auth.Store{Path: path},
		Login:    &auth.Client{HTTP: b.srv.Client(), LoginURL: b.srv.URL + "/login"},
		Playback: playback,
		Username: "user",
		Password: "pass",
	}
}

func dataResponse(data any) (string, int) {
	payload, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		panic(err)
	}
	return string(payload), http.StatusOK
}

func graphQLErrorResponse(message string) (string, int) {
	return fmt.Sprintf(`{"errors":[{"message":%q}]}`, message), http.StatusOK
}

func movieData(id, title, year, dashURL string) map[string]any {
	return tatortMovieData(id, title, year, "", "", "", dashURL)
}

func tatortMovieData(id, title, year, location, team, episodeNo, dashURL string) map[string]any {
	return map[string]any{
		"id":             id,
		"title":          title,
		"productionYear": year,
		"customData": map[string]any{
			"team":                    team,
			"location":                location,
			"episodeProductionNumber": episodeNo,
		},
		"videoSource": map[string]any{"dashUrl": dashURL},
	}
}

func seasonData(id string, number int, title string) map[string]any {
	return map[string]any{"id": id, "title": title, "seasonInSeries": number}
}

func seriesData(title string, seasons ...map[string]any) map[string]any {
	nodes := make([]any, 0, len(seasons))
	for _, season := range seasons {
		nodes = append(nodes, season)
	}
	return map[string]any{"title": title, "seasons": map[string]any{"nodes": nodes}}
}

func episodeData(id string, number int, title, dashURL string) map[string]any {
	return map[string]any{
		"id":              id,
		"episodeInSeason": number,
		"title":           title,
		"videoSource":     map[string]any{"dashUrl": dashURL},
	}
}

func episodesData(episodes ...map[string]any) map[string]any {
	nodes := make([]any, 0, len(episodes))
	for _, episode := range episodes {
		nodes = append(nodes, episode)
	}
	return map[string]any{"episodes": map[string]any{"nodes": nodes}}
}

// fakeFetcher stands in for the DASH pipeline.
type fakeFetcher struct {
	mu       sync.Mutex
	fetches  []string
	muxes    []string
	fetchErr error
	muxErr   error
	onFetch  func(manifestURL, workDir string) error
}

func (f *fakeFetcher) Fetch(_ context.Context, manifestURL, workDir string, progress func(done, total int)) ([]dash.DownloadedTrack, error) {
	f.mu.Lock()
	f.fetches = append(f.fetches, manifestURL)
	hook := f.onFetch
	err := f.fetchErr
	f.mu.Unlock()
	if progress != nil {
		progress(1, 2)
	}
	if hook != nil {
		if hookErr := hook(manifestURL, workDir); hookErr != nil {
			return nil, hookErr
		}
	}
	if err != nil {
		return nil, err
	}
	return []dash.DownloadedTrack{{
		Track: &dash.Track{ID: "v", Kind: dash.KindVideo},
		Files: []string{filepath.Join(workDir, "init.mp4")},
	}}, nil
}

func (f *fakeFetcher) Mux(_ context.Context, _ string, _ []dash.DownloadedTrack, out string) error {
	f.mu.Lock()
	f.muxes = append(f.muxes, out)
	err := f.muxErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(out+".mp4", []byte("fake media"), 0o644)
}

func (f *fakeFetcher) fetchList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fetches...)
}

func (f *fakeFetcher) muxList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.muxes...)
}

// reset forgets the recorded calls, so a second run can be observed on its
// own.
func (f *fakeFetcher) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches = nil
	f.muxes = nil
}

type seasonCall struct {
	show    string
	seasons []Season
}

// fakeUI records the app/UI interaction and answers with canned values.
type fakeUI struct {
	mu          sync.Mutex
	events      []Event
	startErr    error
	seasonCalls []seasonCall
	seasons     []int
	seasonErr   error
	tatortSkip  int
	tatortErr   error
	tatortCalls int

	categoryCalls int
	categories    []string
}

func (u *fakeUI) Start(context.Context, *config.Options) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.startErr
}

func (u *fakeUI) SelectSeasons(_ context.Context, show string, seasons []Season) ([]int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seasonCalls = append(u.seasonCalls, seasonCall{show: show, seasons: append([]Season(nil), seasons...)})
	return u.seasons, u.seasonErr
}

func (u *fakeUI) SelectCategories(_ context.Context, _ string, categories []Category) ([]string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.categoryCalls++
	if u.categories != nil {
		return u.categories, nil
	}
	slugs := make([]string, 0, len(categories))
	for _, category := range categories {
		slugs = append(slugs, category.Slug)
	}
	return slugs, nil
}

func (u *fakeUI) AskTatortSkip(_ context.Context, _ string, _ int) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.tatortCalls++
	return u.tatortSkip, u.tatortErr
}

func (u *fakeUI) Event(event Event) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, event)
}

func (u *fakeUI) logs() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var lines []string
	for _, event := range u.events {
		if event.Kind == EventLog {
			lines = append(lines, event.Message)
		}
	}
	return lines
}

func (u *fakeUI) eventsOf(kind EventKind) []Event {
	u.mu.Lock()
	defer u.mu.Unlock()
	var matches []Event
	for _, event := range u.events {
		if event.Kind == kind {
			matches = append(matches, event)
		}
	}
	return matches
}

type harness struct {
	t         *testing.T
	backend   *backend
	app       *App
	fetcher   *fakeFetcher
	ui        *fakeUI
	downloads string
	opts      *config.Options
}

func newHarness(t *testing.T, opts *config.Options) *harness {
	t.Helper()
	return newHarnessWith(t, newBackend(t), opts)
}

func newHarnessWith(t *testing.T, b *backend, opts *config.Options) *harness {
	t.Helper()
	downloads := filepath.Join(t.TempDir(), "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeFetcher{}
	ui := &fakeUI{}
	api := b.client()
	manager := &download.Manager{
		DownloadsDir: downloads,
		Force:        opts.ForceRedownload,
		Probe:        func(context.Context, string) (string, error) { return harnessMediaTag, nil },
		// The CLI points this at stdout; the tests record it like the app
		// log lines so the skip and rename messages stay assertable.
		Logf: func(format string, args ...any) {
			ui.Event(Event{Kind: EventLog, Message: fmt.Sprintf(format, args...)})
		},
	}
	a := &App{
		Config:  opts,
		Session: b.session(t, api),
		API:     api,
		Fetcher: fetcher,
		Manager: manager,
		UI:      ui,
	}
	return &harness{t: t, backend: b, app: a, fetcher: fetcher, ui: ui, downloads: downloads, opts: opts}
}

func (h *harness) run() error {
	h.t.Helper()
	return h.app.Run(context.Background())
}

// mediaTag matches the fake probe of the harness.
const harnessMediaTag = "German.AAC.1080p.Web-DL.x264"

// finalMoviePath is the expected final output of the harness movie fixture
// ("Test Film", 2020) with the harness media tag.
func (h *harness) finalMoviePath() string {
	stem := "Test.Film.2020"
	return filepath.Join(h.downloads, stem, stem+"."+harnessMediaTag+".mp4")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, data, want)
	}
}

// mp4Files lists every regular .mp4 file below root.
func mp4Files(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".mp4") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return files
}

func TestMovieFlowDownloadsAndFinalizes(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/m1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(func(operation string, variables map[string]any) (string, int) {
		if operation != "MovieDetails" {
			return graphQLErrorResponse("unexpected operation " + operation)
		}
		if got := variables["movieId"]; got != "m1" {
			t.Errorf("movieId variable = %v, want m1", got)
		}
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", "https://cdn.test/manifest.mpd"),
			"series": nil,
		})
	})

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	fetches := h.fetcher.fetchList()
	if len(fetches) != 1 {
		t.Fatalf("Fetch called %d times, want 1 (%v)", len(fetches), fetches)
	}
	if want := "https://cdn.test/manifest.mpd?hdnts=fake-token"; fetches[0] != want {
		t.Errorf("manifest URL = %q, want %q", fetches[0], want)
	}
	muxes := h.fetcher.muxList()
	if len(muxes) != 1 {
		t.Fatalf("Mux called %d times, want 1", len(muxes))
	}
	if want := filepath.Join(h.downloads, "Test Film (2020)", "Test Film"); muxes[0] != want {
		t.Errorf("mux output = %q, want %q", muxes[0], want)
	}

	final := h.finalMoviePath()
	assertFileContent(t, final, "fake media")
	if files := mp4Files(t, h.downloads); len(files) != 1 {
		t.Fatalf("mp4 files = %v, want exactly the final output", files)
	}
	if _, err := os.Stat(filepath.Join(h.downloads, "Test Film (2020)")); !os.IsNotExist(err) {
		t.Errorf("staging directory should have been removed, stat err = %v", err)
	}

	if got := h.ui.eventsOf(EventQueued); len(got) != 1 || got[0].Label != "Test Film" {
		t.Errorf("queued events = %+v, want one event labelled Test Film", got)
	}
	done := h.ui.eventsOf(EventDone)
	if len(done) != 1 || done[0].Path != final {
		t.Errorf("done events = %+v, want one event for %s", done, final)
	}
	progress := h.ui.eventsOf(EventProgress)
	if len(progress) == 0 || progress[0].Total != 2 {
		t.Errorf("progress events = %+v, want a 1/2 update", progress)
	}
	if logs := strings.Join(h.ui.logs(), "\n"); !strings.Contains(logs, "Lade Film Test Film (2020)/Test Film...") {
		t.Errorf("logs missing the Lade line:\n%s", logs)
	}
	// The playback session must be released again.
	h.backend.mu.Lock()
	defer h.backend.mu.Unlock()
	if len(h.backend.deleteIDs) != 1 || h.backend.deleteIDs[0] != "m1" {
		t.Errorf("delete playback ids = %v, want [m1]", h.backend.deleteIDs)
	}
	if want := 1; h.backend.sessionCalls != want {
		t.Errorf("session calls = %d, want %d", h.backend.sessionCalls, want)
	}
}

func TestMovieFlowWithoutDashURLFails(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/m1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(func(string, map[string]any) (string, int) {
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", ""),
			"series": nil,
		})
	})

	err := h.run()
	if err == nil || !strings.Contains(err.Error(), "no video URL in API response") {
		t.Fatalf("Run error = %v, want no video URL in API response", err)
	}
	if len(h.fetcher.fetchList()) != 0 {
		t.Errorf("Fetch must not run without a video URL")
	}
	if files := mp4Files(t, h.downloads); len(files) != 0 {
		t.Errorf("mp4 files = %v, want none", files)
	}
}

func TestMovieFlowAuthorizationFailureStopsDownload(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/m1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setPlayback("", true) // answers null, so the renewal also fails
	h.backend.setGraphQL(func(string, map[string]any) (string, int) {
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", "https://cdn.test/manifest.mpd"),
			"series": nil,
		})
	})

	err := h.run()
	if err == nil || !strings.Contains(err.Error(), "no playback authorization") {
		t.Fatalf("Run error = %v, want no playback authorization", err)
	}
	if len(h.fetcher.fetchList()) != 0 {
		t.Errorf("Fetch must not run without authorization")
	}
	if files := mp4Files(t, h.downloads); len(files) != 0 {
		t.Errorf("mp4 files = %v, want none", files)
	}
}

func TestMovieFlowMuxFailureLeavesNoOutput(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/m1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.fetcher.muxErr = errors.New("ffmpeg exploded")
	h.backend.setGraphQL(func(string, map[string]any) (string, int) {
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", "https://cdn.test/manifest.mpd"),
			"series": nil,
		})
	})

	err := h.run()
	if err == nil || !strings.Contains(err.Error(), "muxing failed for") {
		t.Fatalf("Run error = %v, want muxing failed for", err)
	}
	if files := mp4Files(t, h.downloads); len(files) != 0 {
		t.Errorf("mp4 files = %v, want none after a mux failure", files)
	}
}

func TestMovieFlowSkipsExistingOutput(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/m1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	final := h.finalMoviePath()
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.backend.setGraphQL(func(string, map[string]any) (string, int) {
		return dataResponse(map[string]any{
			"movie":  movieData("m1", "Test Film", "2020", "https://cdn.test/manifest.mpd"),
			"series": nil,
		})
	})

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.fetcher.fetchList()) != 0 {
		t.Errorf("Fetch must be skipped for an existing output")
	}
	logs := strings.Join(h.ui.logs(), "\n")
	if !strings.Contains(logs, "SKIP (already exists):") {
		t.Errorf("logs missing the skip line:\n%s", logs)
	}
	assertFileContent(t, final, "already here")
}

func seriesBackend(seasons map[string][]map[string]any) func(string, map[string]any) (string, int) {
	return func(operation string, variables map[string]any) (string, int) {
		switch operation {
		case "MovieDetails":
			nodes := make([]map[string]any, 0, len(seasons))
			for _, season := range []map[string]any{
				seasonData("season-1", 1, "Staffel 1"),
				seasonData("season-2", 2, "Staffel 2"),
			} {
				if _, ok := seasons[season["id"].(string)]; ok {
					nodes = append(nodes, season)
				}
			}
			return dataResponse(map[string]any{
				"movie":  nil,
				"series": seriesData("Test Show", nodes...),
			})
		case "EpisodesInSeasonData":
			seasonID, _ := variables["seasonId"].(string)
			return dataResponse(episodesData(seasons[seasonID]...))
		}
		return graphQLErrorResponse("unexpected operation " + operation)
	}
}

func TestSeriesFlowEpisodeFailureIsolation(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{
		"season-1": {
			episodeData("e1", 1, "Pilot", "https://cdn.test/e1.mpd"),
			episodeData("e2", 2, "Zweite", "https://cdn.test/e2.mpd"),
			episodeData("e3", 3, "Dritte", "https://cdn.test/e3.mpd"),
		},
	}))
	h.fetcher.onFetch = func(manifestURL, _ string) error {
		if strings.Contains(manifestURL, "e2.mpd") {
			return errors.New("segment failed")
		}
		return nil
	}

	err := h.run()
	if err == nil {
		t.Fatal("Run must fail when an episode fails")
	}
	want := "1 episode download(s) failed (first: download failed for Test Show/Season 01/Test Show S01E02 - Zweite: segment failed)"
	if err.Error() != want {
		t.Fatalf("Run error = %q, want %q", err.Error(), want)
	}
	// Every episode is attempted, so one failure does not stop the season.
	if got := len(h.fetcher.fetchList()); got != 3 {
		t.Errorf("Fetch calls = %d, want 3", got)
	}
	files := mp4Files(t, h.downloads)
	if len(files) != 2 {
		t.Fatalf("mp4 files = %v, want the two successful episodes", files)
	}
	seasonDir := "Test.Show.S01." + harnessMediaTag
	for _, name := range []string{
		filepath.Join(h.downloads, seasonDir, "Test.Show.S01E01."+harnessMediaTag+".mp4"),
		filepath.Join(h.downloads, seasonDir, "Test.Show.S01E03."+harnessMediaTag+".mp4"),
	} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	if logs := strings.Join(h.ui.logs(), "\n"); !strings.Contains(logs, "FEHLER: download failed for Test Show/Season 01/Test Show S01E02 - Zweite: segment failed") {
		t.Errorf("logs missing the FEHLER line:\n%s", logs)
	}
	if failures := h.ui.eventsOf(EventFailed); len(failures) != 1 {
		t.Errorf("failed events = %+v, want one", failures)
	}
}

func TestSeriesSkipAppliesOnlyToFirstSeason(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: true, Skip: 2}
	h := newHarness(t, opts)
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{
		"season-1": {
			episodeData("a1", 1, "Eins", "https://cdn.test/a1.mpd"),
			episodeData("a2", 2, "Zwei", "https://cdn.test/a2.mpd"),
			episodeData("a3", 3, "Drei", "https://cdn.test/a3.mpd"),
		},
		"season-2": {
			episodeData("b1", 1, "Vier", "https://cdn.test/b1.mpd"),
			episodeData("b2", 2, "Fuenf", "https://cdn.test/b2.mpd"),
		},
	}))

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fetches := h.fetcher.fetchList()
	if len(fetches) != 3 {
		t.Fatalf("Fetch calls = %d (%v), want 1 from season 1 plus 2 from season 2", len(fetches), fetches)
	}
	if strings.Contains(strings.Join(fetches, " "), "a1.mpd") || strings.Contains(strings.Join(fetches, " "), "a2.mpd") {
		t.Errorf("skipped episodes were downloaded: %v", fetches)
	}
	for _, want := range []string{"a3.mpd", "b1.mpd", "b2.mpd"} {
		found := false
		for _, fetch := range fetches {
			if strings.Contains(fetch, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in %v", want, fetches)
		}
	}
	logs := strings.Join(h.ui.logs(), "\n")
	if !strings.Contains(logs, "Überspringe 2 Episode(n) in der ersten Staffel.") {
		t.Errorf("logs missing the skip line:\n%s", logs)
	}
}

func TestSeriesSkipExistingEpisodeEmitsSkippedEvent(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{
		"season-1": {
			episodeData("e1", 1, "Pilot", "https://cdn.test/e1.mpd"),
			episodeData("e2", 2, "Zweite", "https://cdn.test/e2.mpd"),
		},
	}))

	// The finished output of the first episode makes Prepare report a skip.
	seasonDir := "Test.Show.S01." + harnessMediaTag
	final := filepath.Join(h.downloads, seasonDir, "Test.Show.S01E01."+harnessMediaTag+".mp4")
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	skipped := h.ui.eventsOf(EventSkipped)
	if len(skipped) != 1 {
		t.Fatalf("skipped events = %+v, want exactly one", skipped)
	}
	wantLabel := "Test Show/Season 01/Test Show S01E01 - Pilot"
	if skipped[0].Label != wantLabel {
		t.Errorf("skipped event label = %q, want %q", skipped[0].Label, wantLabel)
	}
	if fetches := h.fetcher.fetchList(); len(fetches) != 1 || !strings.Contains(fetches[0], "e2.mpd") {
		t.Errorf("Fetch calls = %v, want only the second episode", fetches)
	}
	assertFileContent(t, final, "already here")
}

func TestSeriesEpisodeNumberPadding(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{
		"season-1": {
			episodeData("e1", 1, "Erste", "https://cdn.test/e1.mpd"),
			episodeData("e100", 100, "Hundertste", "https://cdn.test/e100.mpd"),
		},
	}))

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	seasonDir := "Test.Show.S01." + harnessMediaTag
	for _, name := range []string{"Test.Show.S01E001", "Test.Show.S01E100"} {
		path := filepath.Join(h.downloads, seasonDir, name+"."+harnessMediaTag+".mp4")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", path, err)
		}
	}
}

func TestSeriesEmptySeasonFails(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{"season-1": {}}))

	err := h.run()
	if err == nil {
		t.Fatal("Run must fail for an empty season")
	}
	want := "1 episode download(s) failed (first: no episodes returned for season 1)"
	if err.Error() != want {
		t.Fatalf("Run error = %q, want %q", err.Error(), want)
	}
	if len(h.fetcher.fetchList()) != 0 {
		t.Errorf("Fetch must not run for an empty season")
	}
}

func TestSeriesAutomaticSelectsAllSeasons(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{
		"season-1": {episodeData("a1", 1, "Eins", "https://cdn.test/a1.mpd")},
		"season-2": {episodeData("b1", 1, "Zwei", "https://cdn.test/b2.mpd")},
	}))

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls := h.ui.seasonCalls; len(calls) != 0 {
		t.Errorf("SelectSeasons called in automatic mode: %+v", calls)
	}
	if got := len(h.fetcher.fetchList()); got != 2 {
		t.Errorf("Fetch calls = %d, want one episode per season", got)
	}
}

func TestSeriesInteractiveSelection(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/details/s1", Automatic: false, Skip: 1}
	h := newHarness(t, opts)
	h.ui.seasons = []int{2}
	h.backend.setGraphQL(seriesBackend(map[string][]map[string]any{
		"season-1": {episodeData("a1", 1, "Eins", "https://cdn.test/a1.mpd")},
		"season-2": {episodeData("b1", 1, "Zwei", "https://cdn.test/b2.mpd")},
	}))

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := h.ui.seasonCalls
	if len(calls) != 1 {
		t.Fatalf("SelectSeasons calls = %d, want 1", len(calls))
	}
	if calls[0].show != "Test Show" {
		t.Errorf("SelectSeasons show = %q, want Test Show", calls[0].show)
	}
	if len(calls[0].seasons) != 2 || calls[0].seasons[0].Number != 1 || calls[0].seasons[1].ID != "season-2" {
		t.Errorf("SelectSeasons seasons = %+v, want both seasons in order", calls[0].seasons)
	}
	fetches := h.fetcher.fetchList()
	if len(fetches) != 1 || !strings.Contains(fetches[0], "b2.mpd") {
		t.Errorf("Fetch calls = %v, want only season 2", fetches)
	}
	if got := len(h.ui.eventsOf(EventQueued)); got != 1 {
		t.Errorf("queued events = %d, want 1", got)
	}
}

func tatortHTML(urls ...string) string {
	items := make([]string, 0, len(urls))
	for _, url := range urls {
		items = append(items, fmt.Sprintf(`{"url":%q}`, url))
	}
	return "<html><head><script type=\"application/ld+json\">" +
		`{"itemListElement":[` + strings.Join(items, ",") + `]}` +
		"</script></head><body></body></html>"
}

func tatortBackend() func(string, map[string]any) (string, int) {
	return func(_ string, variables map[string]any) (string, int) {
		id, _ := variables["movieId"].(string)
		switch id {
		case "t1":
			return dataResponse(map[string]any{
				"movie":  tatortMovieData("t1", "Der Fall", "2023", "koeln", "Team A", "7", "https://cdn.test/t1.mpd"),
				"series": nil,
			})
		case "t2":
			return dataResponse(map[string]any{
				"movie":  tatortMovieData("t2", "Der zweite Fall", "2024", "koeln", "Team A", "8", "https://cdn.test/t2.mpd"),
				"series": nil,
			})
		}
		return graphQLErrorResponse("unknown tatort episode " + id)
	}
}

func TestTatortFlowAutomaticDownloadsEveryEpisode(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/kategorie/tatort-koeln", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setCategory(func(slug string) (string, int) {
		if slug != "tatort-koeln" {
			t.Errorf("category slug = %q, want tatort-koeln", slug)
		}
		return tatortHTML("https://www.ardplus.de/details/t1", "https://www.ardplus.de/details/t2"), http.StatusOK
	})
	h.backend.setGraphQL(tatortBackend())

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls := h.ui.tatortCalls; calls != 0 {
		t.Errorf("AskTatortSkip called %d times in automatic mode", calls)
	}
	fetches := h.fetcher.fetchList()
	if len(fetches) != 2 {
		t.Fatalf("Fetch calls = %v, want both episodes", fetches)
	}
	for _, name := range []string{
		"Tatort.koeln.Folge.7.2023." + harnessMediaTag + ".mp4",
		"Tatort.koeln.Folge.8.2024." + harnessMediaTag + ".mp4",
	} {
		path := filepath.Join(h.downloads, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", path, err)
		}
	}
	logs := strings.Join(h.ui.logs(), "\n")
	if !strings.Contains(logs, "Der Tatort Koeln hat 2 Episoden.") {
		t.Errorf("logs missing the Tatort count:\n%s", logs)
	}
	if !strings.Contains(logs, "Überspringe 0 Episode(n).") {
		t.Errorf("logs missing the skip line:\n%s", logs)
	}
}

func TestTatortFlowAppliesInteractiveSkipOffset(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/kategorie/tatort-koeln", Automatic: false, Skip: 1}
	h := newHarness(t, opts)
	h.ui.tatortSkip = 1
	h.backend.setCategory(func(string) (string, int) {
		return tatortHTML("https://www.ardplus.de/details/t1", "https://www.ardplus.de/details/t2"), http.StatusOK
	})
	h.backend.setGraphQL(tatortBackend())

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.ui.tatortCalls != 1 {
		t.Errorf("AskTatortSkip calls = %d, want 1", h.ui.tatortCalls)
	}
	fetches := h.fetcher.fetchList()
	if len(fetches) != 1 || !strings.Contains(fetches[0], "t2.mpd") {
		t.Fatalf("Fetch calls = %v, want only the second episode", fetches)
	}
	if _, err := os.Stat(filepath.Join(h.downloads, "Tatort.koeln.Folge.7.2023."+harnessMediaTag+".mp4")); !os.IsNotExist(err) {
		t.Errorf("skipped episode must not be downloaded, stat err = %v", err)
	}
	if logs := strings.Join(h.ui.logs(), "\n"); !strings.Contains(logs, "Überspringe 1 Episode(n).") {
		t.Errorf("logs missing the skip line:\n%s", logs)
	}
}

func TestTatortFlowRecordsUnparsableEpisodeURL(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/kategorie/tatort-koeln", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setCategory(func(string) (string, int) {
		return tatortHTML("https://www.ardplus.de/krimi/ohne-id"), http.StatusOK
	})
	h.backend.setGraphQL(tatortBackend())

	err := h.run()
	if err == nil {
		t.Fatal("Run must fail for an unparsable episode URL")
	}
	want := "1 episode download(s) failed (first: could not parse Tatort episode id from https://www.ardplus.de/krimi/ohne-id)"
	if err.Error() != want {
		t.Fatalf("Run error = %q, want %q", err.Error(), want)
	}
	if len(h.fetcher.fetchList()) != 0 {
		t.Errorf("Fetch must not run without a content id")
	}
}

func writeLinks(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "links.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func batchLogs(t *testing.T, linksPath string) (success, failed, download []string) {
	t.Helper()
	logsDir := filepath.Join(filepath.Dir(linksPath), "logs")
	read := func(pattern string) []string {
		matches, err := filepath.Glob(filepath.Join(logsDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("%s matched %v, want exactly one log file", pattern, matches)
		}
		data, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("reading %s: %v", matches[0], err)
		}
		text := strings.TrimRight(string(data), "\n")
		if text == "" {
			return nil
		}
		return strings.Split(text, "\n")
	}
	return read("successful_links_*.txt"), read("failed_links_*.txt"), read("download_log_*.txt")
}

func TestBatchRecordsSuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	linksPath := writeLinks(t, dir,
		"# a comment\n\n  https://www.ardplus.de/details/ok  \n"+
			"https://www.ardplus.de/details/bad\r\n"+
			"https://example.com/details/ignored\n")
	opts := &config.Options{LinksFile: linksPath, Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(func(_ string, variables map[string]any) (string, int) {
		if variables["movieId"] == "ok" {
			return dataResponse(map[string]any{
				"movie":  movieData("ok", "Guter Film", "2021", "https://cdn.test/ok.mpd"),
				"series": nil,
			})
		}
		return graphQLErrorResponse("kaputt")
	})

	err := h.run()
	if err == nil {
		t.Fatal("Run must fail when a link fails")
	}
	if err.Error() != "2 link(s) failed" {
		t.Fatalf("Run error = %q, want 2 link(s) failed", err.Error())
	}
	success, failed, download := batchLogs(t, linksPath)
	if len(success) != 1 || success[0] != "https://www.ardplus.de/details/ok" {
		t.Errorf("success log = %v, want the normalized ok link", success)
	}
	wantFailed := []string{
		"https://www.ardplus.de/details/bad\tcould not fetch content metadata (graphql: kaputt)",
		"https://example.com/details/ignored\tinvalid URL: unsupported host: example.com",
	}
	if len(failed) != 2 || failed[0] != wantFailed[0] || failed[1] != wantFailed[1] {
		t.Errorf("failed log = %v, want %v", failed, wantFailed)
	}

	// The download log mirrors the batch lines but not the "Downloads
	// directory" line, which the Bash version prints before opening the log.
	downloadText := strings.Join(download, "\n")
	for _, want := range []string{
		"Batch download started at",
		"Processing: https://www.ardplus.de/details/ok",
		"Recorded success: https://www.ardplus.de/details/ok",
		"Recorded failure: https://example.com/details/ignored (invalid URL: unsupported host: example.com)",
		"Batch download finished.",
	} {
		if !strings.Contains(downloadText, want) {
			t.Errorf("download log missing %q:\n%s", want, downloadText)
		}
	}
	if strings.Contains(downloadText, "Downloads directory:") {
		t.Errorf("download log must not contain the Downloads directory line:\n%s", downloadText)
	}
	logs := strings.Join(h.ui.logs(), "\n")
	for _, want := range []string{
		"Batch download started at",
		"Processing: https://www.ardplus.de/details/ok",
		"Recorded success: https://www.ardplus.de/details/ok",
		"Recorded failure: https://example.com/details/ignored (invalid URL: unsupported host: example.com)",
		"Batch download finished.",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
	// The successful link produced a movie.
	if files := mp4Files(t, h.downloads); len(files) != 1 {
		t.Errorf("mp4 files = %v, want one movie", files)
	}
}

func TestBatchAllLinksSucceed(t *testing.T) {
	dir := t.TempDir()
	linksPath := writeLinks(t, dir, "https://www.ardplus.de/details/ok\n# done\n")
	opts := &config.Options{LinksFile: linksPath, Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setGraphQL(func(_ string, variables map[string]any) (string, int) {
		return dataResponse(map[string]any{
			"movie":  movieData(variables["movieId"].(string), "Guter Film", "2021", "https://cdn.test/ok.mpd"),
			"series": nil,
		})
	})

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	success, failed, download := batchLogs(t, linksPath)
	if len(success) != 1 || success[0] != "https://www.ardplus.de/details/ok" {
		t.Errorf("success log = %v, want the ok link", success)
	}
	if len(failed) != 0 {
		t.Errorf("failed log = %v, want no failures", failed)
	}
	if downloadText := strings.Join(download, "\n"); !strings.Contains(downloadText, "Recorded success: https://www.ardplus.de/details/ok") {
		t.Errorf("download log = %q, want the recorded success", downloadText)
	}
	if got := len(h.fetcher.fetchList()); got != 1 {
		t.Errorf("Fetch calls = %d, want 1", got)
	}
}

func TestBatchEmptyLinksFileSucceeds(t *testing.T) {
	dir := t.TempDir()
	linksPath := writeLinks(t, dir, "# nothing to do\n\n")
	opts := &config.Options{LinksFile: linksPath, Automatic: true, Skip: 1}
	h := newHarness(t, opts)

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if logs := strings.Join(h.ui.logs(), "\n"); !strings.Contains(logs, "Batch download finished.") {
		t.Errorf("logs missing the batch footer:\n%s", logs)
	}
}

func TestReadLinksParsesLines(t *testing.T) {
	dir := t.TempDir()
	path := writeLinks(t, dir, "# comment\n\n  https://www.ardplus.de/details/a  \nhttps://www.ardplus.de/details/b#trailing comment\r\n\n")
	links, err := readLinks(path)
	if err != nil {
		t.Fatalf("readLinks: %v", err)
	}
	want := []string{"https://www.ardplus.de/details/a", "https://www.ardplus.de/details/b"}
	if len(links) != len(want) {
		t.Fatalf("links = %v, want %v", links, want)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("links[%d] = %q, want %q", i, links[i], want[i])
		}
	}
}

func TestReadLinksMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := readLinks(path)
	if err == nil || err.Error() != "Links file missing or not found: "+path {
		t.Fatalf("readLinks error = %v, want Links file missing or not found", err)
	}
}

// An episode without a production number must still be recognised as
// downloaded on the next run.
func TestTatortFlowSkipsAnEpisodeWithoutProductionNumber(t *testing.T) {
	opts := &config.Options{URL: "https://www.ardplus.de/kategorie/tatort-koeln", Automatic: true, Skip: 1}
	h := newHarness(t, opts)
	h.backend.setCategory(func(string) (string, int) {
		return tatortHTML("https://www.ardplus.de/details/t1"), http.StatusOK
	})
	h.backend.setGraphQL(func(_ string, variables map[string]any) (string, int) {
		id, _ := variables["movieId"].(string)
		if id != "t1" {
			return graphQLErrorResponse("unknown tatort episode " + id)
		}
		// No episode production number: the file name is built from the
		// location and the year alone.
		return dataResponse(map[string]any{
			"movie":  tatortMovieData("t1", "Der Fall", "2023", "koeln", "Team A", "", "https://cdn.test/t1.mpd"),
			"series": nil,
		})
	})

	if err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fetches := h.fetcher.fetchList(); len(fetches) != 1 {
		t.Fatalf("Fetch calls = %v, want one episode", fetches)
	}
	final := filepath.Join(h.downloads, "Tatort.koeln.2023."+harnessMediaTag+".mp4")
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("missing %s: %v", final, err)
	}

	// The second run must skip the episode instead of downloading it again.
	h.fetcher.reset()
	if err := h.run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if fetches := h.fetcher.fetchList(); len(fetches) != 0 {
		t.Fatalf("the second run must not download the episode again, fetches = %v", fetches)
	}
}
