package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(pairs map[string]string) Env {
	return func(key string) string { return pairs[key] }
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

func TestParseArgsHelpAndVersion(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want error
	}{
		{"help exits with the help sentinel", []string{"--help"}, ErrHelp},
		{"short help exits with the help sentinel", []string{"-h"}, ErrHelp},
		{"version exits with the version sentinel", []string{"--version"}, ErrVersion},
		{"short version exits with the version sentinel", []string{"-v"}, ErrVersion},
		{"help wins over following arguments", []string{"--automatic", "--help", "url"}, ErrHelp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseArgs(tc.args); !errors.Is(err, tc.want) {
				t.Fatalf("ParseArgs(%q) error = %v, want %v", tc.args, err, tc.want)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"unknown option", []string{"--bogus", "https://www.ardplus.de/details/foo"}, "Unknown option: --bogus"},
		{"unknown short option", []string{"-x"}, "Unknown option: -x"},
		{"missing url", nil, "URL or --links-file required"},
		{"empty url", []string{""}, "URL or --links-file required"},
		{"links-file requires a file argument", []string{"--links-file"}, "Error: --links-file requires a file argument"},
		{"links-file rejects an option argument", []string{"--links-file", "--automatic"}, "Error: --links-file requires a file argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseArgs(tc.args)
			if err == nil {
				t.Fatalf("ParseArgs(%q) succeeded, want error", tc.args)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("error = %q, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseArgsPositionalSplits(t *testing.T) {
	t.Run("url mode", func(t *testing.T) {
		cases := []struct {
			name     string
			args     []string
			url      string
			username string
			password string
			skipRaw  string
		}{
			{"url only", []string{"https://www.ardplus.de/details/foo"}, "https://www.ardplus.de/details/foo", "", "", ""},
			{"url with lone trailing argument", []string{"https://www.ardplus.de/details/foo", "7"}, "https://www.ardplus.de/details/foo", "", "", "7"},
			{"url with username and password", []string{"https://www.ardplus.de/details/foo", "user", "pass"}, "https://www.ardplus.de/details/foo", "user", "pass", ""},
			{"url with username, password and skip", []string{"https://www.ardplus.de/details/foo", "user", "pass", "3"}, "https://www.ardplus.de/details/foo", "user", "pass", "3"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				o, err := ParseArgs(tc.args)
				if err != nil {
					t.Fatalf("ParseArgs: %v", err)
				}
				if o.URL != tc.url || o.Username != tc.username || o.Password != tc.password || o.SkipRaw != tc.skipRaw {
					t.Fatalf("got %+v", o)
				}
				if o.Batch() {
					t.Fatal("URL mode must not report batch mode")
				}
			})
		}
	})

	t.Run("batch mode", func(t *testing.T) {
		cases := []struct {
			name     string
			args     []string
			username string
			password string
			skipRaw  string
		}{
			{"links file only", []string{"--links-file", "links.txt"}, "", "", ""},
			{"links file with lone trailing argument", []string{"--links-file", "links.txt", "2"}, "", "", "2"},
			{"links file with username and password", []string{"--links-file", "links.txt", "user", "pass"}, "user", "pass", ""},
			{"links file with username, password and skip", []string{"--links-file", "links.txt", "user", "pass", "4"}, "user", "pass", "4"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				o, err := ParseArgs(tc.args)
				if err != nil {
					t.Fatalf("ParseArgs: %v", err)
				}
				if !o.Batch() || o.LinksFile != "links.txt" {
					t.Fatalf("got %+v", o)
				}
				if o.Username != tc.username || o.Password != tc.password || o.SkipRaw != tc.skipRaw {
					t.Fatalf("got %+v", o)
				}
			})
		}
	})

	t.Run("options and the option terminator", func(t *testing.T) {
		o, err := ParseArgs([]string{"--automatic", "--force-redownload", "--", "https://www.ardplus.de/details/foo"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}
		if !o.Automatic || !o.ForceRedownload || o.URL != "https://www.ardplus.de/details/foo" {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("options after the first positional become positional", func(t *testing.T) {
		o, err := ParseArgs([]string{"https://www.ardplus.de/details/foo", "--automatic"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}
		if o.Automatic {
			t.Fatal("an option after the URL must not be parsed as an option")
		}
		if o.SkipRaw != "--automatic" {
			t.Fatalf("skipRaw = %q, want --automatic", o.SkipRaw)
		}
	})
	t.Run("the terminator keeps a following option positional", func(t *testing.T) {
		o, err := ParseArgs([]string{"--", "https://www.ardplus.de/details/foo", "--automatic"})
		if err != nil {
			t.Fatalf("ParseArgs: %v", err)
		}
		if o.Automatic || o.SkipRaw != "--automatic" {
			t.Fatalf("got %+v", o)
		}
	})
}

func TestResolveSkip(t *testing.T) {
	t.Run("default 1", func(t *testing.T) {
		o := &Options{}
		if err := o.ResolveSkip(); err != nil {
			t.Fatalf("ResolveSkip: %v", err)
		}
		if o.Skip != 1 {
			t.Fatalf("Skip = %d, want 1", o.Skip)
		}
	})
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"0", 0},
		{"1", 1},
		{"5", 5},
		{"007", 7},
	} {
		t.Run("explicit "+tc.raw, func(t *testing.T) {
			o := &Options{SkipRaw: tc.raw}
			if err := o.ResolveSkip(); err != nil {
				t.Fatalf("ResolveSkip: %v", err)
			}
			if o.Skip != tc.want {
				t.Fatalf("Skip = %d, want %d", o.Skip, tc.want)
			}
		})
	}
	t.Run("a lone non-numeric argument is the username", func(t *testing.T) {
		o := &Options{SkipRaw: "someuser"}
		if err := o.ResolveSkip(); err != nil {
			t.Fatalf("ResolveSkip: %v", err)
		}
		if o.Username != "someuser" || o.Skip != 1 {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("invalid skip value with credentials", func(t *testing.T) {
		o := &Options{SkipRaw: "nonsense", Username: "user", Password: "pass"}
		err := o.ResolveSkip()
		if err == nil {
			t.Fatal("expected an error")
		}
		want := "Invalid skip value: nonsense (expected a non-negative number)"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err, want)
		}
	})
	t.Run("invalid skip value with a username only", func(t *testing.T) {
		o := &Options{SkipRaw: "nonsense", Username: "user"}
		if err := o.ResolveSkip(); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestCredentialsPath(t *testing.T) {
	t.Run("explicit credentials file wins", func(t *testing.T) {
		env := envFrom(map[string]string{"ARD_PLUS_CREDENTIALS_FILE": "/etc/custom", "XDG_CONFIG_HOME": "/xdg"})
		if got := CredentialsPath(env); got != "/etc/custom" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("XDG_CONFIG_HOME is used", func(t *testing.T) {
		env := envFrom(map[string]string{"XDG_CONFIG_HOME": "/xdg"})
		if got := CredentialsPath(env); got != "/xdg/ard-plus-dl/credentials" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("HOME fallback", func(t *testing.T) {
		env := envFrom(map[string]string{"HOME": "/home/user"})
		if got := CredentialsPath(env); got != "/home/user/.config/ard-plus-dl/credentials" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLoadCredentialsFile(t *testing.T) {
	t.Run("loads and comments and blanks are ignored", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "# comment line\nusername=file-user\n\npassword=file-pass\n", 0o600)
		username, password, tag := LoadCredentialsFile(path, &bytes.Buffer{})
		if username != "file-user" || password != "file-pass" || tag != "" {
			t.Fatalf("got %q/%q/%q", username, password, tag)
		}
	})
	t.Run("CRLF line endings are stripped", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "username=crlf-user\r\npassword=crlf-pass\r\n", 0o600)
		username, password, _ := LoadCredentialsFile(path, &bytes.Buffer{})
		if username != "crlf-user" || password != "crlf-pass" {
			t.Fatalf("got %q/%q", username, password)
		}
	})
	t.Run("the first value wins", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "username=first\npassword=first-pass\ntag=first-tag\nusername=second\npassword=second-pass\ntag=second-tag\n", 0o600)
		username, password, tag := LoadCredentialsFile(path, &bytes.Buffer{})
		if username != "first" || password != "first-pass" || tag != "first-tag" {
			t.Fatalf("got %q/%q/%q", username, password, tag)
		}
	})
	t.Run("ARD_PLUS_* aliases are accepted", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "ARD_PLUS_USER=env-style-user\nARD_PLUS_PASSWORD=env-style-pass\nARD_PLUS_TAG=env-style-tag\n", 0o600)
		username, password, tag := LoadCredentialsFile(path, &bytes.Buffer{})
		if username != "env-style-user" || password != "env-style-pass" || tag != "env-style-tag" {
			t.Fatalf("got %q/%q/%q", username, password, tag)
		}
	})
	t.Run("a missing file yields nothing", func(t *testing.T) {
		dir := t.TempDir()
		username, password, tag := LoadCredentialsFile(filepath.Join(dir, "does-not-exist"), &bytes.Buffer{})
		if username != "" || password != "" || tag != "" {
			t.Fatalf("got %q/%q/%q", username, password, tag)
		}
	})
	t.Run("warns about loose permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "username=file-user\npassword=file-pass\n", 0o644)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		var stderr bytes.Buffer
		LoadCredentialsFile(path, &stderr)
		if !strings.Contains(stderr.String(), "readable by group/others") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})
	t.Run("no warning for a private file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "username=file-user\n", 0o600)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		var stderr bytes.Buffer
		LoadCredentialsFile(path, &stderr)
		if stderr.Len() != 0 {
			t.Fatalf("unexpected warning: %q", stderr.String())
		}
	})
}

func TestResolveCredentials(t *testing.T) {
	t.Run("the environment provides the credentials", func(t *testing.T) {
		dir := t.TempDir()
		env := envFrom(map[string]string{
			"ARD_PLUS_USER":             "env-user",
			"ARD_PLUS_PASSWORD":         "env-pass",
			"ARD_PLUS_TAG":              "envtag",
			"ARD_PLUS_CREDENTIALS_FILE": filepath.Join(dir, "none"),
		})
		o := &Options{}
		ResolveCredentials(o, env, &bytes.Buffer{})
		if o.Username != "env-user" || o.Password != "env-pass" || o.FileTag != "envtag" {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("positional credentials win over the environment", func(t *testing.T) {
		dir := t.TempDir()
		env := envFrom(map[string]string{
			"ARD_PLUS_USER":             "env-user",
			"ARD_PLUS_PASSWORD":         "env-pass",
			"ARD_PLUS_CREDENTIALS_FILE": filepath.Join(dir, "none"),
		})
		o := &Options{Username: "arg-user", Password: "arg-pass"}
		ResolveCredentials(o, env, &bytes.Buffer{})
		if o.Username != "arg-user" || o.Password != "arg-pass" {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("the credentials file is the last fallback", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "username=file-user\npassword=file-pass\ntag=filetag\n", 0o600)
		env := envFrom(map[string]string{"ARD_PLUS_CREDENTIALS_FILE": path})
		o := &Options{}
		ResolveCredentials(o, env, &bytes.Buffer{})
		if o.Username != "file-user" || o.Password != "file-pass" || o.FileTag != "filetag" {
			t.Fatalf("got %+v", o)
		}
		if o.CredentialsFile != path {
			t.Fatalf("CredentialsFile = %q, want %q", o.CredentialsFile, path)
		}
	})
	t.Run("the environment wins over the credentials file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "username=file-user\npassword=file-pass\n", 0o600)
		env := envFrom(map[string]string{
			"ARD_PLUS_USER":             "env-user",
			"ARD_PLUS_PASSWORD":         "env-pass",
			"ARD_PLUS_CREDENTIALS_FILE": path,
		})
		o := &Options{}
		ResolveCredentials(o, env, &bytes.Buffer{})
		if o.Username != "env-user" || o.Password != "env-pass" {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("a partial credentials file fills only the gaps", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials")
		writeFile(t, path, "password=file-pass\n", 0o600)
		env := envFrom(map[string]string{
			"ARD_PLUS_USER":             "env-user",
			"ARD_PLUS_CREDENTIALS_FILE": path,
		})
		o := &Options{}
		ResolveCredentials(o, env, &bytes.Buffer{})
		if o.Username != "env-user" || o.Password != "file-pass" {
			t.Fatalf("got %+v", o)
		}
	})
}

func TestTokenPath(t *testing.T) {
	t.Run("XDG_STATE_HOME is used", func(t *testing.T) {
		home := t.TempDir()
		state := t.TempDir()
		env := envFrom(map[string]string{"HOME": home, "XDG_STATE_HOME": state})
		got := TokenPath(env, t.TempDir())
		want := filepath.Join(state, "ard-plus-dl", "token")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if info, err := os.Stat(filepath.Dir(got)); err != nil || !info.IsDir() {
			t.Fatalf("state directory not created: %v", err)
		}
	})
	t.Run("falls back to HOME/.local/state", func(t *testing.T) {
		home := t.TempDir()
		env := envFrom(map[string]string{"HOME": home})
		got := TokenPath(env, t.TempDir())
		want := filepath.Join(home, ".local", "state", "ard-plus-dl", "token")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("falls back to work_dir without HOME", func(t *testing.T) {
		workDir := t.TempDir()
		env := envFrom(map[string]string{"HOME": ""})
		got := TokenPath(env, workDir)
		want := filepath.Join(workDir, ".ard-plus-dl", "token")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("falls back to work_dir when the state directory is unusable", func(t *testing.T) {
		workDir := t.TempDir()
		blocker := filepath.Join(t.TempDir(), "blocker")
		writeFile(t, blocker, "not a directory", 0o644)
		env := envFrom(map[string]string{"HOME": t.TempDir(), "XDG_STATE_HOME": filepath.Join(blocker, "state")})
		got := TokenPath(env, workDir)
		want := filepath.Join(workDir, ".ard-plus-dl", "token")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

func TestValidateFileTag(t *testing.T) {
	t.Run("valid tags", func(t *testing.T) {
		for _, tag := range []string{"example", "ok.group-1_x", "A", "0abc", "abcdefghijklmnop"} {
			t.Run(tag, func(t *testing.T) {
				if err := ValidateFileTag(tag); err != nil {
					t.Fatalf("ValidateFileTag(%q) = %v", tag, err)
				}
			})
		}
	})
	t.Run("invalid tags", func(t *testing.T) {
		for _, tag := range []string{"bad/tag", "bad group", "abcdefghijklmnopq", "-lead", ".lead", "ümlaut"} {
			t.Run(tag, func(t *testing.T) {
				err := ValidateFileTag(tag)
				if err == nil {
					t.Fatalf("ValidateFileTag(%q) succeeded", tag)
				}
				want := "Invalid Datei-Tag: " + tag + " (allowed: letters, digits, '.', '_', '-')"
				if err.Error() != want {
					t.Fatalf("error = %q, want %q", err, want)
				}
			})
		}
	})
	t.Run("a missing tag is rejected", func(t *testing.T) {
		err := ValidateFileTag("")
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.HasPrefix(err.Error(), "Datei-Tag missing! Set ARD_PLUS_TAG") {
			t.Fatalf("error = %q", err)
		}
		if !strings.Contains(err.Error(), "tag=...") {
			t.Fatalf("error = %q", err)
		}
	})
}

func TestValidateContentURL(t *testing.T) {
	t.Run("accepted urls", func(t *testing.T) {
		for _, raw := range []string{
			"https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt",
			"https://www.ardplus.de/kategorie/tatort-koeln",
			"http://ardplus.de/details/x",
		} {
			t.Run(raw, func(t *testing.T) {
				if err := ValidateContentURL(raw); err != nil {
					t.Fatalf("ValidateContentURL(%q) = %v", raw, err)
				}
			})
		}
	})
	t.Run("rejected urls carry the reason", func(t *testing.T) {
		cases := []struct{ name, raw, want string }{
			{"other schemes", "ftp://www.ardplus.de/details/x", "not an absolute http(s) URL: ftp://www.ardplus.de/details/x"},
			{"foreign hosts", "https://example.com/details/x", "unsupported host: example.com"},
			{"the bare host", "https://www.ardplus.de/", "unsupported path: /"},
			{"relative paths", "/kategorie/foo", "not an absolute http(s) URL: /kategorie/foo"},
			{"a url without a path", "https://www.ardplus.de", "unsupported path: "},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := ValidateContentURL(tc.raw)
				if err == nil {
					t.Fatalf("ValidateContentURL(%q) succeeded", tc.raw)
				}
				if err.Error() != tc.want {
					t.Fatalf("error = %q, want %q", err, tc.want)
				}
			})
		}
	})
}
