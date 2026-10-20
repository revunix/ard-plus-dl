// Package cli is the headless entry point: argument handling, dependency
// checks, wiring and exit codes.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/revunix/ard-plus-dl/internal/config"
)

func useTUI(opts *config.Options, stdinTTY, stdoutTTY bool) bool {
	return stdinTTY && stdoutTTY && !opts.Automatic && !opts.Batch()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// readLine reads one line from the reader. It gives up when the context is
// cancelled, so that Ctrl+C does not leave the process in a prompt.
func readLine(ctx context.Context, reader io.Reader) string {
	buf := make([]byte, 0, 64)
	one := make([]byte, 1)
	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ""
			default:
			}
		}
		n, err := reader.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(buf))
}

func promptCredentials(ctx context.Context, opts *config.Options, stdin io.Reader, stdout io.Writer, stdinFile *os.File) {
	if opts.Username == "" {
		fmt.Fprint(stdout, "ARD Plus username: ")
		opts.Username = readLine(ctx, stdin)
	}
	if opts.Password == "" {
		fmt.Fprint(stdout, "ARD Plus password: ")
		if stdinFile != nil {
			if data, err := term.ReadPassword(int(stdinFile.Fd())); err == nil {
				opts.Password = string(data)
				fmt.Fprintln(stdout)
				return
			}
		}
		opts.Password = readLine(ctx, stdin)
	}
}

func credentialsHelp(opts *config.Options) string {
	return "Credentials missing! Provide them via one of:\n" +
		"  - environment variables: ARD_PLUS_USER and ARD_PLUS_PASSWORD\n" +
		"  - credentials file: " + opts.CredentialsFile + " (chmod 600, lines: username=... / password=...)\n" +
		"  - positional arguments (visible to other processes via ps - not recommended)\n"
}

func resolveWorkDir(opts *config.Options) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if !opts.Batch() {
		return cwd, nil
	}
	abs, err := filepath.Abs(opts.LinksFile)
	if err != nil {
		return "", err
	}
	opts.LinksFile = abs
	return filepath.Dir(abs), nil
}

func ensureWritableDir(path string) bool {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	probe := filepath.Join(path, ".ard-plus-dl-write-test")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	file.Close()
	_ = os.Remove(probe)
	return true
}

func checkDependencies(env config.Env) (ffmpeg, ffprobe string, err error) {
	ffmpeg = env("ARD_PLUS_FFMPEG")
	if ffmpeg == "" {
		ffmpeg, _ = exec.LookPath("ffmpeg")
	}
	ffprobe = env("ARD_PLUS_FFPROBE")
	if ffprobe == "" {
		ffprobe, _ = exec.LookPath("ffprobe")
	}
	var missing []string
	if ffmpeg == "" {
		missing = append(missing, "ffmpeg")
	}
	if ffprobe == "" {
		missing = append(missing, "ffprobe")
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("Missing required dependencies: %s", strings.Join(missing, " "))
	}
	return ffmpeg, ffprobe, nil
}

// headlessUI prints plain lines and asks for the same answers the Bash
