# Context-aware autocomplete for the SQL editor

## Problem

The editor in `internal/editor/editor.go` opens the column suggestion list on every word-rune keystroke, anywhere in the query. This is wrong in several places that matter:

- **After the table reference.** `SELECT * FROM t` → typing any letter pops the column list. Columns aren't valid grammar there.
- **In the `SELECT` list without a separator.** `SELECT id ` → typing `f` (intending `FROM`) pops the column list and misleads.
- **Inside string literals and comments.** `SELECT 'r` opens completion against `r…` columns even though the caret is inside a quoted literal.
- **Before any clause keyword.** Typing at the start of an empty buffer suggests columns even though no `SELECT` has been written yet.

The current `inSelectList` heuristic only governs the already-used filter, not whether to fire at all.

## Goal

Auto-trigger the column list only in positions where a column identifier is grammatically valid. Manual completion via Tab stays unchanged — explicit user action, full dictionary, works anywhere.

## Non-goals

- Full SQL parsing. We don't need an AST; we need a clause-context detector.
- Smart re-ordering of suggestions, alias resolution, or table-qualified completion (`t.col`). Out of scope.
- Auto-inserting commas after accepted columns. Out of scope.

## Architecture

A new package `internal/sqlctx` exposes a single function:

```go
package sqlctx

type Context int

const (
    Unknown       Context = iota
    ColumnList
    Predicate
    TablePosition
    StringLiteral
    Comment
)

// At reports the context at byte offset `caret` in `text`.
func At(text string, caret int) Context
```

`internal/editor/editor.go` calls `sqlctx.At` from the auto-trigger path and from `removeAlreadyUsed`. The local helpers `inSelectList`, `firstWordIndex`, `lastWordIndex`, and `isWordBoundary`/`isWordByte` move to `sqlctx` (or get deleted if no longer used). Tab handling does not change.

### Editor integration

In `HandleKey` (the word-rune branch):

```go
case isWordRune(r):
    ctx := sqlctx.At(m.flatText(), m.caretByteOffset())
    if ctx == sqlctx.ColumnList || ctx == sqlctx.Predicate {
        m.openColumnSuggestions()
    }
```

In `removeAlreadyUsed`:

```go
if sqlctx.At(text, caret) != sqlctx.ColumnList {
    return suggestions
}
// ...existing dedupe logic against the joined query text
```

`flatText()` and `caretByteOffset()` are tiny helpers added to `Model` that join `lines` and convert `(row, col)` to a byte offset. The editor already does this inline in `inSelectList`; we lift it into helpers.

## The token walker

`sqlctx.At` is a single left-to-right scan of `text[:caret]`, byte-by-byte (ASCII-fast-path; non-ASCII is opaque inside identifiers, which is fine — we only branch on ASCII keywords and quote/comment markers). It tracks:

1. **Skip-state.** When entering `'…'`, `"…"`, `--…\n`, or `/*…*/`, every byte is opaque until the matching close. Single-quote escape is the SQL-standard `''` doubling. If `caret` lands inside a skip-state, return `StringLiteral` or `Comment` immediately (don't update anchors from inside).
2. **Anchor.** The most recent clause keyword seen at top-level (paren depth 0 for most, special-cased for `USING`). Enum: `aSelect`, `aFrom`, `aJoin`, `aWhere`, `aGroupBy`, `aOrderBy`, `aHaving`, `aOn`, `aUsing`. Multi-word keywords (`GROUP BY`, `ORDER BY`, `LEFT JOIN`, `RIGHT JOIN`, `INNER JOIN`, `OUTER JOIN`, `FULL JOIN`, `CROSS JOIN`, `LEFT OUTER JOIN`, etc.) are normalized: any token sequence ending in `JOIN` sets anchor `aJoin`; `GROUP` followed by `BY` sets `aGroupBy`; `ORDER` followed by `BY` sets `aOrderBy`.
3. **Paren stack.** A small stack of `(anchorBefore, depthAtEntry)` entries pushed when we see `USING (` and popped when the matching `)` closes. Inside `USING (...)` the anchor is `aUsing`. (Generic parens — function calls, expression grouping — are tracked for depth only; they don't change the anchor.)
4. **`awaitsComma` flag.** Inside `aSelect` / `aGroupBy` / `aOrderBy` / `aUsing`, set when a *value-token* is completed: identifier terminated by a non-word char, closing quote of a string literal, end of a numeric literal, a closing `)`, or a bare `*` at column-list start. Cleared by `,`. When we arrive at `caret` with this flag set, downgrade the anchor's normal `ColumnList` to `Unknown`. The flag is *not* set while we're still mid-identifier, so `SELECT id^` (caret right after `id`, no terminator yet) stays `ColumnList` — that case is the user typing the first column. `SELECT id f^` (terminator consumed, then a fresh word) is `Unknown`.

`AND` and `OR` are recognized as keywords but do *not* change the anchor — they continue whatever predicate clause is active.

`,` does not change the anchor; commas inherit context. `,` in `aFrom` stays `TablePosition`; `,` in `aSelect` clears `awaitsComma` and stays `ColumnList`.

### Anchor → Context

| Anchor | Context |
|---|---|
| `aSelect`, `aGroupBy`, `aOrderBy`, `aUsing` | `ColumnList` |
| `aWhere`, `aHaving`, `aOn` | `Predicate` |
| `aFrom`, `aJoin` | `TablePosition` |
| (none) | `Unknown` |

If `awaitsComma` is true at the caret, the anchor's normal `ColumnList` downgrades to `Unknown` regardless of whether the caret sits inside a word — covers both `SELECT id ^` (cursor at whitespace) and `SELECT id f^` (cursor mid-second-word).

### Auto-trigger contexts

| Position | Auto-trigger? |
|---|---|
| `SELECT^` / `SELECT id, ^` / `SELECT id, c^` | ✅ |
| `WHERE ^`, `… AND ^`, `… OR ^`, `HAVING ^` | ✅ |
| `GROUP BY ^` / `ORDER BY ^` (and `,` within) | ✅ |
| `ON ^` (any JOIN flavor) | ✅ |
| `USING (^)` / `USING (a, ^)` | ✅ |
| `FROM ^`, `FROM t^`, `JOIN ^`, `JOIN x^` | ❌ |
| `FROM t1, ^` (table list comma) | ❌ |
| Inside `'…'`, `"…"`, `--…`, `/*…*/` | ❌ |
| Before any clause keyword | ❌ |
| `SELECT id ^` (no comma yet, mid-word) | ❌ |

### Edge cases handled

- Case-insensitive keyword match (`select`/`SELECT`/`Select` all work).
- Word-bounded keyword match: `SELECTED` is not `SELECT`; `WHEREVER` is not `WHERE`.
- Escaped quotes: `'it''s ok'` doesn't terminate at the first `''`.
- Unterminated string at EOF: the rest of the buffer counts as `StringLiteral` (which means the auto-trigger correctly stays off until a closing quote is typed).
- Nested parens around `USING`: only `USING (`'s direct paren matters; inner function calls inside `USING (foo, bar(x))` keep the `aUsing` anchor.
- DuckDB quoted identifiers (`"col with space"`) are treated like strings for skip purposes — they're identifiers, but completion shouldn't fire mid-quote.

## Testing

### `internal/sqlctx/sqlctx_test.go`

Table-driven. Each case: input text containing a literal `^` marker, expected `Context`. The test harness strips the `^`, computes its byte offset, and calls `At`. Cases (non-exhaustive):

- `^` → `Unknown`
- `SELECT ^` → `ColumnList`
- `SELECT id, ^` → `ColumnList`
- `SELECT id, c^` → `ColumnList`
- `SELECT id ^` → `Unknown` (no comma)
- `SELECT id f^` → `Unknown` (mid-word, no comma)
- `SELECT * f^` → `Unknown` (`*` already filled the column list)
- `SELECT * FROM ^` → `TablePosition`
- `SELECT * FROM t^` → `TablePosition`
- `SELECT * FROM t ^` → `TablePosition`
- `SELECT * FROM t WHERE ^` → `Predicate`
- `SELECT * FROM t WHERE id = 1 AND ^` → `Predicate`
- `SELECT * FROM t WHERE id = 1 OR ^` → `Predicate`
- `SELECT * FROM t LEFT JOIN x ON ^` → `Predicate`
- `SELECT * FROM t INNER JOIN x ON ^` → `Predicate`
- `SELECT * FROM t JOIN x USING (^)` → `ColumnList`
- `SELECT * FROM t JOIN x USING (a, ^)` → `ColumnList`
- `SELECT * FROM t1, ^` → `TablePosition`
- `SELECT * FROM t GROUP BY ^` → `ColumnList`
- `SELECT * FROM t ORDER BY ^` → `ColumnList`
- `SELECT * FROM t GROUP BY id HAVING ^` → `Predicate`
- `SELECT 'oops ^` → `StringLiteral`
- `SELECT 'it''s ok' , ^` → `ColumnList` (escaped quote doesn't end the literal)
- `SELECT * -- comment ^` → `Comment`
- `SELECT */* block ^ */ id` → `Comment`
- `SELECT * FROM t WHERE id = 'x' AND ^` → `Predicate` (string close didn't break anchor)
- `select * from t where ^` → `Predicate` (case insensitive)
- `SELECTED ^` (no real `SELECT` keyword) → `Unknown`

### `internal/editor/editor_test.go`

Three new tests:

- `TestNoAutoTriggerAfterTable`: `SELECT * FROM t` with caret at end, `HandleKey("w")` → `len(suggestions) == 0`.
- `TestNoAutoTriggerInSelectWithoutComma`: `SELECT id ` with caret at end, `HandleKey("f")` → `len(suggestions) == 0`.
- `TestAutoTriggerInJoinOn`: `SELECT * FROM t JOIN x ON ` with caret at end, `HandleKey("c")` against columns `[customer_id, customer_name]` → `len(suggestions) == 2`.

Existing tests audited:

- `TestAutoTriggerInsideSelectFrom`, `TestAutoTriggerFiresInWhereClause`, `TestTabAcceptsWhenOnlyOneSuggestionOpen`, `TestSuggestionsHideAlreadyUsedColumns`, `TestAlreadyUsedFilterIsCaseInsensitive`, `TestAlreadyUsedFilterOffInWhereClause`, `TestAlreadyUsedFilterActiveInSelectList`, `TestTypingNarrowsOpenSuggestionList`, `TestBackspaceReopensListAfterNoMatchTyping`, `TestNonWordCharClosesSuggestions` — all covered by the new contexts; should pass with no test code changes.
- `TestInsertTextPreservesLiteralContent` — paste path doesn't go through auto-trigger; unaffected.
- Tab tests (`TestTabCompletesUniquePrefix`, `TestTabOnEmptyCaretShowsAllOptions`, `TestTabBetweenWordsShowsAllOptions`, `TestTabAcceptsSelectionWhenListOpen`) — Tab is context-free; unaffected.

## Out of scope, explicit

- **Keyword completion in non-column positions.** Typing `w` after `FROM t ` does not pop a list suggesting `WHERE`. Tab still does (full dictionary), and that's enough.
- **Schema-qualified completion** (`t.column`). The app has a single table `t`, so the prefix is noise.
- **Multiple FROM aliases.** No alias tracking. JOIN target tables aren't queried for their columns.
- **Auto-comma insertion** after accepted SELECT columns. The user types `,` themselves.

## Migration / compatibility

Single binary, single user codebase. No flag, no shim. Land it as one PR. The behavior change is strict — auto-trigger fires in fewer places than before — and the user gains: no more spurious popups in table position or after a stray `t`.
