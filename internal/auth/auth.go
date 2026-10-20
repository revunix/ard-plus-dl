// Package auth implements ARD Plus login, session token storage and playback
// authorization without the renewal loop of the original script.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// LoginURL is the ARD Plus web login endpoint that answers with an
// Authorization response header.
const LoginURL = "https://auth.ardplus.de/auth/login?plainRedirect=true&redirectURL=https%3A%2F%2Fwww.ardplus.de%2Flogin%2Fcallback&errorRedirectURL=https%3A%2F%2Fwww.ardplus.de%2Fanmeldung%3Ferror%3Dtrue"

// DefaultUA is the user agent used for every ARD Plus request.
const DefaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"

// Store persists the session token in a 0600 file inside a 0700 directory.
type Store struct {
	Path string
}

// Load returns the stored token, trimmed of line endings.
func (s Store) Load() (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", err
	}
	return strings.Trim(string(data), "\r\n"), nil
}

// Save writes the token atomically.
func (s Store) Save(token string) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, filepath.Base(s.Path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return err
	}
	return os.Chmod(s.Path, 0o600)
}

// Remove deletes the stored token, ignoring a missing file.
func (s Store) Remove() error {
	err := os.Remove(s.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Client performs the login request.
type Client struct {
	HTTP     *http.Client
	LoginURL string
	UA       string
}

// uriEncode percent-encodes like jq's @uri (RFC 3986, space becomes %20).
func uriEncode(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Login exchanges credentials for a session token.
func (c *Client) Login(ctx context.Context, user, pass string) (string, error) {
	loginURL := c.LoginURL
	if loginURL == "" {
		loginURL = LoginURL
	}
	ua := c.UA
	if ua == "" {
		ua = DefaultUA
	}
	body := "username=" + uriEncode(user) + "&password=" + uriEncode(pass)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Host = "auth.ardplus.de"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://www.ardplus.de")
	req.Header.Set("Referer", "https://www.ardplus.de/")
	req.Header.Set("User-Agent", ua)

	client := http.Client{}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// curl runs without -L, and the token arrives in the Authorization header
	// of the first response, so redirects must not be followed.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return "", err
	}

	token := strings.TrimSpace(resp.Header.Get("Authorization"))
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.Trim(token, "\r\n")
	if JWTType(token) != "JWT" {
		return "", fmt.Errorf("Login not possible! Please check credentials and subscription for user %s.", user)
	}
	return token, nil
}

// JWTType decodes the header of a JWT and returns its "typ" claim.
func JWTType(token string) string {
	head, _, _ := strings.Cut(token, ".")
	if head == "" {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(head, "="))
	if err != nil {
		return ""
	}
	var header struct {
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return ""
	}
	return header.Typ
}

// Playback is the slice of the ARD Plus API the session needs.
type Playback interface {
	SetToken(token string)
	PlaybackAuth(ctx context.Context, contentID, contentType string) (string, error)
	DeletePlayback(ctx context.Context, contentID, contentType string) error
}

// Session owns the token lifecycle: it loads a stored token, logs in on demand
// and renews a rejected session exactly once.
type Session struct {
	Store    Store
	Login    *Client
	Playback Playback
	Username string
	Password string
	Logf     func(format string, args ...any)

	Token   string
	renewed bool
}

func (s *Session) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Ensure makes a token available, logging in when none is stored.
func (s *Session) Ensure(ctx context.Context) error {
	if token, err := s.Store.Load(); err == nil && token != "" {
		s.setToken(token)
		return nil
	}
	return s.login(ctx)
}

func (s *Session) setToken(token string) {
	s.Token = token
	if s.Playback != nil {
		s.Playback.SetToken(token)
	}
}

func (s *Session) login(ctx context.Context) error {
	if s.Username == "" || s.Password == "" {
		return errors.New("Credentials missing! Provide them via one of:\n" +
			"  - environment variables: ARD_PLUS_USER and ARD_PLUS_PASSWORD\n" +
			"  - credentials file (chmod 600, lines: username=... / password=...)\n" +
			"  - positional arguments (visible to other processes via ps - not recommended)")
	}
	token, err := s.Login.Login(ctx, s.Username, s.Password)
	if err != nil {
		return err
	}
	if err := s.Store.Save(token); err != nil {
		return err
	}
	s.setToken(token)
	return nil
}

// Renew deletes the stored token and logs in again.
func (s *Session) Renew(ctx context.Context) error {
	s.logf("Session abgelaufen oder ungültig, neuer Login...")
	_ = s.Store.Remove()
	return s.login(ctx)
}

// Authorization returns the playback URL parameters, renewing a rejected
// session once. A session that stays rejected is not renewed again for the
// following items.
func (s *Session) Authorization(ctx context.Context, contentID, contentType string) (string, error) {
	param, err := s.Playback.PlaybackAuth(ctx, contentID, contentType)
	if err != nil {
		return "", err
	}
	if param != "" {
		s.renewed = false
		return param, nil
	}
	if s.renewed {
		return "", errors.New("playback session rejected")
	}
	if err := s.Renew(ctx); err != nil {
		return "", err
	}
	s.renewed = true
	param, err = s.Playback.PlaybackAuth(ctx, contentID, contentType)
	if err != nil {
		return "", err
	}
	if param == "" {
		return "", errors.New("playback session rejected")
	}
	return param, nil
}

// Release ends the playback session for one content item.
func (s *Session) Release(ctx context.Context, contentID, contentType string) {
	if contentID == "" || contentType == "" {
		return
	}
	if err := s.Playback.DeletePlayback(ctx, contentID, contentType); err != nil {
		s.logf("playback cleanup failed: %v", err)
	}
}
