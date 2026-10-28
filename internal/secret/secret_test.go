package secret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func writeCredentials(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestKeychain(t *testing.T) {
	t.Run("set and get round trip", func(t *testing.T) {
		keyring.MockInit()
		k := &Keychain{}
		if k.Backend() == "" {
			t.Fatal("Backend() is empty")
		}
		if _, err := k.Get(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() before Set = %v, want ErrNotFound", err)
		}
		if err := k.Set("geheim"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := k.Get()
		if err != nil || got != "geheim" {
			t.Fatalf("Get() = %q, %v; want %q, nil", got, err, "geheim")
		}
	})

	t.Run("set overwrites the stored password", func(t *testing.T) {
		keyring.MockInit()
		k := &Keychain{}
		if err := k.Set("alt"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := k.Set("neu"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := k.Get()
		if err != nil || got != "neu" {
			t.Fatalf("Get() = %q, %v; want %q, nil", got, err, "neu")
		}
	})

	t.Run("clear removes the password", func(t *testing.T) {
		keyring.MockInit()
		k := &Keychain{}
		if err := k.Set("geheim"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := k.Clear(); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		if _, err := k.Get(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() after Clear = %v, want ErrNotFound", err)
		}
		if err := k.Clear(); err != nil {
			t.Fatalf("second Clear: %v", err)
		}
	})

	t.Run("open prefers the keychain when it answers", func(t *testing.T) {
		keyring.MockInit()
		st := Open(filepath.Join(t.TempDir(), "credentials"))
		if _, ok := st.(*Keychain); !ok {
			t.Fatalf("Open() = %T, want *Keychain", st)
		}
	})
}

func TestFile(t *testing.T) {
	t.Run("set creates a 0600 file in a 0700 directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config", "credentials")
		f := &File{Path: path}
		if f.Backend() == "" {
			t.Fatal("Backend() is empty")
		}
		if err := f.Set("geheim"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got, want := readFile(t, path), "password=geheim\n"; got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
		if got := fileMode(t, path); got != 0o600 {
			t.Errorf("file mode = %o, want 600", got)
		}
		if got := fileMode(t, filepath.Dir(path)); got != 0o700 {
			t.Errorf("directory mode = %o, want 700", got)
		}
		got, err := f.Get()
		if err != nil || got != "geheim" {
			t.Fatalf("Get() = %q, %v; want %q, nil", got, err, "geheim")
		}
	})

	t.Run("set keeps unrelated lines and comments", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "username=alice\nnote=privat\n# comment\nnotes without equals\n", 0o600)
		f := &File{Path: path}
		if err := f.Set("geheim"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		want := "username=alice\nnote=privat\n# comment\nnotes without equals\npassword=geheim\n"
		if got := readFile(t, path); got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	})

	t.Run("set replaces an existing password line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "username=alice\npassword=alt\nnote=privat\n", 0o600)
		f := &File{Path: path}
		if err := f.Set("neu"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		want := "username=alice\nnote=privat\npassword=neu\n"
		got := readFile(t, path)
		if got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
		if strings.Contains(got, "alt") {
			t.Errorf("content still holds the old password: %q", got)
		}
	})

	t.Run("set replaces ARD_PLUS_PASSWORD and keeps the other keys", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "ARD_PLUS_PASSWORD=alt\nARD_PLUS_USER=bob\nusername=alice\n", 0o600)
		f := &File{Path: path}
		if err := f.Set("neu"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		want := "ARD_PLUS_USER=bob\nusername=alice\npassword=neu\n"
		got := readFile(t, path)
		if got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
		if strings.Contains(got, "ARD_PLUS_PASSWORD") {
			t.Errorf("content still holds ARD_PLUS_PASSWORD: %q", got)
		}
		if n := strings.Count(got, "password="); n != 1 {
			t.Errorf("password lines = %d, want 1", n)
		}
	})

	t.Run("set normalizes CRLF lines", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "username=alice\r\npassword=alt\r\n", 0o600)
		f := &File{Path: path}
		if err := f.Set("neu"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		want := "username=alice\npassword=neu\n"
		if got := readFile(t, path); got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	})

	t.Run("repeated set keeps one password line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "username=alice\npassword=alt\n", 0o600)
		f := &File{Path: path}
		for _, password := range []string{"eins", "zwei", "drei"} {
			if err := f.Set(password); err != nil {
				t.Fatalf("Set(%q): %v", password, err)
			}
		}
		got := readFile(t, path)
		if want := "username=alice\npassword=drei\n"; got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
		if value, err := f.Get(); err != nil || value != "drei" {
			t.Fatalf("Get() = %q, %v; want %q, nil", value, err, "drei")
		}
	})

	t.Run("set enforces 0600 on an existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "username=alice\n", 0o644)
		f := &File{Path: path}
		if err := f.Set("geheim"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := fileMode(t, path); got != 0o600 {
			t.Errorf("file mode = %o, want 600", got)
		}
	})

	t.Run("get returns ErrNotFound without a password", func(t *testing.T) {
		missing := &File{Path: filepath.Join(t.TempDir(), "credentials")}
		if _, err := missing.Get(); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(missing file) = %v, want ErrNotFound", err)
		}

		noPasswordPath := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, noPasswordPath, "username=alice\nnote=privat\n", 0o600)
		if _, err := (&File{Path: noPasswordPath}).Get(); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(file without password) = %v, want ErrNotFound", err)
		}

		emptyPath := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, emptyPath, "password=\n", 0o600)
		if _, err := (&File{Path: emptyPath}).Get(); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(empty password) = %v, want ErrNotFound", err)
		}
	})

	t.Run("get returns the first non-empty password", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "password=eins\nARD_PLUS_PASSWORD=zwei\n", 0o600)
		got, err := (&File{Path: path}).Get()
		if err != nil || got != "eins" {
			t.Errorf("Get() = %q, %v; want %q, nil", got, err, "eins")
		}

		emptyFirst := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, emptyFirst, "password=\nARD_PLUS_PASSWORD=zwei\n", 0o600)
		got, err = (&File{Path: emptyFirst}).Get()
		if err != nil || got != "zwei" {
			t.Errorf("Get() = %q, %v; want %q, nil", got, err, "zwei")
		}
	})

	t.Run("clear removes only the password line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "username=alice\nnote=privat\n# comment\npassword=alt\n", 0o600)
		f := &File{Path: path}
		if err := f.Clear(); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		want := "username=alice\nnote=privat\n# comment\n"
		got := readFile(t, path)
		if got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
		if strings.Contains(got, "password=") {
			t.Errorf("content still holds a password line: %q", got)
		}
		if got := fileMode(t, path); got != 0o600 {
			t.Errorf("file mode = %o, want 600", got)
		}
		if _, err := f.Get(); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get() after Clear = %v, want ErrNotFound", err)
		}
	})

	t.Run("clear removes an ARD_PLUS_PASSWORD line too", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		writeCredentials(t, path, "ARD_PLUS_PASSWORD=alt\nusername=alice\n", 0o600)
		f := &File{Path: path}
		if err := f.Clear(); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		if got, want := readFile(t, path), "username=alice\n"; got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	})

	t.Run("clear on a missing file does nothing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials")
		f := &File{Path: path}
		if err := f.Clear(); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Clear created %q or failed: %v", path, err)
		}
	})
}
