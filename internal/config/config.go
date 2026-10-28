// Package config parses the command line, resolves credentials and computes the
// application's paths. The rules and messages keep the original command-line
// behavior so existing scripts and habits keep working.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Env looks up an environment variable; injectable for tests.
type Env func(string) string

// OSEnv is the process environment.
func OSEnv(key string) string { return os.Getenv(key) }

// ErrHelp, ErrVersion and ErrNoURL signal conditions the caller handles
// itself: printing usage/version, or (in a terminal) collecting a URL.
var (
	ErrHelp    = errors.New("help requested")
	ErrVersion = errors.New("version requested")
	ErrNoURL   = errors.New("URL or --links-file required")
)

// Options is the parsed command line plus resolved paths.
type Options struct {
	URL             string
	LinksFile       string
	Automatic       bool
	ForceRedownload bool
	Username        string
	Password        string

	// Skip is the number of leading episodes skipped in the first processed
	// season; 1 means "do not skip".
	Skip int
	// SkipRaw is the unconverted positional value, validated by ResolveSkip.
	SkipRaw string

	// LibraryMode is one of "", "list", "check", "download-new", "add".
	LibraryMode string
	// AddURL is the URL of --add.
	AddURL string
	// LibraryPath is the SQLite database.
	LibraryPath string
	// JSON prints the library in the machine readable form.
	JSON bool

	DownloadsDir    string
	TokenFile       string
	CredentialsFile string
	WorkDir         string
}

// Batch reports whether the options select the links-file mode.
func (o *Options) Batch() bool { return o.LinksFile != "" }

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

func (o *Options) setLibraryMode(mode string) error {
	if o.LibraryMode != "" {
		return errors.New("Only one of --library, --check, --download-new, --add may be used")
	}
	o.LibraryMode = mode
	return nil
}

// ParseArgs parses the argument vector. Options must come before the first
// positional argument, exactly like the Bash implementation.
func ParseArgs(args []string) (*Options, error) {
	o := &Options{Skip: 1}
	var positional []string

	for i := 0; i < len(args); {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return nil, ErrHelp
		case a == "-v" || a == "--version":
			return nil, ErrVersion
		case a == "--automatic":
			o.Automatic = true
			i++
		case a == "--force-redownload":
			o.ForceRedownload = true
			i++
		case a == "--library":
			if err := o.setLibraryMode("list"); err != nil {
				return nil, err
			}
			i++
		case a == "--json":
			o.JSON = true
			i++
		case a == "--check":
			if err := o.setLibraryMode("check"); err != nil {
				return nil, err
			}
			i++
		case a == "--download-new":
			if err := o.setLibraryMode("download-new"); err != nil {
				return nil, err
			}
			i++
		case a == "--add":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, errors.New("Error: --add requires a URL")
			}
			if err := o.setLibraryMode("add"); err != nil {
				return nil, err
			}
			o.AddURL = args[i+1]
			i += 2
		case a == "--links-file":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, errors.New("Error: --links-file requires a file argument")
			}
			o.LinksFile = args[i+1]
			i += 2
		case a == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("Unknown option: %s", a)
		default:
			positional = append(positional, args[i:]...)
			i = len(args)
		}
	}

	if o.LibraryMode != "" {
		if len(positional) > 0 {
			return nil, fmt.Errorf("unexpected argument: %s", positional[0])
		}
		if o.JSON && o.LibraryMode != "list" {
			return nil, errors.New("--json requires --library")
		}
		return o, nil
	}
	if o.JSON {
		return nil, errors.New("--json requires --library")
	}

	if o.Batch() {
		if len(positional) >= 2 {
			o.Username = positional[0]
			o.Password = positional[1]
			if len(positional) >= 3 {
				o.SkipRaw = positional[2]
			}
		} else if len(positional) == 1 {
			o.SkipRaw = positional[0]
		}
		return o, nil
	}

	if len(positional) == 0 || positional[0] == "" {
		return nil, ErrNoURL
	}
	o.URL = positional[0]
	if len(positional) >= 3 {
		o.Username = positional[1]
		o.Password = positional[2]
		if len(positional) >= 4 {
			o.SkipRaw = positional[3]
		}
	} else if len(positional) == 2 {
		o.SkipRaw = positional[1]
	}
	return o, nil
}

// ResolveSkip converts the raw trailing argument. A lone non-numeric value is a
// username, not a skip count, which mirrors the Bash behaviour.
func (o *Options) ResolveSkip() error {
	if o.SkipRaw == "" {
		o.Skip = 1
		return nil
	}
	if digitsOnly.MatchString(o.SkipRaw) {
		n, err := strconv.Atoi(o.SkipRaw)
		if err != nil {
			return fmt.Errorf("Invalid skip value: %s (expected a non-negative number)", o.SkipRaw)
		}
		o.Skip = n
		return nil
	}
	if o.Username == "" && o.Password == "" {
		o.Username = o.SkipRaw
		o.Skip = 1
		return nil
	}
	return fmt.Errorf("Invalid skip value: %s (expected a non-negative number)", o.SkipRaw)
}

// CredentialsPath returns the credentials file location.
func CredentialsPath(env Env) string {
	if v := env("ARD_PLUS_CREDENTIALS_FILE"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "ard-plus-dl", "credentials")
		}
	}
	base := env("XDG_CONFIG_HOME")
	if base == "" {
		base = env("HOME") + "/.config"
	}
	return base + "/ard-plus-dl/credentials"
}

func mkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// StateDir returns the private state directory, falling back to
// <workDir>/.ard-plus-dl when the home-based location cannot be created.
func StateDir(env Env, workDir string) string {
	if runtime.GOOS == "windows" {
		if dir, err := os.UserCacheDir(); err == nil && dir != "" {
			stateDir := filepath.Join(dir, "ard-plus-dl")
			if mkdirPrivate(stateDir) == nil {
				return stateDir
			}
		}
	}
	if env("HOME") != "" {
		base := env("XDG_STATE_HOME")
		if base == "" {
			base = env("HOME") + "/.local/state"
		}
		stateDir := base + "/ard-plus-dl"
		if mkdirPrivate(stateDir) == nil {
			return stateDir
		}
	}
	fallbackDir := filepath.Join(workDir, ".ard-plus-dl")
	_ = mkdirPrivate(fallbackDir)
	return fallbackDir
}

// TokenPath returns the session token file.
func TokenPath(env Env, workDir string) string {
	return filepath.Join(StateDir(env, workDir), "token")
}

// LibraryPath returns the SQLite database path.
func LibraryPath(env Env, workDir string) string {
	if value := env("ARD_PLUS_LIBRARY"); value != "" {
		return value
	}
	return filepath.Join(StateDir(env, workDir), "library.db")
}

// LoadCredentialsFile reads username and password from a credentials file. The
// first value wins for each key. A group- or world-readable file produces a
// warning on stderr (Unix only).
func LoadCredentialsFile(path string, stderr io.Writer) (username, password string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", ""
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(stderr, "Warning: credentials file %s is readable by group/others (chmod 600 recommended).\n", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "username", "ARD_PLUS_USER":
			if username == "" {
				username = value
			}
		case "password", "ARD_PLUS_PASSWORD":
			if password == "" {
				password = value
			}
		}
	}
	return username, password
}

// ResolveCredentials fills missing credentials from the environment and the
// credentials file, in that order.
func ResolveCredentials(o *Options, env Env, stderr io.Writer) {
	if o.Username == "" {
		o.Username = env("ARD_PLUS_USER")
	}
	if o.Password == "" {
		o.Password = env("ARD_PLUS_PASSWORD")
	}
	o.CredentialsFile = CredentialsPath(env)
	if o.Username == "" || o.Password == "" {
		username, password := LoadCredentialsFile(o.CredentialsFile, stderr)
		if o.Username == "" {
			o.Username = username
		}
		if o.Password == "" {
			o.Password = password
		}
	}
}

var contentURLPattern = regexp.MustCompile(`^https?://([^/]+)(/.*)?$`)

// ValidateContentURL accepts ARD Plus content and category URLs only. The
// returned error carries the reason used in the user-facing message.
func ValidateContentURL(raw string) error {
	m := contentURLPattern.FindStringSubmatch(raw)
	if m == nil {
		return fmt.Errorf("not an absolute http(s) URL: %s", raw)
	}
	host := m[1]
	path := m[2]
	if host != "www.ardplus.de" && host != "ardplus.de" {
		return fmt.Errorf("unsupported host: %s", host)
	}
	// /tatort is the hub listing every Tatort category; /tatort-<slug> is the
	// short form of /kategorie/tatort-<slug>.
	if strings.HasPrefix(path, "/details/") || strings.HasPrefix(path, "/kategorie/") ||
		path == "/tatort" || strings.HasPrefix(path, "/tatort-") {
		return nil
	}
	return fmt.Errorf("unsupported path: %s", path)
}
