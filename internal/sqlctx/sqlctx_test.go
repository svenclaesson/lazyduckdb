package sqlctx

import (
	"strings"
	"testing"
)

func TestAt(t *testing.T) {
	cases := []struct {
		// input contains a literal '^' marking the caret. The marker is
		// stripped before passing to At; the byte index where it sat is
		// the caret offset.
		in   string
		want Context
	}{
		// pre-clause / unknown
		{"^", Unknown},
		{"   ^", Unknown},
		{"SELECTED ^", Unknown}, // false-keyword: not a real SELECT

		// SELECT column list
		{"SELECT ^", ColumnList},
		{"SELECT id, ^", ColumnList},
		{"SELECT id, c^", ColumnList},
		{"SELECT id^", ColumnList}, // mid-identifier, no terminator yet
		{"select * from t where ^", Predicate},

		// awaitsComma rule
		{"SELECT id ^", Unknown},
		{"SELECT id f^", Unknown},
		{"SELECT * f^", Unknown},
		{"SELECT 'x' ^", Unknown},
		{"SELECT 1 ^", Unknown},
		{"SELECT count(*) ^", Unknown},
		{"SELECT 'it''s ok' , ^", ColumnList}, // escaped quote doesn't end literal

		// table position
		{"SELECT * FROM ^", TablePosition},
		{"SELECT * FROM t^", TablePosition},
		{"SELECT * FROM t ^", TablePosition},
		{"SELECT * FROM t1, ^", TablePosition},

		// JOIN flavors → table position
		{"SELECT * FROM t JOIN ^", TablePosition},
		{"SELECT * FROM t LEFT JOIN ^", TablePosition},
		{"SELECT * FROM t LEFT OUTER JOIN ^", TablePosition},
		{"SELECT * FROM t INNER JOIN x^", TablePosition},
		{"SELECT * FROM t CROSS JOIN ^", TablePosition},

		// JOIN ... ON → predicate
		{"SELECT * FROM t LEFT JOIN x ON ^", Predicate},
		{"SELECT * FROM t INNER JOIN x ON ^", Predicate},
		{"SELECT * FROM t JOIN x ON x.id = t.id AND ^", Predicate},

		// USING (...) → column list inside the paren
		{"SELECT * FROM t JOIN x USING (^)", ColumnList},
		{"SELECT * FROM t JOIN x USING (a, ^)", ColumnList},
		{"SELECT * FROM t JOIN x USING (a, b^)", ColumnList},
		{"SELECT * FROM t JOIN x USING (a) ^", TablePosition}, // closed paren → back to FROM/JOIN context

		// WHERE / AND / OR / HAVING
		{"SELECT * FROM t WHERE ^", Predicate},
		{"SELECT * FROM t WHERE id = 1 AND ^", Predicate},
		{"SELECT * FROM t WHERE id = 1 OR ^", Predicate},
		{"SELECT * FROM t WHERE id = 'x' AND ^", Predicate},
		{"SELECT * FROM t GROUP BY id HAVING ^", Predicate},

		// GROUP BY / ORDER BY → column list
		{"SELECT * FROM t GROUP BY ^", ColumnList},
		{"SELECT * FROM t ORDER BY ^", ColumnList},
		{"SELECT * FROM t GROUP BY a, ^", ColumnList},
		{"SELECT * FROM t ORDER BY a DESC, ^", ColumnList},
		{"SELECT * FROM t GROUP BY a ^", Unknown}, // awaitsComma also applies in GROUP BY

		// strings & comments
		{"SELECT 'oops ^", StringLiteral},
		{"SELECT \"col ^", StringLiteral}, // quoted identifier treated like string for skip
		{"SELECT * -- comment ^", Comment},
		{"SELECT */* block ^ */ id", Comment},
		{"SELECT * /* unterminated ^", Comment},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			text, caret := splitMarker(t, tc.in)
			got := At(text, caret)
			if got != tc.want {
				t.Errorf("At(%q, %d) = %v, want %v", text, caret, got, tc.want)
			}
		})
	}
}

func splitMarker(t *testing.T, in string) (string, int) {
	t.Helper()
	i := strings.IndexByte(in, '^')
	if i < 0 {
		t.Fatalf("missing ^ caret marker in %q", in)
	}
	return in[:i] + in[i+1:], i
}
