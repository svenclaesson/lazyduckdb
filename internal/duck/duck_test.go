package duck

import (
	"math"
	"testing"
)

func TestFormatFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{20200825, "20200825"},     // a date-as-float — no scientific notation
		{1.5, "1.5"},               // fractional stays fractional
		{0, "0"},                   // zero
		{-42, "-42"},               // negative whole
		{1e20, "100000000000000000000"},
		{math.Inf(1), "+Inf"},
	}
	for _, tc := range cases {
		got := formatFloat(tc.in)
		if got != tc.want {
			t.Errorf("formatFloat(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOpenAndQuery(t *testing.T) {
	const path = "/tmp/lazyduck_sample.parquet"
	s, err := Open(path)
	if err != nil {
		t.Skipf("sample parquet missing at %s: %v", path, err)
	}
	defer s.Close()

	if len(s.Columns) == 0 {
		t.Fatal("expected at least one column")
	}
	rs, err := s.Query("SELECT * FROM t", 5)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs.Rows) == 0 {
		t.Fatal("expected rows")
	}
	if len(rs.Rows) > 5 {
		t.Fatalf("limit=5 but got %d rows", len(rs.Rows))
	}
	if len(rs.Columns) != len(s.Columns) {
		t.Fatalf("col mismatch: %d vs %d", len(rs.Columns), len(s.Columns))
	}
	if rs.TotalRows <= 0 {
		t.Fatalf("expected positive TotalRows, got %d", rs.TotalRows)
	}
	if rs.TotalRows < len(rs.Rows) {
		t.Fatalf("TotalRows=%d < returned rows=%d", rs.TotalRows, len(rs.Rows))
	}
}
