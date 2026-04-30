// Package sqlctx classifies the position of the caret inside a SQL
// query so the editor knows whether typing a word-rune should auto-open
// the column suggestion list.
//
// At is intentionally not a real parser — it's a single-pass tokenizer
// that tracks the most recent clause keyword (the "anchor") and a few
// flags. Half-typed queries are the common case, so the rules favor
// being lenient: if we can't tell what clause we're in, we return
// Unknown rather than guessing.
package sqlctx

import "strings"

type Context int

const (
	Unknown Context = iota
	ColumnList
	Predicate
	TablePosition
	StringLiteral
	Comment
)

type anchor int

const (
	aNone anchor = iota
	aSelect
	aFrom
	aJoin
	aWhere
	aGroupBy
	aOrderBy
	aHaving
	aOn
	aUsing
)

type usingFrame struct {
	prev   anchor
	awaits bool
	depth  int // paren depth at the open paren
}

// At reports the context at byte offset caret in text.
func At(text string, caret int) Context {
	if caret < 0 {
		caret = 0
	}
	if caret > len(text) {
		caret = len(text)
	}

	var (
		anc        anchor
		awaits     bool
		parenDepth int
		stack      []usingFrame
	)

	pos := 0
	for pos < caret {
		c := text[pos]

		// '...' string literal (SQL '' escape).
		if c == '\'' {
			end := scanSingle(text, pos+1)
			if end < 0 || end >= caret {
				return StringLiteral
			}
			if isColumnAnchor(anc) {
				awaits = true
			}
			pos = end + 1
			continue
		}
		// "..." quoted identifier — treat like string for skip purposes.
		if c == '"' {
			end := scanDouble(text, pos+1)
			if end < 0 || end >= caret {
				return StringLiteral
			}
			if isColumnAnchor(anc) {
				awaits = true
			}
			pos = end + 1
			continue
		}
		// -- line comment to \n.
		if c == '-' && pos+1 < len(text) && text[pos+1] == '-' {
			nl := strings.IndexByte(text[pos:], '\n')
			if nl < 0 {
				return Comment
			}
			end := pos + nl
			if caret <= end {
				return Comment
			}
			pos = end + 1
			continue
		}
		// /* ... */ block comment.
		if c == '/' && pos+1 < len(text) && text[pos+1] == '*' {
			close := strings.Index(text[pos+2:], "*/")
			if close < 0 {
				return Comment
			}
			end := pos + 2 + close + 2
			if caret < end {
				return Comment
			}
			pos = end
			continue
		}

		switch {
		case c == '(':
			parenDepth++
			pos++
		case c == ')':
			parenDepth--
			if len(stack) > 0 && stack[len(stack)-1].depth == parenDepth {
				fr := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				anc = fr.prev
				awaits = fr.awaits
			}
			// A closed paren is a value-token in column anchors
			// (function call result, parenthesized expression).
			if isColumnAnchor(anc) {
				awaits = true
			}
			pos++
		case c == ',':
			if isColumnAnchor(anc) {
				awaits = false
			}
			pos++
		case c == '*':
			if isColumnAnchor(anc) && !awaits {
				awaits = true
			}
			pos++
		case isDigit(c):
			j := pos
			for j < len(text) && (isDigit(text[j]) || text[j] == '.') {
				j++
			}
			if j < caret && isColumnAnchor(anc) {
				awaits = true
			}
			pos = j
		case isWordStart(c):
			j := pos
			for j < len(text) && isWordRune(text[j]) {
				j++
			}
			word := text[pos:j]
			terminated := j < caret
			if !terminated {
				return contextResult(anc, awaits, parenDepth)
			}
			switch keywordOf(word) {
			case kwSelect:
				anc, awaits = aSelect, false
			case kwFrom:
				anc, awaits = aFrom, false
			case kwWhere:
				anc, awaits = aWhere, false
			case kwHaving:
				anc, awaits = aHaving, false
			case kwOn:
				anc, awaits = aOn, false
			case kwJoin:
				anc, awaits = aJoin, false
			case kwGroup:
				k := skipWS(text, j)
				if peeksKeyword(text, k, "by") {
					anc, awaits = aGroupBy, false
					j = k + 2
				}
			case kwOrder:
				k := skipWS(text, j)
				if peeksKeyword(text, k, "by") {
					anc, awaits = aOrderBy, false
					j = k + 2
				}
			case kwUsing:
				k := skipWS(text, j)
				if k < len(text) && text[k] == '(' {
					stack = append(stack, usingFrame{prev: anc, awaits: awaits, depth: parenDepth})
					parenDepth++
					anc, awaits = aUsing, false
					j = k + 1
				}
			case kwAnd, kwOr, kwBy,
				kwLeft, kwRight, kwInner, kwOuter, kwFull, kwCross, kwNatural,
				kwAsc, kwDesc, kwAs:
				// no-op — these don't change anchor and aren't value-tokens.
			default:
				// Plain identifier. In a column anchor it's a value-token.
				if isColumnAnchor(anc) {
					awaits = true
				}
			}
			pos = j
		default:
			pos++
		}
	}

	return contextResult(anc, awaits, parenDepth)
}

func contextResult(anc anchor, awaits bool, _ int) Context {
	if isColumnAnchor(anc) && awaits {
		return Unknown
	}
	switch anc {
	case aSelect, aGroupBy, aOrderBy, aUsing:
		return ColumnList
	case aWhere, aHaving, aOn:
		return Predicate
	case aFrom, aJoin:
		return TablePosition
	}
	return Unknown
}

func isColumnAnchor(a anchor) bool {
	switch a {
	case aSelect, aGroupBy, aOrderBy, aUsing:
		return true
	}
	return false
}

// scanSingle finds the index of the closing single-quote starting from
// `from`, handling SQL standard `''` escaping. Returns -1 if unterminated.
func scanSingle(text string, from int) int {
	for i := from; i < len(text); i++ {
		if text[i] != '\'' {
			continue
		}
		if i+1 < len(text) && text[i+1] == '\'' {
			i++ // skip the escaped pair
			continue
		}
		return i
	}
	return -1
}

// scanDouble finds the closing `"` for a quoted identifier. DuckDB uses
// `""` to escape an embedded double quote, mirroring the single-quote rule.
func scanDouble(text string, from int) int {
	for i := from; i < len(text); i++ {
		if text[i] != '"' {
			continue
		}
		if i+1 < len(text) && text[i+1] == '"' {
			i++
			continue
		}
		return i
	}
	return -1
}

func skipWS(text string, from int) int {
	for from < len(text) {
		c := text[from]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			from++
			continue
		}
		break
	}
	return from
}

// peeksKeyword reports whether text[from:] begins with the given keyword
// (case-insensitive) and is followed by a non-word boundary.
func peeksKeyword(text string, from int, kw string) bool {
	if from+len(kw) > len(text) {
		return false
	}
	for i := 0; i < len(kw); i++ {
		if lower(text[from+i]) != kw[i] {
			return false
		}
	}
	end := from + len(kw)
	if end < len(text) && isWordRune(text[end]) {
		return false
	}
	return true
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
func isWordStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}
func isWordRune(b byte) bool { return isWordStart(b) || isDigit(b) }

type keyword int

const (
	kwNone keyword = iota
	kwSelect
	kwFrom
	kwWhere
	kwHaving
	kwOn
	kwJoin
	kwGroup
	kwOrder
	kwBy
	kwUsing
	kwAnd
	kwOr
	kwLeft
	kwRight
	kwInner
	kwOuter
	kwFull
	kwCross
	kwNatural
	kwAsc
	kwDesc
	kwAs
)

func keywordOf(word string) keyword {
	// ASCII lowercase compare without allocating in the hot path.
	switch len(word) {
	case 2:
		switch {
		case eqFold(word, "on"):
			return kwOn
		case eqFold(word, "by"):
			return kwBy
		case eqFold(word, "or"):
			return kwOr
		case eqFold(word, "as"):
			return kwAs
		}
	case 3:
		switch {
		case eqFold(word, "and"):
			return kwAnd
		case eqFold(word, "asc"):
			return kwAsc
		}
	case 4:
		switch {
		case eqFold(word, "from"):
			return kwFrom
		case eqFold(word, "join"):
			return kwJoin
		case eqFold(word, "left"):
			return kwLeft
		case eqFold(word, "full"):
			return kwFull
		case eqFold(word, "desc"):
			return kwDesc
		}
	case 5:
		switch {
		case eqFold(word, "where"):
			return kwWhere
		case eqFold(word, "group"):
			return kwGroup
		case eqFold(word, "order"):
			return kwOrder
		case eqFold(word, "using"):
			return kwUsing
		case eqFold(word, "right"):
			return kwRight
		case eqFold(word, "inner"):
			return kwInner
		case eqFold(word, "outer"):
			return kwOuter
		case eqFold(word, "cross"):
			return kwCross
		}
	case 6:
		switch {
		case eqFold(word, "select"):
			return kwSelect
		case eqFold(word, "having"):
			return kwHaving
		}
	case 7:
		if eqFold(word, "natural") {
			return kwNatural
		}
	}
	return kwNone
}

func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lower(a[i]) != b[i] {
			return false
		}
	}
	return true
}
