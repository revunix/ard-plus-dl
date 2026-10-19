// Package secret stores the ARD Plus password. It prefers the operating
// system's keychain and falls back to the 0600 credentials file when no
// keychain is reachable (for example in a container without a session bus).
package secret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when no password is stored.
var ErrNotFound = errors.New("no password stored")

// Store keeps the password.
type Store interface {
	Get() (string, error)
	Set(password string) error
	Clear() error
	// Backend names the storage in user-facing messages.
	Backend() string
}

const (
	keyringService = "ard-plus-dl"
	keyringUser    = "password"
)

// Open returns a keychain-backed store when the platform keychain answers, and
// otherwise a store backed by the credentials file.
func Open(credentialsFile string) Store {
	keychain := &Keychain{}
	if err := keychain.available(); err != nil {
		return &File{Path: credentialsFile}
	}
	return keychain
}

// Keychain uses the operating system keychain (macOS Keychain, Windows
// Credential Manager, Linux Secret Service).
type Keychain struct{}

func (k *Keychain) available() error {
	_, err := keyring.Get(keyringService, keyringUser)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Get returns the stored password.
func (k *Keychain) Get() (string, error) {
	value, err := keyring.Get(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return value, err
}

// Set stores the password.
func (k *Keychain) Set(password string) error {
	return keyring.Set(keyringService, keyringUser, password)
}

// Clear removes the password.
func (k *Keychain) Clear() error {
	err := keyring.Delete(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Backend names the storage.
func (k *Keychain) Backend() string { return "Schlüsselbund" }

// File stores the password in the credentials file, keeping the other keys.
type File struct {
	Path string
}

// Get reads the password from the credentials file.
func (f *File) Get() (string, error) {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		key, value, found := strings.Cut(line, "=")
		if found && (key == "password" || key == "ARD_PLUS_PASSWORD") && value != "" {
			return value, nil
		}
	}
	return "", ErrNotFound
}

// Set writes the password, replacing an existing password line.
func (f *File) Set(password string) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	var kept []string
	if data, err := os.ReadFile(f.Path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			key, _, found := strings.Cut(line, "=")
			if found && (key == "password" || key == "ARD_PLUS_PASSWORD") {
				continue
			}
			kept = append(kept, line)
		}
	}
	// Drop the empty element the trailing newline produces, so that repeated
	// writes do not pile up blank lines above the password.
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	kept = append(kept, "password="+password)
	content := strings.TrimLeft(strings.Join(kept, "\n"), "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(f.Path, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Chmod(f.Path, 0o600)
}

// Clear removes the password from the credentials file.
func (f *File) Clear() error {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSuffix(line, "\r")
		key, _, found := strings.Cut(trimmed, "=")
		if found && (key == "password" || key == "ARD_PLUS_PASSWORD") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return os.WriteFile(f.Path, []byte(strings.TrimLeft(strings.Join(kept, "\n"), "\n")), 0o600)
}

// Backend names the storage.
func (f *File) Backend() string { return fmt.Sprintf("Datei %s", f.Path) }
