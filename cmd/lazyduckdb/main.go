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

var version = "0.1.19"

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
				if err := installAndRelaunch(r.LatestTag); err != nil {
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
		// Multi-arg invocations are usually a shell expanding a glob
		// (`lazyduckdb Ford*.parquet` → `Ford1.parquet Ford2.parquet
		// ...`), and the user's mental model is a single unioned
		// table — not three separate t1/t2/t3 views to JOIN. When the
		// resolved paths look like that pattern, fold them into a
		// group so queries against `t` / `t1` see all rows. Mixed or
		// unrelated paths fall through to the original t1/t2/t3
		// behavior, which is still useful for cross-file joins.
		if len(paths) > 1 {
			if label, files, ok := detectGlobExpansion(paths); ok {
				groupLabel = label
				groupFiles = files
				paths = nil
			}
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

// detectGlobExpansion decides whether a multi-arg invocation looks
// like the shell expanded a single glob pattern. When yes, it
// reconstructs the pattern (e.g. "/abs/dir/Ford*.parquet") so the
// duck layer can treat the files as one unioned view. Conservative
// on purpose: it requires same directory, no already-glob args, and
// either a basename prefix of ≥ 2 chars or a non-trivial common
// suffix (more than just the extension). Anything else falls back
// to t1/t2/t3, which is what people doing real cross-file joins
// expect.
func detectGlobExpansion(paths []string) (string, []string, bool) {
	if len(paths) < 2 {
		return "", nil, false
	}
	for _, p := range paths {
		if duck.IsGlob(p) {
			return "", nil, false
		}
	}
	dir := filepath.Dir(paths[0])
	bases := make([]string, len(paths))
	bases[0] = filepath.Base(paths[0])
	for i := 1; i < len(paths); i++ {
		if filepath.Dir(paths[i]) != dir {
			return "", nil, false
		}
		bases[i] = filepath.Base(paths[i])
	}
	allSame := true
	for i := 1; i < len(bases); i++ {
		if bases[i] != bases[0] {
			allSame = false
			break
		}
	}
	if allSame {
		return "", nil, false
	}
	prefix := commonPrefix(bases)
	suffix := commonSuffix(bases)
	ext := filepath.Ext(bases[0])
	// Require ≥ 2 chars of meaningful prefix or suffix-body so two
	// unrelated files that just happen to share a trailing letter
	// before the extension (`cars.parquet`, `trucks.parquet`) don't
	// get coerced into a group.
	suffixBody := len(suffix) - len(ext)
	if suffixBody < 0 {
		suffixBody = 0
	}
	if len(prefix) < 2 && suffixBody < 2 {
		return "", nil, false
	}
	return filepath.Join(dir, prefix+"*"+suffix), paths, true
}

func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		n := 0
		for n < len(p) && n < len(s) && p[n] == s[n] {
			n++
		}
		p = p[:n]
		if p == "" {
			return ""
		}
	}
	return p
}

func commonSuffix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	s0 := ss[0]
	n := len(s0)
	for _, s := range ss[1:] {
		m := 0
		for m < n && m < len(s) && s0[len(s0)-1-m] == s[len(s)-1-m] {
			m++
		}
		n = m
		if n == 0 {
			return ""
		}
	}
	return s0[len(s0)-n:]
}

// installAndRelaunch runs `go install ...@<tag>` and then exec's
// the freshly-installed binary in place of this process, preserving
// argv so the user lands in the same state they asked for. We pin
// to the exact tag we just resolved from the GitHub releases API
// instead of @latest because the module proxy's @latest index can
// lag a fresh release by minutes, but explicit-version fetches are
// resolved on demand and work immediately.
func installAndRelaunch(tag string) error {
	bin, err := update.Install(context.Background(), tag)
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
