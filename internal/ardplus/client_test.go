package ardplus

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func noSleep(context.Context, time.Duration) {}

// step is one scripted HTTP answer.
type step struct {
	status         int
	body           string
	header         http.Header
	transportError bool
}

// sequenceServer replays the steps of a script; the last step repeats.
type sequenceServer struct {
	mu      sync.Mutex
	steps   []step
	calls   int
	bodies  []string
	paths   []string
	urls    []string
	methods []string
	header  http.Header
}

func newSequenceServer(t *testing.T, steps ...step) (*sequenceServer, *httptest.Server) {
	t.Helper()
	seq := &sequenceServer{steps: steps, header: http.Header{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seq.mu.Lock()
		idx := seq.calls
		seq.calls++
		seq.bodies = append(seq.bodies, string(body))
		seq.urls = append(seq.urls, r.URL.String())
		seq.methods = append(seq.methods, r.Method)
		seq.paths = append(seq.paths, r.URL.Path)
		for key, values := range r.Header {
			seq.header[key] = append([]string(nil), values...)
		}
		if idx >= len(seq.steps) {
			idx = len(seq.steps) - 1
		}
		current := seq.steps[idx]
		seq.mu.Unlock()

		if current.transportError {
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			panic("cannot hijack the test connection")
		}
		for key, values := range current.header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(current.status)
		_, _ = io.WriteString(w, current.body)
	}))
	t.Cleanup(srv.Close)
	return seq, srv
}

func (s *sequenceServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *sequenceServer) lastBody() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return ""
	}
	return s.bodies[len(s.bodies)-1]
}

func newClient(srv *httptest.Server, logs *[]string) *Client {
	client := &Client{
		HTTP:        srv.Client(),
		GraphQLURL:  srv.URL + "/ard/graphql",
		TokenURL:    srv.URL,
		CategoryURL: srv.URL + "/kategorie",
		Sleep:       noSleep,
	}
	client.SetToken("stub-token")
	if logs != nil {
		client.Logf = func(format string, args ...any) {
			*logs = append(*logs, fmt.Sprintf(format, args...))
		}
	}
	return client
}

func assertGraphQLRequest(t *testing.T, seq *sequenceServer, operation, hash string, wantVariables map[string]string) {
	t.Helper()
	if len(seq.urls) != 1 || len(seq.methods) != 1 {
		t.Fatalf("requests = %d, want 1", len(seq.urls))
	}
	if seq.methods[0] != http.MethodGet {
		t.Fatalf("method = %q, want GET", seq.methods[0])
	}
	reqURL, err := url.Parse(seq.urls[0])
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}
	if reqURL.Path != "/ard/graphql" {
		t.Fatalf("path = %q", reqURL.Path)
	}
	query := reqURL.Query()
	if got := query.Get("operationName"); got != operation {
		t.Fatalf("operationName = %q, want %q", got, operation)
	}
	var extensions struct {
		PersistedQuery struct {
			Version    int    `json:"version"`
			SHA256Hash string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	}
	if err := json.Unmarshal([]byte(query.Get("extensions")), &extensions); err != nil {
		t.Fatalf("parse persisted-query extension: %v", err)
	}
	if extensions.PersistedQuery.Version != 1 || extensions.PersistedQuery.SHA256Hash != hash {
		t.Fatalf("persisted query = %+v, want version 1 hash %q", extensions.PersistedQuery, hash)
	}
	var variables map[string]string
	if err := json.Unmarshal([]byte(query.Get("variables")), &variables); err != nil {
		t.Fatalf("parse variables: %v", err)
	}
	if len(variables) != len(wantVariables) {
		t.Fatalf("variables = %v, want %v", variables, wantVariables)
	}
	for key, value := range wantVariables {
		if variables[key] != value {
			t.Errorf("variable %s = %q, want %q", key, variables[key], value)
		}
	}
	if got := seq.header.Get("Cookie"); got != "sid=stub-token" {
		t.Errorf("Cookie = %q", got)
	}
	if got := seq.header.Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want absent for GET", got)
	}
	if got := seq.header.Get("Origin"); got != "https://www.ardplus.de" {
		t.Errorf("Origin = %q", got)
	}
	if got := seq.header.Get("Referer"); got != "https://www.ardplus.de/" {
		t.Errorf("Referer = %q", got)
	}
	if got := seq.header.Get("User-Agent"); !strings.Contains(got, "Chrome/") {
		t.Errorf("User-Agent = %q", got)
	}
}

const movieDataOK = `{"data":{"movie":{"id":"m1","title":"Test","productionYear":"2020"}}}`

func TestGraphQLPermanentErrorIsNotRetried(t *testing.T) {
	var logs []string
	seq, srv := newSequenceServer(t, step{status: 200, body: `{"errors":[{"message":"Movie not found"}]}`})
	client := newClient(srv, &logs)

	_, _, err := client.MovieDetails(context.Background(), "abc")
	if err == nil {
		t.Fatal("a permanent API error must fail")
	}
	if err.Error() != "Movie not found" {
		t.Fatalf("error = %q, want the API message", err.Error())
	}
	if seq.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", seq.callCount())
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "permanently") {
		t.Fatalf("logs = %v, want a permanent-failure line", logs)
	}
	if strings.Contains(joined, "attempt ") {
		t.Fatalf("logs = %v, a permanent failure must not be logged as a retry", logs)
	}
}

func TestGraphQLForbiddenIsNotRetried(t *testing.T) {
	var logs []string
	seq, srv := newSequenceServer(t, step{status: http.StatusForbidden, body: "forbidden"})
	client := newClient(srv, &logs)

	_, _, err := client.MovieDetails(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("MovieDetails error = %v, want permanent HTTP 403", err)
	}
	if seq.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", seq.callCount())
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "not retrying") || strings.Contains(joined, "drosselt") {
		t.Fatalf("logs = %v, 403 must be reported as forbidden rather than throttling", logs)
	}
}

func TestGraphQLTooManyRequestsIsRetried(t *testing.T) {
	seq, srv := newSequenceServer(t,
		step{status: http.StatusTooManyRequests, body: `{"errors":[{"message":"rate limited"}]}`},
		step{status: http.StatusOK, body: movieDataOK},
	)
	var delays []time.Duration
	client := newClient(srv, nil)
	client.Sleep = func(_ context.Context, delay time.Duration) { delays = append(delays, delay) }

	if _, _, err := client.MovieDetails(context.Background(), "abc"); err != nil {
		t.Fatalf("MovieDetails after 429: %v", err)
	}
	if seq.callCount() != 2 || len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("calls = %d, delays = %v, want two requests after a 2s wait", seq.callCount(), delays)
	}
}

func TestGraphQLTransportErrorIsRetried(t *testing.T) {
	var logs []string
	seq, srv := newSequenceServer(t,
		step{transportError: true},
		step{status: 200, body: movieDataOK},
	)
	client := newClient(srv, &logs)

	movie, _, err := client.MovieDetails(context.Background(), "abc")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if movie == nil || movie.ID != "m1" {
		t.Fatalf("movie = %+v", movie)
	}
	if seq.callCount() != 2 {
		t.Fatalf("calls = %d, want 2", seq.callCount())
	}
	if !strings.Contains(strings.Join(logs, "\n"), "attempt 1/5") {
		t.Fatalf("logs = %v, want a retry line for attempt 1/5", logs)
	}
}

func TestGraphQLServerErrorIsRetried(t *testing.T) {
	var logs []string
	seq, srv := newSequenceServer(t,
		step{status: 503, body: "unavailable"},
		step{status: 200, body: movieDataOK},
	)
	client := newClient(srv, &logs)

	movie, _, err := client.MovieDetails(context.Background(), "abc")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if movie == nil || movie.ID != "m1" {
		t.Fatalf("movie = %+v", movie)
	}
	if seq.callCount() != 2 {
		t.Fatalf("calls = %d, want 2", seq.callCount())
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "HTTP 503") {
		t.Fatalf("logs = %v, want the HTTP status in the retry line", logs)
	}
}

func TestGraphQLEmptyDataIsRetried(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"null data", `{"data":null}`},
		{"empty object data", `{"data":{}}`},
		{"all null values", `{"data":{"movie":null,"series":null}}`},
		{"errors without message", `{"errors":[{"message":""}],"data":null}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seq, srv := newSequenceServer(t, step{status: 200, body: tc.body})
			client := newClient(srv, nil)
			client.Attempts = 2

			_, _, err := client.MovieDetails(context.Background(), "abc")
			if err == nil {
				t.Fatal("empty data must fail after the retry budget")
			}
			if seq.callCount() != 2 {
				t.Fatalf("calls = %d, want 2 (the response must be retried)", seq.callCount())
			}
		})
	}
}

func TestGraphQLRetryBudget(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 500, body: ""})
	client := newClient(srv, nil)
	client.Attempts = 3

	if _, _, err := client.MovieDetails(context.Background(), "abc"); err == nil {
		t.Fatal("a permanently failing endpoint must fail")
	}
	if seq.callCount() != 3 {
		t.Fatalf("calls = %d, want the configured 3 attempts", seq.callCount())
	}
}

func TestGraphQLRetryBackoff(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 500, body: ""})
	var delays []time.Duration
	client := newClient(srv, nil)
	client.Sleep = func(_ context.Context, d time.Duration) { delays = append(delays, d) }

	if _, _, err := client.MovieDetails(context.Background(), "abc"); err == nil {
		t.Fatal("a permanently failing endpoint must fail")
	}
	if seq.callCount() != 5 {
		t.Fatalf("calls = %d, want the default 5 attempts", seq.callCount())
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(delays) != len(want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delay %d = %v, want %v", i, delays[i], want[i])
		}
	}
}

func TestGraphQLDataOK(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"empty", ``, false},
		{"null", `null`, false},
		{"object with one value", `{"movie":{"id":"m"}}`, true},
		{"object with all nulls", `{"movie":null}`, false},
		{"array", `[1]`, true},
		{"scalar", `"text"`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := graphQLDataOK(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("graphQLDataOK(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestGraphQLDataOKFixtures(t *testing.T) {
	tests := []struct {
		file string
		want bool
	}{
		{"graphql-ok.json", true},
		{"graphql-error.json", false},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var response graphQLResponse
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			if got := graphQLDataOK(response.Data) && len(response.Errors) == 0; got != tc.want {
				t.Fatalf("fixture %s accepted = %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestGraphQLRequestShape(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 200, body: movieDataOK})
	client := newClient(srv, nil)

	if _, _, err := client.MovieDetails(context.Background(), "abc123"); err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	assertGraphQLRequest(t, seq, operationMovieDetails, movieDetailsHash, map[string]string{
		"movieId":          "abc123",
		"externalId":       "abc123",
		"slug":             "abc123",
		"potentialMovieId": "abc123",
	})
}

func TestEpisodesInSeasonRequestShape(t *testing.T) {
	body := `{"data":{"episodes":{"nodes":[{"id":"e1","title":"Folge 1","episodeInSeason":1}]}}}`
	seq, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	episodes, err := client.Episodes(context.Background(), "season-guid")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].ID != "e1" {
		t.Fatalf("episodes = %+v", episodes)
	}
	assertGraphQLRequest(t, seq, operationEpisodes, episodesInSeasonHash, map[string]string{"seasonId": "season-guid"})
}

func TestEpisodesInSeasonPersistedQueryFallback(t *testing.T) {
	persistedError := `{"errors":[{"message":"PersistedQueryNotFound","extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`
	body := `{"data":{"episodes":{"nodes":[{"id":"e1","episodeInSeason":1,"title":"Folge 1"}]}}}`
	seq, srv := newSequenceServer(t,
		step{status: http.StatusOK, body: persistedError},
		step{status: http.StatusOK, body: body},
	)
	client := newClient(srv, nil)

	episodes, err := client.Episodes(context.Background(), "season-guid")
	if err != nil {
		t.Fatalf("Episodes with persisted-query fallback: %v", err)
	}
	if len(episodes) != 1 || episodes[0].ID != "e1" || episodes[0].Title != "Folge 1" {
		t.Fatalf("episodes = %+v", episodes)
	}
	if len(seq.urls) != 2 || len(seq.methods) != 2 {
		t.Fatalf("requests = %v, want a GET followed by a POST", seq.methods)
	}

	first, err := url.Parse(seq.urls[0])
	if err != nil {
		t.Fatalf("parse first request URL: %v", err)
	}
	if seq.methods[0] != http.MethodGet {
		t.Fatalf("first method = %q, want GET", seq.methods[0])
	}
	var extensions struct {
		PersistedQuery struct {
			SHA256Hash string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	}
	if err := json.Unmarshal([]byte(first.Query().Get("extensions")), &extensions); err != nil {
		t.Fatalf("parse persisted-query extension: %v", err)
	}
	if extensions.PersistedQuery.SHA256Hash != episodesInSeasonHash {
		t.Fatalf("first hash = %q, want %q", extensions.PersistedQuery.SHA256Hash, episodesInSeasonHash)
	}

	if seq.methods[1] != http.MethodPost {
		t.Fatalf("second method = %q, want POST", seq.methods[1])
	}
	if got := seq.header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var retry struct {
		OperationName string            `json:"operationName"`
		Query         string            `json:"query"`
		Variables     map[string]string `json:"variables"`
	}
	if err := json.Unmarshal([]byte(seq.lastBody()), &retry); err != nil {
		t.Fatalf("parse fallback body: %v", err)
	}
	if retry.OperationName != operationEpisodes {
		t.Fatalf("operationName = %q, want %q", retry.OperationName, operationEpisodes)
	}
	if retry.Variables["seasonId"] != "season-guid" {
		t.Fatalf("seasonId = %q, want %q", retry.Variables["seasonId"], "season-guid")
	}
	if !strings.Contains(retry.Query, "query EpisodesInSeasonData") || !strings.Contains(retry.Query, "fragment EpisodeDetailSliderData") {
		t.Fatalf("query does not contain the full EpisodesInSeasonData document: %s", retry.Query)
	}
}

func TestEpisodesInSeasonNoPersistedQueryFallback(t *testing.T) {
	body := `{"data":{"episodes":{"nodes":[{"id":"e1","title":"Folge 1","episodeInSeason":1}]}}}`
	seq, srv := newSequenceServer(t, step{status: http.StatusOK, body: body})
	client := newClient(srv, nil)

	if _, err := client.Episodes(context.Background(), "season-guid"); err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if seq.callCount() != 1 {
		t.Fatalf("calls = %d, want 1 without a persisted-query error", seq.callCount())
	}
	if seq.methods[0] != http.MethodGet {
		t.Fatalf("method = %q, want GET", seq.methods[0])
	}
}

func TestMovieDetailsPersistedQueryFallback(t *testing.T) {
	persistedError := `{"errors":[{"message":"PersistedQueryNotFound","extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`
	body := `{"data":{"movie":{"id":"m1","title":"Tatort: Test","productionYear":"2024",
	  "customData":{"team":"Team A","location":"Köln","episodeProductionNumber":"12"},
	  "videoSource":{"dashUrl":"https://example.test/fallback.mpd"}}}}`
	seq, srv := newSequenceServer(t,
		step{status: http.StatusOK, body: persistedError},
		step{status: http.StatusOK, body: body},
	)
	client := newClient(srv, nil)

	movie, _, err := client.MovieDetails(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("MovieDetails with persisted-query fallback: %v", err)
	}
	if movie == nil || movie.ID != "m1" || movie.Title != "Tatort: Test" {
		t.Fatalf("movie = %+v", movie)
	}
	if movie.ProductionYear != "2024" || movie.DashURL != "https://example.test/fallback.mpd" {
		t.Fatalf("movie = %+v", movie)
	}
	if movie.CustomData.Team != "Team A" || movie.CustomData.Location != "Köln" || movie.CustomData.EpisodeProductionNumber != "12" {
		t.Fatalf("customData = %+v", movie.CustomData)
	}
	if len(seq.urls) != 2 || len(seq.methods) != 2 {
		t.Fatalf("requests = %v, want a GET followed by a POST", seq.methods)
	}

	first, err := url.Parse(seq.urls[0])
	if err != nil {
		t.Fatalf("parse first request URL: %v", err)
	}
	if seq.methods[0] != http.MethodGet {
		t.Fatalf("first method = %q, want GET", seq.methods[0])
	}
	if got := first.Query().Get("operationName"); got != operationMovieDetails {
		t.Fatalf("first operationName = %q, want %q", got, operationMovieDetails)
	}
	var extensions struct {
		PersistedQuery struct {
			SHA256Hash string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	}
	if err := json.Unmarshal([]byte(first.Query().Get("extensions")), &extensions); err != nil {
		t.Fatalf("parse persisted-query extension: %v", err)
	}
	if extensions.PersistedQuery.SHA256Hash != movieDetailsHash {
		t.Fatalf("first hash = %q, want %q", extensions.PersistedQuery.SHA256Hash, movieDetailsHash)
	}

	if seq.methods[1] != http.MethodPost {
		t.Fatalf("second method = %q, want POST", seq.methods[1])
	}
	if got := seq.header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var retry struct {
		OperationName string            `json:"operationName"`
		Query         string            `json:"query"`
		Variables     map[string]string `json:"variables"`
	}
	if err := json.Unmarshal([]byte(seq.lastBody()), &retry); err != nil {
		t.Fatalf("parse fallback body: %v", err)
	}
	if retry.OperationName != operationMovieDetails {
		t.Fatalf("operationName = %q, want %q", retry.OperationName, operationMovieDetails)
	}
	wantVariables := map[string]string{
		"movieId":          "abc123",
		"externalId":       "abc123",
		"slug":             "abc123",
		"potentialMovieId": "abc123",
	}
	if len(retry.Variables) != len(wantVariables) {
		t.Fatalf("variables = %v, want %v", retry.Variables, wantVariables)
	}
	for key, want := range wantVariables {
		if retry.Variables[key] != want {
			t.Errorf("variable %s = %q, want %q", key, retry.Variables[key], want)
		}
	}
	if !strings.Contains(retry.Query, "query MovieDetails") || !strings.Contains(retry.Query, "fragment MovieDetailData") {
		t.Fatalf("query does not contain the full MovieDetails document: %s", retry.Query)
	}
}

func TestMovieDetailsNoPersistedQueryFallback(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: http.StatusOK, body: movieDataOK})
	client := newClient(srv, nil)

	movie, _, err := client.MovieDetails(context.Background(), "abc")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if movie == nil || movie.ID != "m1" {
		t.Fatalf("movie = %+v", movie)
	}
	if seq.callCount() != 1 {
		t.Fatalf("calls = %d, want 1 without a persisted-query error", seq.callCount())
	}
	if seq.methods[0] != http.MethodGet {
		t.Fatalf("method = %q, want GET", seq.methods[0])
	}
}

func TestMovieDetailsParsing(t *testing.T) {
	body := `{"data":{"movie":{"id":"m1","title":"Der Test","productionYear":2021,
	  "customData":{"team":"Team A","location":"Köln","episodeProductionNumber":"12"},
	  "videoSource":{"dashUrl":"https://example.test/manifest.mpd?token=abc"}},
	  "series":{"title":"Serie","seasons":{"nodes":[{"id":"s1","title":"Staffel 1","seasonInSeries":1}]}}}}`
	_, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	movie, series, err := client.MovieDetails(context.Background(), "abc")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if movie == nil {
		t.Fatal("movie is nil")
	}
	if movie.ID != "m1" || movie.Title != "Der Test" {
		t.Fatalf("movie = %+v", movie)
	}
	if movie.ProductionYear != "2021" {
		t.Fatalf("productionYear = %q, want the numeric value as string", movie.ProductionYear)
	}
	if movie.CustomData.Team != "Team A" || movie.CustomData.Location != "Köln" || movie.CustomData.EpisodeProductionNumber != "12" {
		t.Fatalf("customData = %+v", movie.CustomData)
	}
	if movie.DashURL != "https://example.test/manifest.mpd?token=abc" {
		t.Fatalf("dashUrl = %q", movie.DashURL)
	}
	if series == nil || series.Title != "Serie" || len(series.Seasons) != 1 {
		t.Fatalf("series = %+v", series)
	}
	if series.Seasons[0].ID != "s1" || series.Seasons[0].SeasonInSeries != 1 {
		t.Fatalf("season = %+v", series.Seasons[0])
	}
}

func TestMovieDetailsNullFields(t *testing.T) {
	body := `{"data":{"movie":{"id":"m1","title":"Ohne Jahr","productionYear":null,"customData":null,"videoSource":null}}}`
	_, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	movie, series, err := client.MovieDetails(context.Background(), "abc")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if series != nil {
		t.Fatalf("series = %+v, want nil", series)
	}
	if movie.ProductionYear != "" {
		t.Fatalf("productionYear = %q, want empty", movie.ProductionYear)
	}
	if movie.DashURL != "" {
		t.Fatalf("dashUrl = %q, want empty", movie.DashURL)
	}
	if movie.CustomData != (CustomData{}) {
		t.Fatalf("customData = %+v, want the zero value", movie.CustomData)
	}
}

func TestMovieDetailsInvalidContent(t *testing.T) {
	body := `{"data":{"movie":null,"series":null,"extra":true}}`
	_, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	_, _, err := client.MovieDetails(context.Background(), "abc")
	if err == nil {
		t.Fatal("an empty response must fail")
	}
	if err.Error() != "invalid content (no movie or series in API response)" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestEpisodesParsing(t *testing.T) {
	body := `{"data":{"episodes":{"nodes":[
	  {"id":"e1","episodeInSeason":3,"title":"Drei","videoSource":{"dashUrl":"https://example.test/3.mpd"}},
	  {"id":"e2","episodeInSeason":null,"title":"Ohne Nummer"},
	  {"id":"e3","episodeInSeason":"4","title":"Vier"}
	]}}}`
	_, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	episodes, err := client.Episodes(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(episodes) != 3 {
		t.Fatalf("episodes = %+v", episodes)
	}
	if episodes[0].EpisodeInSeason == nil || *episodes[0].EpisodeInSeason != 3 {
		t.Fatalf("episode 1 number = %v", episodes[0].EpisodeInSeason)
	}
	if episodes[0].DashURL != "https://example.test/3.mpd" {
		t.Fatalf("episode 1 dashUrl = %q", episodes[0].DashURL)
	}
	if episodes[1].EpisodeInSeason != nil {
		t.Fatalf("null episodeInSeason = %v, want nil", *episodes[1].EpisodeInSeason)
	}
	if episodes[1].Title != "Ohne Nummer" {
		t.Fatalf("episode 2 title = %q", episodes[1].Title)
	}
	if episodes[2].EpisodeInSeason == nil || *episodes[2].EpisodeInSeason != 4 {
		t.Fatalf("string episodeInSeason = %v, want 4", episodes[2].EpisodeInSeason)
	}
}

func TestEpisodesEmptySeason(t *testing.T) {
	_, srv := newSequenceServer(t, step{status: 200, body: `{"data":{"episodes":{"nodes":[]}},"extra":1}`})
	client := newClient(srv, nil)
	episodes, err := client.Episodes(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("episodes = %+v, want none", episodes)
	}
}

func TestPlaybackAuthPayload(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 200, body: `{"authorizationParams":"token=abc"}`})
	client := newClient(srv, nil)

	param, err := client.PlaybackAuth(context.Background(), "abc123", "CmsMovie")
	if err != nil {
		t.Fatalf("PlaybackAuth: %v", err)
	}
	if param != "token=abc" {
		t.Fatalf("param = %q", param)
	}
	want := `{"contentId":"abc123","contentType":"CmsMovie","download":false,` +
		`"appInfo":{"platform":"web","appVersion":"1.0.0","build":"web","bundleIdentifier":"web"},` +
		`"deviceInfo":{"isTouchDevice":false,"isTablet":false,"isFireOS":false,"appPlatform":"web",` +
		`"isIOS":false,"isCastReceiver":false,"isSafari":false,"isFirefox":false}}`
	if got := seq.lastBody(); got != want {
		t.Fatalf("payload = %s\nwant  = %s", got, want)
	}
	if seq.paths[0] != "/token/session" {
		t.Fatalf("path = %q", seq.paths[0])
	}
	if got := seq.header.Get("Cookie"); got != "sid=stub-token" {
		t.Fatalf("Cookie = %q", got)
	}
	if got := seq.header.Get("Origin"); got != "https://www.ardplus.de" {
		t.Fatalf("Origin = %q", got)
	}
}

// A gzip-compressed answer must be unpacked. Go's transport only does that
// when it added the Accept-Encoding header itself, which is why the client
// must not set that header by hand.
func TestPlaybackAuthGzipResponse(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write([]byte(`{"authorizationParams":"token=abc"}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	seq, srv := newSequenceServer(t, step{
		status: 200,
		body:   compressed.String(),
		header: http.Header{"Content-Encoding": []string{"gzip"}},
	})
	client := newClient(srv, nil)

	param, err := client.PlaybackAuth(context.Background(), "abc123", "CmsMovie")
	if err != nil {
		t.Fatalf("PlaybackAuth: %v", err)
	}
	if param != "token=abc" {
		t.Fatalf("param = %q, want %q", param, "token=abc")
	}
	if seq.paths[0] != "/token/session" {
		t.Fatalf("path = %q", seq.paths[0])
	}
}

// The client must not set Accept-Encoding itself: Go only decompresses a gzip
// answer transparently when its own transport added the header. The
// recording transport sees the request before the transport touches it.
func TestPlaybackAuthDoesNotForceAcceptEncoding(t *testing.T) {
	_, srv := newSequenceServer(t, step{status: 200, body: `{"authorizationParams":"token=abc"}`})
	recorder := &recordingTransport{base: srv.Client().Transport}
	client := newClient(srv, nil)
	client.HTTP = &http.Client{Transport: recorder}

	if _, err := client.PlaybackAuth(context.Background(), "abc123", "CmsMovie"); err != nil {
		t.Fatalf("PlaybackAuth: %v", err)
	}
	if got := recorder.acceptEncoding(); got != "" {
		t.Fatalf("Accept-Encoding = %q, want it to be left to the transport", got)
	}
}

// recordingTransport remembers the Accept-Encoding header of the request as it
// was before the underlying transport processed it.
type recordingTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	seen string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.seen = req.Header.Get("Accept-Encoding")
	r.mu.Unlock()
	return r.base.RoundTrip(req)
}

func (r *recordingTransport) acceptEncoding() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

func TestPlaybackAuthNullParams(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"null", `{"authorizationParams":null}`},
		{"absent", `{}`},
		{"explicit empty string", `{"authorizationParams":""}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := newSequenceServer(t, step{status: 200, body: tc.body})
			client := newClient(srv, nil)
			param, err := client.PlaybackAuth(context.Background(), "abc123", "CmsMovie")
			if err != nil {
				t.Fatalf("PlaybackAuth: %v", err)
			}
			if param != "" {
				t.Fatalf("param = %q, want empty", param)
			}
		})
	}
}

func TestPlaybackAuthWithoutContent(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 200, body: `{"authorizationParams":"token=abc"}`})
	client := newClient(srv, nil)

	param, err := client.PlaybackAuth(context.Background(), "", "")
	if err != nil {
		t.Fatalf("PlaybackAuth: %v", err)
	}
	if param != "" {
		t.Fatalf("param = %q, want empty", param)
	}
	if seq.callCount() != 0 {
		t.Fatalf("calls = %d, want 0", seq.callCount())
	}
}

func TestDeletePlaybackPayload(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 200, body: `{}`})
	client := newClient(srv, nil)

	if err := client.DeletePlayback(context.Background(), "abc123", "CmsEpisode"); err != nil {
		t.Fatalf("DeletePlayback: %v", err)
	}
	want := `{"contentId":"abc123","contentType":"CmsEpisode"}`
	if got := seq.lastBody(); got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(seq.lastBody()), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("payload keys = %v, want contentId and contentType only", payload)
	}
	if seq.paths[0] != "/token/session/playback/delete" {
		t.Fatalf("path = %q", seq.paths[0])
	}
	if got := seq.header.Get("Cookie"); got != "sid=stub-token" {
		t.Fatalf("Cookie = %q", got)
	}
}

func TestDeletePlaybackWithoutContent(t *testing.T) {
	seq, srv := newSequenceServer(t, step{status: 200, body: `{}`})
	client := newClient(srv, nil)

	if err := client.DeletePlayback(context.Background(), "", ""); err != nil {
		t.Fatalf("DeletePlayback: %v", err)
	}
	if seq.callCount() != 0 {
		t.Fatalf("calls = %d, want 0", seq.callCount())
	}
}

func TestTatortCategoriesReadsOnlyCities(t *testing.T) {
	page := `<html><script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"slug":"tatort-cities","category":{"slug":"tatort-cities","title":"Tatort Städte","subcategories":{"nodes":[{"title":"Tatort M\u00fcnster","slug":"tatort-muenster"},{"title":"Tatort K\u00f6ln","slug":"tatort-koeln"},{"title":"Tatort K\u00f6ln","slug":"tatort-koeln"}]}}}}}</script>
<script type="application/ld+json">{"itemListElement":[{"item":{"url":"https://www.ardplus.de/details/a0S123-film"}}]}</script></html>`
	seq, srv := newSequenceServer(t, step{status: http.StatusOK, body: page})
	client := newClient(srv, nil)

	categories, err := client.TatortCategories(context.Background())
	if err != nil {
		t.Fatalf("TatortCategories: %v", err)
	}
	want := []TatortCategory{
		{Slug: "tatort-muenster", Title: "Tatort Münster"},
		{Slug: "tatort-koeln", Title: "Tatort Köln"},
	}
	if len(categories) != len(want) {
		t.Fatalf("categories = %+v, want %+v", categories, want)
	}
	for i := range want {
		if categories[i] != want[i] {
			t.Errorf("categories[%d] = %+v, want %+v", i, categories[i], want[i])
		}
	}
	if len(seq.paths) != 1 || seq.paths[0] != "/kategorie/tatort-cities" {
		t.Fatalf("request paths = %v, want only the cities collection", seq.paths)
	}
}
func TestTatortEpisodes(t *testing.T) {
	page := `<!doctype html><html><head>
<script type="application/ld+json">
{
  "@type": "ItemList",
  "itemListElement": [
    {"@type": "ListItem", "position": "1", "item": {"@type": "Movie", "name": "Erste", "url": "https://www.ardplus.de/details/aaa111"}},
    {"@type": "ListItem", "position": "2", "item": {"@type": "Movie", "name": "Zweite", "url": "https://www.ardplus.de/details/bbb222"}}
  ]
}
</script>
</head><body>Tatort</body></html>`
	seq, srv := newSequenceServer(t, step{status: 200, body: page})
	client := newClient(srv, nil)

	episodes, err := client.TatortEpisodes(context.Background(), "tatort-koeln")
	if err != nil {
		t.Fatalf("TatortEpisodes: %v", err)
	}
	want := []TatortEpisode{
		{URL: "https://www.ardplus.de/details/aaa111", Name: "Erste"},
		{URL: "https://www.ardplus.de/details/bbb222", Name: "Zweite"},
	}
	if len(episodes) != len(want) {
		t.Fatalf("episodes = %v, want %v", episodes, want)
	}
	for i := range want {
		if episodes[i] != want[i] {
			t.Fatalf("episodes[%d] = %+v, want %+v", i, episodes[i], want[i])
		}
	}
	if seq.paths[0] != "/kategorie/tatort-koeln" {
		t.Fatalf("path = %q", seq.paths[0])
	}
}

func TestTatortEpisodesErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no ld+json", `<html><body>nothing here</body></html>`},
		{"invalid json", `<script type="application/ld+json">{oops</script>`},
		{"other script type", `<script type="application/json">{"itemListElement":[]}</script>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := newSequenceServer(t, step{status: 200, body: tc.body})
			client := newClient(srv, nil)
			_, err := client.TatortEpisodes(context.Background(), "tatort-koeln")
			if err == nil {
				t.Fatal("unparseable category page must fail")
			}
			if err.Error() != "could not parse Tatort episode list from category page" {
				t.Fatalf("error = %q", err.Error())
			}
		})
	}
}

func TestTatortEpisodesReportsHTTPError(t *testing.T) {
	_, srv := newSequenceServer(t, step{status: http.StatusForbidden, body: "forbidden"})
	client := newClient(srv, nil)
	_, err := client.TatortEpisodes(context.Background(), "tatort-berlinger-und-rascher")
	if err == nil || err.Error() != "Tatort-Kategorieseite tatort-berlinger-und-rascher: HTTP 403" {
		t.Fatalf("TatortEpisodes error = %v, want the HTTP status", err)
	}
}

func TestTatortEpisodesAcceptsEmptyList(t *testing.T) {
	page := `<script type="application/ld+json">{"@type":"ItemList","itemListElement":[]}</script>`
	_, srv := newSequenceServer(t, step{status: http.StatusOK, body: page})
	client := newClient(srv, nil)
	episodes, err := client.TatortEpisodes(context.Background(), "tatort-muenster")
	if err != nil {
		t.Fatalf("TatortEpisodes: %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("episodes = %v, want an empty category", episodes)
	}
}

// TestTatortEpisodesMultipleBlocks covers the real-world case where the page
// carries a WebPage ld+json block before the ItemList block that lists episodes.
func TestTatortEpisodesMultipleBlocks(t *testing.T) {
	page := `<!doctype html><html><head>
<script type="application/ld+json">{"@type":"WebPage","name":"Tatort Köln"}</script>
<script type="application/ld+json">
{
  "@type": "ItemList",
  "itemListElement": [
    {"@type": "ListItem", "position": "1", "item": {"@type": "Movie", "name": "Folge Eins", "url": "https://www.ardplus.de/details/xyz001"}},
    {"@type": "ListItem", "position": "2", "item": {"@type": "Movie", "name": "Folge Zwei", "url": "https://www.ardplus.de/details/xyz002"}}
  ]
}
</script>
</head><body>Tatort</body></html>`
	_, srv := newSequenceServer(t, step{status: 200, body: page})
	client := newClient(srv, nil)

	episodes, err := client.TatortEpisodes(context.Background(), "tatort-koeln")
	if err != nil {
		t.Fatalf("TatortEpisodes with multiple ld+json blocks: %v", err)
	}
	want := []TatortEpisode{
		{URL: "https://www.ardplus.de/details/xyz001", Name: "Folge Eins"},
		{URL: "https://www.ardplus.de/details/xyz002", Name: "Folge Zwei"},
	}
	if len(episodes) != len(want) {
		t.Fatalf("episodes = %v, want %v", episodes, want)
	}
	for i := range want {
		if episodes[i] != want[i] {
			t.Fatalf("episodes[%d] = %+v, want %+v", i, episodes[i], want[i])
		}
	}
}

func TestDetailID(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"plain", "https://www.ardplus.de/details/abc123", "abc123"},
		{"trailing slash", "https://www.ardplus.de/details/abc123/", "abc123"},
		{"hyphenated slug", "https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt", "a0S01000000EWYi"},
		// A raw URL keeps the query in the id, exactly like the Bash sed
		// expression; the callers normalize the URL first.
		{"raw query string", "https://www.ardplus.de/details/abc123?utm_source=share", "abc123?utm_source=share"},
		{"normalized query string", "https://www.ardplus.de/details/abc123", "abc123"},
		{"category url", "https://www.ardplus.de/kategorie/tatort-koeln", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetailID(tc.url); got != tc.want {
				t.Fatalf("DetailID(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
