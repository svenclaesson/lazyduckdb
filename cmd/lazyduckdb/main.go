package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/svenclaesson/lazyduckdb/internal/app"
	"github.com/svenclaesson/lazyduckdb/internal/duck"
	"github.com/svenclaesson/lazyduckdb/internal/picker"
	"github.com/svenclaesson/lazyduckdb/internal/update"
)

var version = "0.1.12"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [parquet_file ...]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "  Pass one or more parquet files; they're mounted as t1, t2, ...")
		fmt.Fprintln(os.Stderr, "  With no arguments, a picker lists *.parquet in the current directory.")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// Probe GitHub for a newer release before either the picker or the
	// main TUI runs — both are Bubble Tea programs, and any prompt we
	// print after them risks being scrolled past or hidden when alt
	// screen activates. Skipped when stdin/stdout aren't a TTY (CI,
	// pipes) so non-interactive runs don't block on Enter.
	if isInteractive() {
		if r := update.CheckWithTimeout(version, 1500*time.Millisecond); r.Available {
			fmt.Print(update.PromptText(r.LatestTag, version))
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.EqualFold(strings.TrimSpace(line), "yes") {
				if err := installAndRelaunch(); err != nil {
					// Print and continue — we'd rather launch the old
					// version than refuse to start the app at all.
					fmt.Fprintf(os.Stderr, "install failed: %v\nstarting current version...\n", err)
				}
			}
		}
	}

	args := flag.Args()
	var (
		paths      []string
		groupLabel string
		groupFiles []string
	)
	if len(args) == 0 {
		chosen, group, err := chooseFromCWD()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if chosen == "" {
			// User cancelled — exit silently, same convention as fzf.
			return
		}
		if len(group) > 0 {
			// Picker committed a group via ctrl+a — keep the label
			// for display and use the resolved file list for the
			// actual mount.
			groupLabel = chosen
			groupFiles = group
		} else {
			if abs, err := filepath.Abs(chosen); err == nil {
				chosen = abs
			}
			paths = []string{chosen}
		}
	} else {
		paths = make([]string, len(args))
		for i, a := range args {
			p, err := filepath.Abs(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolve path %q: %v\n", a, err)
				os.Exit(1)
			}
			// Glob args (e.g. quoted '*.parquet') skip the stat — the
			// duck layer does its own expansion via filepath.Glob and
			// hands the pattern to read_parquet for unioning. Stat'ing
			// a glob would always fail.
			if !duck.IsGlob(p) {
				if _, err := os.Stat(p); err != nil {
					fmt.Fprintf(os.Stderr, "parquet file: %v\n", err)
					os.Exit(1)
				}
			}
			paths[i] = p
		}
	}

	var session *duck.Session
	if len(groupFiles) > 0 {
		var err error
		session, err = duck.OpenGroup(groupLabel, groupFiles)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open: %v\n", err)
			os.Exit(1)
		}
	} else {
		var err error
		session, err = duck.Open(paths[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "open: %v\n", err)
			os.Exit(1)
		}
	}
	defer session.Close()
	// paths is empty when the picker committed a group (handled
	// above via OpenGroup) — skip the attach loop in that case.
	if len(paths) > 1 {
		for _, p := range paths[1:] {
			view, aerr := session.Attach(p)
			if aerr != nil {
				fmt.Fprintf(os.Stderr, "attach %s: %v\n", p, aerr)
				os.Exit(1)
			}
			_ = view
		}
	}

	model := app.NewModel(session)

	// v2 moved AltScreen to the View, so no program option needed here.
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}

// installAndRelaunch runs `go install ...@latest` and then exec's
// the freshly-installed binary in place of this process, preserving
// argv so the user lands in the same state they asked for. The Go
// toolchain has up to a few minutes of latency between a `gh
// release` and the proxy serving the new module version; the install
// itself enforces its own timeout, but we leave the parent context
// uncancelled so a slow download still completes.
func installAndRelaunch() error {
	bin, err := update.Install(context.Background())
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Propagate the new binary's exit code so shell pipelines see
		// the right status. exec.ExitError carries it directly; any
		// other error means we failed to spawn at all.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil // unreachable
}

// isInteractive reports whether both stdin and stdout look like a
// terminal. We need stdin so the user can answer the prompt, and
// stdout so the prompt is actually rendered to a person. Uses
// golang.org/x/term — it handles platform quirks (Windows ConPTY,
// macOS pty edge cases) that a plain ModeCharDevice check misses.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func chooseFromCWD() (string, []string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("getwd: %w", err)
	}
	files, err := picker.FindParquetFiles(cwd)
	if err != nil {
		return "", nil, fmt.Errorf("scan %s: %w", cwd, err)
	}
	if len(files) == 0 {
		return "", nil, fmt.Errorf("no .parquet files in %s — pass a path as argument", cwd)
	}
	if len(files) == 1 {
		// Single file — skip the picker and just use it.
		return files[0], nil, nil
	}
	return picker.Pick(files)
}
