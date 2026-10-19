package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// testJWT builds a token whose header decodes to the given JSON object.
func testJWT(t *testing.T, header string) string {
	t.Helper()
	encoded := base64.RawURLEncoding.EncodeToString([]byte(header))
	return encoded + ".payload.signature"
}

func TestJWTType(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"standard header", "eyJ0eXAiOiJKV1QifQ.payload.signature", "JWT"},
		{"header only", "eyJ0eXAiOiJKV1QifQ", "JWT"},
		{"empty token", "", ""},
		{"malformed base64", "!!!.payload.signature", ""},
		{"non-json header", base64.RawURLEncoding.EncodeToString([]byte("hello")) + ".b.c", ""},
		{"header without typ", testJWT(t, `{"alg":"HS256"}`), ""},
		{"other typ", testJWT(t, `{"typ":"at+jwt"}`), "at+jwt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := JWTType(tc.token); got != tc.want {
				t.Fatalf("JWTType(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestStoreSavePermissionsAndLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "ard-plus-dl")
	store := Store{Path: filepath.Join(dir, "token")}
	token := testJWT(t, `{"typ":"JWT"}`)

	if err := store.Save(token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("token dir mode = %o, want 700", perm)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != token {
		t.Fatalf("Load = %q, want %q", got, token)
	}
}

func TestStoreSaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "token")}
	if err := store.Save("first"); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	if err := store.Save("second"); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "second" {
		t.Fatalf("Load = %q, want %q", got, "second")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "token" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("leftover files after overwrite: %v", names)
	}
}

func TestStoreLoadTrimsCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("token-value\r\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := Store{Path: path}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "token-value" {
		t.Fatalf("Load = %q, want %q", got, "token-value")
	}
}

func TestStoreLoadMissing(t *testing.T) {
	if _, err := (Store{Path: filepath.Join(t.TempDir(), "absent")}).Load(); err == nil {
		t.Fatal("Load of a missing file should fail")
	}
}

func TestStoreRemoveIgnoresMissing(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "token")}
	if err := store.Remove(); err != nil {
		t.Fatalf("Remove of missing file: %v", err)
	}
	if err := store.Save("value"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(store.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token still present after Remove: %v", err)
	}
}

func TestLoginFormEncoding(t *testing.T) {
	var body, contentType, origin, referer, host, ua string
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		contentType = r.Header.Get("Content-Type")
		origin = r.Header.Get("Origin")
		referer = r.Header.Get("Referer")
		host = r.Host
		ua = r.Header.Get("User-Agent")
		method, path = r.Method, r.URL.Path
		w.Header().Set("Authorization", "Bearer "+testJWT(t, `{"typ":"JWT"}`))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login?plainRedirect=true"}
	token, err := client.Login(context.Background(), "user name", "p@ss:word/1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if JWTType(token) != "JWT" {
		t.Fatalf("returned token %q is not a JWT", token)
	}
	if want := "username=user%20name&password=p%40ss%3Aword%2F1"; body != want {
		t.Fatalf("form body = %q, want %q", body, want)
	}
	if method != http.MethodPost {
		t.Fatalf("method = %s, want POST", method)
	}
	if path != "/auth/login" {
		t.Fatalf("path = %s", path)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if origin != "https://www.ardplus.de" {
		t.Fatalf("Origin = %q", origin)
	}
	if referer != "https://www.ardplus.de/" {
		t.Fatalf("Referer = %q", referer)
	}
	if host != "auth.ardplus.de" {
		t.Fatalf("Host = %q, want auth.ardplus.de", host)
	}
	if !strings.Contains(ua, "Chrome/119.0.0.0") {
		t.Fatalf("User-Agent = %q", ua)
	}
}

func TestLoginAuthorizationHeaderVariants(t *testing.T) {
	jwt := testJWT(t, `{"typ":"JWT"}`)
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"plain token", jwt, jwt},
		{"bearer prefix", "Bearer " + jwt, jwt},
		{"surrounding spaces", "  " + jwt + "  ", jwt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Authorization", tc.header)
			}))
			defer srv.Close()
			client := &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"}
			got, err := client.Login(context.Background(), "user", "pass")
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			if got != tc.want {
				t.Fatalf("token = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoginRejectsNonJWT(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"empty header", ""},
		{"opaque value", "not-a-jwt"},
		{"microsoft-style header", base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"at+jwt"}`)) + ".b.c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Authorization", tc.header)
				}
			}))
			defer srv.Close()
			client := &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"}
			_, err := client.Login(context.Background(), "user", "pass")
			if err == nil {
				t.Fatal("Login should fail without a JWT")
			}
			want := "Login not possible! Please check credentials and subscription for user user."
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestLoginDoesNotFollowRedirect(t *testing.T) {
	jwt := testJWT(t, `{"typ":"JWT"}`)
	var secondCalls int
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Authorization", "Bearer "+jwt)
		w.Header().Set("Location", "/after-login")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/after-login", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		secondCalls++
		mu.Unlock()
		w.Header().Set("Authorization", "opaque-value")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"}
	got, err := client.Login(context.Background(), "user", "pass")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got != jwt {
		t.Fatalf("token = %q, want the header of the first response", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if secondCalls != 0 {
		t.Fatalf("redirect was followed %d time(s)", secondCalls)
	}
}

// fakePlayback is a Playback stub with a scripted authorization sequence.
type fakePlayback struct {
	mu        sync.Mutex
	auth      func(contentID, contentType string) (string, error)
	authCalls []string
	deletes   []string
	token     string
	deleteErr error
}

func (f *fakePlayback) SetToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = token
}

func (f *fakePlayback) PlaybackAuth(_ context.Context, contentID, contentType string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authCalls = append(f.authCalls, contentID+"/"+contentType)
	if f.auth == nil {
		return "", nil
	}
	return f.auth(contentID, contentType)
}

func (f *fakePlayback) DeletePlayback(_ context.Context, contentID, contentType string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, contentID+"/"+contentType)
	return f.deleteErr
}

func (f *fakePlayback) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.authCalls)
}

// loginServer serves /auth/login and counts the requests.
func loginServer(t *testing.T, jwt string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Authorization", "Bearer "+jwt)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

func TestSessionEnsureUsesStoredToken(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "token")}
	jwt := testJWT(t, `{"typ":"JWT"}`)
	if err := store.Save(jwt); err != nil {
		t.Fatalf("Save: %v", err)
	}
	srv, logins := loginServer(t, jwt)
	playback := &fakePlayback{}
	session := &Session{
		Store:    store,
		Login:    &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"},
		Playback: playback,
		Username: "user",
		Password: "pass",
	}
	if err := session.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if session.Token != jwt {
		t.Fatalf("Token = %q, want %q", session.Token, jwt)
	}
	if playback.token != jwt {
		t.Fatalf("playback token = %q, want %q", playback.token, jwt)
	}
	if logins() != 0 {
		t.Fatalf("stored token caused %d login(s)", logins())
	}
}

func TestSessionEnsureLogsInWithoutToken(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "token")}
	jwt := testJWT(t, `{"typ":"JWT"}`)
	srv, logins := loginServer(t, jwt)
	session := &Session{
		Store:    store,
		Login:    &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"},
		Playback: &fakePlayback{},
		Username: "user",
		Password: "pass",
	}
	if err := session.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if logins() != 1 {
		t.Fatalf("logins = %d, want 1", logins())
	}
	stored, err := store.Load()
	if err != nil || stored != jwt {
		t.Fatalf("stored token = %q (err %v), want %q", stored, err, jwt)
	}
}

func TestSessionEnsureWithoutCredentials(t *testing.T) {
	session := &Session{Store: Store{Path: filepath.Join(t.TempDir(), "token")}, Playback: &fakePlayback{}}
	err := session.Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure without credentials should fail")
	}
	if !strings.Contains(err.Error(), "Credentials missing!") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestSessionRenew(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "token")}
	if err := store.Save("old-token"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jwt := testJWT(t, `{"typ":"JWT"}`)
	srv, logins := loginServer(t, jwt)
	var logs []string
	playback := &fakePlayback{}
	session := &Session{
		Store:    store,
		Login:    &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"},
		Playback: playback,
		Username: "user",
		Password: "pass",
		Logf:     func(format string, args ...any) { logs = append(logs, format) },
	}
	if err := session.Renew(context.Background()); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if logins() != 1 {
		t.Fatalf("logins = %d, want 1", logins())
	}
	if session.Token != jwt {
		t.Fatalf("Token = %q, want %q", session.Token, jwt)
	}
	stored, _ := store.Load()
	if stored != jwt {
		t.Fatalf("stored = %q, want %q", stored, jwt)
	}
	if playback.token != jwt {
		t.Fatalf("playback token = %q", playback.token)
	}
	if len(logs) != 1 || logs[0] != "Session abgelaufen oder ungültig, neuer Login..." {
		t.Fatalf("logs = %v", logs)
	}
}

func TestSessionAuthorizationRenewsOnce(t *testing.T) {
	dir := t.TempDir()
	store := Store{Path: filepath.Join(dir, "token")}
	jwt := testJWT(t, `{"typ":"JWT"}`)
	srv, logins := loginServer(t, jwt)
	playback := &fakePlayback{}
	session := &Session{
		Store:    store,
		Login:    &Client{HTTP: srv.Client(), LoginURL: srv.URL + "/auth/login"},
		Playback: playback,
		Username: "user",
		Password: "pass",
	}

	// Phase 1: the first session call is rejected, the renewal makes the
	// second one succeed (the renewal guard mirrors the Bash
	// session_renewed flag, which stays set until the next direct success).
	var phaseMu sync.Mutex
	grant := false
	playback.auth = func(string, string) (string, error) {
		phaseMu.Lock()
		defer phaseMu.Unlock()
		if grant && logins() > 0 {
			return "token=abc", nil
		}
		return "", nil
	}
	phaseMu.Lock()
	grant = true
	phaseMu.Unlock()

	param, err := session.Authorization(context.Background(), "abc123", "CmsMovie")
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if param != "token=abc" {
		t.Fatalf("param = %q", param)
	}
	if logins() != 1 {
		t.Fatalf("renewal logins = %d, want 1", logins())
	}
	if playback.calls() != 2 {
		t.Fatalf("playback calls = %d, want 2", playback.calls())
	}
	if !session.renewed {
		t.Fatal("the successful renewal must arm the renewal guard")
	}

	// Phase 1b: a directly granted authorization resets the guard.
	param, err = session.Authorization(context.Background(), "abc123", "CmsMovie")
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if param != "token=abc" {
		t.Fatalf("param = %q", param)
	}
	if session.renewed {
		t.Fatal("a directly granted authorization must reset the renewal guard")
	}
	if logins() != 1 {
		t.Fatalf("a granted authorization must not log in again, logins = %d", logins())
	}

	// Phase 2: the API keeps rejecting the session; the first rejection renews
	// once, the next item does not trigger another login.
	phaseMu.Lock()
	grant = false
	phaseMu.Unlock()
	before := logins()
	if _, err := session.Authorization(context.Background(), "abc123", "CmsMovie"); err == nil {
		t.Fatal("a rejected session should fail")
	}
	if logins() != before+1 {
		t.Fatalf("first rejection logins = %d, want %d", logins(), before+1)
	}
	after := logins()
	if _, err := session.Authorization(context.Background(), "abc123", "CmsMovie"); err == nil {
		t.Fatal("a second rejection should fail too")
	}
	if logins() != after {
		t.Fatalf("renewal repeated: logins = %d, want %d", logins(), after)
	}
}

func TestSessionAuthorizationWithoutRenewal(t *testing.T) {
	playback := &fakePlayback{auth: func(string, string) (string, error) { return "token=xyz", nil }}
	session := &Session{Store: Store{Path: filepath.Join(t.TempDir(), "token")}, Playback: playback}
	param, err := session.Authorization(context.Background(), "id", "CmsEpisode")
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if param != "token=xyz" {
		t.Fatalf("param = %q", param)
	}
	if playback.calls() != 1 {
		t.Fatalf("playback calls = %d, want 1", playback.calls())
	}
}

func TestSessionAuthorizationPropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	playback := &fakePlayback{auth: func(string, string) (string, error) { return "", wantErr }}
	session := &Session{Store: Store{Path: filepath.Join(t.TempDir(), "token")}, Playback: playback}
	if _, err := session.Authorization(context.Background(), "id", "CmsEpisode"); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestSessionRelease(t *testing.T) {
	playback := &fakePlayback{}
	session := &Session{Store: Store{Path: filepath.Join(t.TempDir(), "token")}, Playback: playback}

	session.Release(context.Background(), "", "")
	if len(playback.deletes) != 0 {
		t.Fatalf("Release without content should not call the API, got %v", playback.deletes)
	}
	session.Release(context.Background(), "abc123", "CmsEpisode")
	if len(playback.deletes) != 1 || playback.deletes[0] != "abc123/CmsEpisode" {
		t.Fatalf("deletes = %v", playback.deletes)
	}

	var logs []string
	session.Logf = func(format string, args ...any) { logs = append(logs, format) }
	playback.deleteErr = errors.New("cleanup gone")
	session.Release(context.Background(), "abc123", "CmsEpisode")
	if len(logs) != 1 || !strings.Contains(logs[0], "playback cleanup failed") {
		t.Fatalf("logs = %v", logs)
	}
}

func TestLoginURLConstant(t *testing.T) {
	if !strings.HasPrefix(LoginURL, "https://auth.ardplus.de/auth/login?") {
		t.Fatalf("LoginURL = %q", LoginURL)
	}
	if !strings.Contains(LoginURL, "plainRedirect=true") {
		t.Fatalf("LoginURL is missing the query parameters: %q", LoginURL)
	}
}
