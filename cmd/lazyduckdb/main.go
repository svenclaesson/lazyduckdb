package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/svenclaesson/lazyduckdb/internal/app"
	"github.com/svenclaesson/lazyduckdb/internal/duck"
	"github.com/svenclaesson/lazyduckdb/internal/picker"
)

var version = "0.1"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version (shorthand)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <parquet_file>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	args := flag.Args()
	var absPath string
	switch len(args) {
	case 0:
		chosen, err := choosFromCWD()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if chosen == "" {
			// User cancelled — exit silently, same convention as fzf.
			return
		}
		absPath = chosen
	case 1:
		p, err := filepath.Abs(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintf(os.Stderr, "parquet file: %v\n", err)
			os.Exit(1)
		}
		absPath = p
	default:
		flag.Usage()
		os.Exit(2)
	}

	session, err := duck.Open(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()

	model := app.NewModel(session)

	// v2 moved AltScreen to the View, so no program option needed here.
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}

func choosFromCWD() (string, error) {
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
