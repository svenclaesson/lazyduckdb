// Package update is a best-effort GitHub release check. It runs
// before the TUI takes over the screen so the user sees the message,
// and it silently swallows network/parse errors — being unable to
// reach GitHub must never block launching the app.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releasesURL = "https://api.github.com/repos/svenclaesson/lazyduckdb/releases/latest"

// InstallCommand is what the user pastes to upgrade. Centralized so
// the README and the prompt can't drift apart.
const InstallCommand = "go install github.com/svenclaesson/lazyduckdb/cmd/lazyduckdb@latest"

// Result describes the outcome of a release check. Available is true
// only when LatestTag is strictly newer than the running version.
type Result struct {
	LatestTag string
	Available bool
}

// Check fetches the latest release tag from GitHub and compares it to
// current. Any error (network down, rate limited, malformed payload)
// returns Available=false with a nil error — callers should treat a
// failed check as "no update", not as a startup failure.
func Check(ctx context.Context, current string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return Result{}, nil
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, nil
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, nil
	}
	if payload.TagName == "" {
		return Result{}, nil
	}

	return Result{
		LatestTag: payload.TagName,
		Available: newer(payload.TagName, current),
	}, nil
}

// CheckWithTimeout is the convenience wrapper main uses — bounded so
// a slow GitHub never delays the TUI by more than the timeout.
func CheckWithTimeout(current string, timeout time.Duration) Result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r, _ := Check(ctx, current)
	return r
}

// newer returns true when latest is a strictly higher semver-ish
// version than current. Handles "v" prefixes and any number of dotted
// numeric components; falls back to string inequality when either
// side has non-numeric segments we don't understand.
func newer(latest, current string) bool {
	l, lOK := parseVersion(latest)
	c, cOK := parseVersion(current)
	if !lOK || !cOK {
		return strings.TrimPrefix(latest, "v") != strings.TrimPrefix(current, "v") &&
			strings.TrimPrefix(latest, "v") > strings.TrimPrefix(current, "v")
	}
	for i := 0; i < len(l) || i < len(c); i++ {
		var a, b int
		if i < len(l) {
			a = l[i]
		}
		if i < len(c) {
			b = c[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

func parseVersion(s string) ([]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// PromptText is the message shown to the user when an update is
// available. Pulled out so it can be unit-tested without touching
// stdin/stdout.
func PromptText(latest, current string) string {
	if !strings.HasPrefix(latest, "v") {
		latest = "v" + latest
	}
	return fmt.Sprintf("lazyduckdb %s is available (you have v%s).\n  upgrade: %s\n  type yes to install now, or press enter to skip and continue: ",
		latest, strings.TrimPrefix(current, "v"), InstallCommand)
}

// Install runs InstallCommand, streaming its stdout/stderr to the
// caller's terminal so the user sees `go install` progress. Returns
// the path to the freshly-installed binary on success — that's the
// one main.go exec's into so the new version takes over without a
// manual relaunch.
func Install(ctx context.Context) (string, error) {
	gobin, err := goBinDir(ctx)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "go", "install", "github.com/svenclaesson/lazyduckdb/cmd/lazyduckdb@latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go install: %w", err)
	}
	bin := filepath.Join(gobin, binaryName())
	if _, err := os.Stat(bin); err != nil {
		// `go install` succeeded but we can't find the binary where we
		// expect it — treat as a soft failure so the caller falls back
		// to "please relaunch" rather than exec'ing a stale path.
		return "", fmt.Errorf("installed binary not found at %s: %w", bin, err)
	}
	return bin, nil
}

// goBinDir returns the directory `go install` writes binaries to.
// Honors GOBIN if set, otherwise falls back to `go env GOPATH`/bin —
// the same precedence the go toolchain uses.
func goBinDir(ctx context.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("GOBIN")); v != "" {
		return v, nil
	}
	out, err := exec.CommandContext(ctx, "go", "env", "GOPATH").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOPATH: %w", err)
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return "", errors.New("empty GOPATH")
	}
	// `go env GOPATH` may return a list separated by os.PathListSeparator;
	// the first entry is the one `go install` uses.
	if i := strings.IndexByte(gopath, os.PathListSeparator); i >= 0 {
		gopath = gopath[:i]
	}
	return filepath.Join(gopath, "bin"), nil
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "lazyduckdb.exe"
	}
	return "lazyduckdb"
}

// ErrSkipped is returned by Prompt to signal the user dismissed the
// update — currently informational only, the caller continues either
// way. Kept so future flows (e.g. auto-quit-and-upgrade) can branch.
var ErrSkipped = errors.New("update skipped")
