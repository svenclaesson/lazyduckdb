package main

import (
	"path/filepath"
	"testing"
)

func TestDetectGlobExpansion(t *testing.T) {
	cases := []struct {
		name      string
		paths     []string
		wantOK    bool
		wantLabel string
	}{
		{
			name:      "common prefix and extension",
			paths:     []string{"/d/Ford1.parquet", "/d/Ford2.parquet", "/d/Ford3.parquet"},
			wantOK:    true,
			wantLabel: filepath.Join("/d", "Ford*.parquet"),
		},
		{
			name:      "common prefix with hyphen",
			paths:     []string{"/d/data-2024.parquet", "/d/data-2025.parquet"},
			wantOK:    true,
			wantLabel: filepath.Join("/d", "data-202*.parquet"),
		},
		{
			// "summer" and "winter" share trailing "er" so the common
			// suffix is "er-data.parquet" — that's still a precise
			// glob for these files.
			name:      "common suffix only — different prefixes still groupable when suffix has body",
			paths:     []string{"/d/summer-data.parquet", "/d/winter-data.parquet"},
			wantOK:    true,
			wantLabel: filepath.Join("/d", "*er-data.parquet"),
		},
		{
			name:   "only extension shared, no body — keep separate",
			paths:  []string{"/d/cars.parquet", "/d/trucks.parquet"},
			wantOK: false,
		},
		{
			name:   "different directories — keep separate",
			paths:  []string{"/d/Ford1.parquet", "/e/Ford2.parquet"},
			wantOK: false,
		},
		{
			name:   "single path — no group",
			paths:  []string{"/d/Ford1.parquet"},
			wantOK: false,
		},
		{
			name:   "duplicates of same file — no group",
			paths:  []string{"/d/Ford.parquet", "/d/Ford.parquet"},
			wantOK: false,
		},
		{
			name:   "explicit glob arg present — defer to duck layer",
			paths:  []string{"/d/Ford*.parquet", "/d/Ford1.parquet"},
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			label, files, ok := detectGlobExpansion(c.paths)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (label=%q files=%v)", ok, c.wantOK, label, files)
			}
			if !ok {
				return
			}
			if label != c.wantLabel {
				t.Errorf("label = %q, want %q", label, c.wantLabel)
			}
			if len(files) != len(c.paths) {
				t.Errorf("files length = %d, want %d", len(files), len(c.paths))
			}
		})
	}
}
