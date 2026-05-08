package picker

import "testing"

func TestQueryToGlob(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Substring queries get wrapped so the glob matches the same
		// files the on-screen filter showed.
		{"test", "*test*.parquet"},
		{"foo_bar", "*foo_bar*.parquet"},
		// Empty query → match everything.
		{"", "*.parquet"},
		{"   ", "*.parquet"},
		// User-typed globs are trusted; .parquet is appended only when missing.
		{"test_*", "test_*.parquet"},
		{"test_*.parquet", "test_*.parquet"},
		{"data[0-9].parquet", "data[0-9].parquet"},
		{"*", "*.parquet"},
	}
	for _, tc := range cases {
		if got := queryToGlob(tc.in); got != tc.want {
			t.Errorf("queryToGlob(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
