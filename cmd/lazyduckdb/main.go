package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/svenclaesson/lazyduckdb/internal/app"
	"github.com/svenclaesson/lazyduckdb/internal/duck"
	"github.com/svenclaesson/lazyduckdb/internal/picker"
	"github.com/svenclaesson/lazyduckdb/internal/update"
)

var version = "0.1.11"

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
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
	}

	args := flag.Args()
	var paths []string
	if len(args) == 0 {
		chosen, err := chooseFromCWD()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if chosen == "" {
			// User cancelled — exit silently, same convention as fzf.
			return
		}
		paths = []string{chosen}
	} else {
		paths = make([]string, len(args))
		for i, a := range args {
			p, err := filepath.Abs(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolve path %q: %v\n", a, err)
				os.Exit(1)
			}
			if _, err := os.Stat(p); err != nil {
				fmt.Fprintf(os.Stderr, "parquet file: %v\n", err)
				os.Exit(1)
			}
			paths[i] = p
		}
	}

	session, err := duck.Open(paths[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()
	for _, p := range paths[1:] {
		view, aerr := session.Attach(p)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "attach %s: %v\n", p, aerr)
			os.Exit(1)
		}
		_ = view
	}

	model := app.NewModel(session)

	// v2 moved AltScreen to the View, so no program option needed here.
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}

// isInteractive reports whether both stdin and stdout look like a
// terminal. We need stdin so the user can answer the prompt, and
// stdout so the prompt is actually rendered to a person. Uses
// golang.org/x/term — it handles platform quirks (Windows ConPTY,
// macOS pty edge cases) that a plain ModeCharDevice check misses.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func chooseFromCWD() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	files, err := picker.FindParquetFiles(cwd)
	if err != nil {
		return "", fmt.Errorf("scan %s: %w", cwd, err)
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no .parquet files in %s — pass a path as argument", cwd)
	}
	if len(files) == 1 {
		// Single file — skip the picker and just use it.
		return files[0], nil
	}
	return picker.Pick(files)
}
