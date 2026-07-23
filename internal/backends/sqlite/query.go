// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sqlite

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// prepareSelectClause returns SELECT clause for default column of provided table name.
//
// For capped collection with onlyRecordIDs, it returns select clause for recordID column.
//
// For capped collection, it returns select clause for recordID column and default column.
func prepareSelectClause(table, comment string, capped, onlyRecordIDs bool) string {
	if comment != "" {
		comment = strings.ReplaceAll(comment, "/*", "/ *")
		comment = strings.ReplaceAll(comment, "*/", "* /")
		comment = `/* ` + comment + ` */`
	}

	if capped && onlyRecordIDs {
		return fmt.Sprintf(`SELECT %s %s FROM %q`, comment, metadata.RecordIDColumn, table)
	}

	if capped {
		return fmt.Sprintf(`SELECT %s %s, %s FROM %q`, comment, metadata.RecordIDColumn, metadata.DefaultColumn, table)
	}

	return fmt.Sprintf(`SELECT %s %s FROM %q`, comment, metadata.DefaultColumn, table)
}

// pushdownSafeString reports whether Go's encoding/json (used by sjson when the
// document was stored) and SQLite's -> operator (which re-renders the stored
// JSON when we compare against it) produce byte-identical serializations of s,
// making a parameterized comparison on the -> expression exact. Go escapes
// '<', '>', '&', U+2028 and U+2029 as \uXXXX while SQLite renders them raw,
// and control-character escapes can differ, so values containing any of those
// are not pushed down (the in-Go filter still handles them correctly).
func pushdownSafeString(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029' {
			return false
		}
	}

	return true
}

// pushdownSafeLiteralSubstring reports whether s can be pushed to SQLite as a
// LIKE substring for a `$regex` filter (performance follow-up: "Filter by
// card title"). It must be a plain, ASCII, case-fold-safe LITERAL:
//   - non-empty and all ASCII: SQLite's LIKE folds case only for ASCII A-Z, so
//     for an ASCII literal `LIKE` is a correct SUPERSET of a case-insensitive Go
//     regex; a non-ASCII literal could make LIKE MISS a match the Go 'i' regex
//     keeps (\u00e9/\u00c9), which would drop a card \u2014 never allowed.
//   - no regex metacharacters (so the literal means itself, not a pattern), and
//   - no LIKE wildcards `%`/`_` (so we need no ESCAPE clause), and
//   - pushdownSafeString (so the -> JSON serialization is byte-identical).
// The Go filter still re-applies the real regex, so this only ever prunes rows
// SQLite can prove cannot match \u2014 never the authority on what does.
func pushdownSafeLiteralSubstring(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r > 0x7f {
			return false
		}

		switch r {
		case '.', '^', '$', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|', '\\', '%', '_':
			return false
		}
	}

	return pushdownSafeString(s)
}

// prepareWhereClause builds a WHERE clause selecting a SUPERSET of the documents
// matching the given filter, from the filter's top-level string/ObjectID
// equality conditions. Exact filtering still happens in Go afterwards
// (common.FilterIterator re-applies the whole filter), so a superset is always
// correct — but evaluating the cheap conditions inside SQLite avoids decoding
// every document's sjson in Go, which was pinning the CPU on busy collections
// (every {field: value} equality query decoded the whole collection).
//
// The expressions are built EXACTLY like Registry.indexesCreate builds its
// expression indexes (_ferretdb_sjson->"field"), so SQLite can satisfy them from
// an existing index: {_id: X} uses the unique _id index, and Mongo-level indexes
// that the application declares accelerate their fields too.
//
// Because Mongo equality {f: "x"} also matches documents where f is an ARRAY
// containing "x", each non-_id condition keeps array values with an
// index-friendly range arm: array JSON renders as "[...", so expr >= '[' AND
// expr < '\' selects exactly the arrays, and the Go filter decides which of
// them actually match. _id can never be an array, so it uses plain equality.
func prepareWhereClause(filter *types.Document) (string, []any) {
	if filter == nil || filter.Len() == 0 {
		return "", nil
	}

	var conds []string
	var args []any

	for _, k := range filter.Keys() {
		// $-operator top-level keys (e.g. $or) stay in the Go filter.
		if k == "" || strings.HasPrefix(k, "$") {
			continue
		}

		v := must.NotFail(filter.Get(k))

		// The same expression Registry.indexesCreate indexes. A DOTTED path becomes
		// a nested -> chain (`col->"a"->"b"`), so SQLite can pair the WHERE with the
		// nested expression index — e.g. a client's `{'meta.cardId': X}` attachment
		// lookup, which otherwise dropped the WHERE and full-scanned the collection.
		expr := jsonPathExpr(k)

		var cond string
		var condArgs []any
		var ok bool
		if strings.ContainsRune(k, '.') {
			cond, condArgs, ok = pushdownDottedFieldCondition(expr, v)
		} else {
			cond, condArgs, ok = pushdownFieldCondition(expr, k, v)
		}

		if ok {
			conds = append(conds, cond)
			args = append(args, condArgs...)
		}
	}

	if len(conds) == 0 {
		return "", nil
	}

	return ` WHERE ` + strings.Join(conds, ` AND `), args
}

// pushdownFieldCondition returns a WHERE condition (and its args) selecting a
// SUPERSET of the documents matching {key: v}, or ok=false when v cannot be
// pushed down (the Go filter then stays the sole authority for that field).
//
// Handled: scalar string/ObjectID equality, {$in: [...safe...]} (an $in list
// filter) and {$regex: literal} (a substring filter). Everything else —
// ranges ($gt/$lte/…, unsafe on JSON-text ordering), $ne, non-ASCII or
// non-literal regex, unsafe values — stays in Go.
func pushdownFieldCondition(expr, key string, v any) (string, []any, bool) {
	switch val := v.(type) {
	case string:
		if !pushdownSafeString(val) {
			return "", nil, false
		}

		return equalityCondition(expr, key), []any{marshalPushdownValue(v)}, true

	case types.ObjectID:
		// hex-encoded by sjson; always byte-identical in both serializations
		return equalityCondition(expr, key), []any{marshalPushdownValue(v)}, true

	case types.Regex:
		return regexCondition(expr, val.Pattern, val.Options)

	case *types.Document:
		return operatorCondition(expr, key, val)

	default:
		return "", nil, false
	}
}

// jsonPathExpr builds the DefaultColumn -> accessor for a (possibly dotted) field
// key, EXACTLY like Registry.indexesCreate builds its expression index, so SQLite
// can pair a WHERE on the key with that index: "a" -> `col->"a"`, "a.b" ->
// `col->"a"->"b"`.
func jsonPathExpr(key string) string {
	segments := strings.Split(key, ".")
	for i, s := range segments {
		segments[i] = quoteJSONLabel(s)
	}

	return fmt.Sprintf(`%s->%s`, metadata.DefaultColumn, strings.Join(segments, "->"))
}

// pushdownDottedFieldCondition pushes down a DOTTED-path field (e.g. "meta.cardId")
// for scalar string/ObjectID equality and {$in: [...]} only — the conditions whose
// SQL references ONLY `expr` (the nested -> chain that matches the expression index
// Registry.indexesCreate builds for a dotted key). Range ($gt/…) and $regex on a
// dotted path build a scalar ->> on the raw key, which is not a valid nested JSON
// path, so they stay in the Go filter. `expr` is the nested chain; the key is passed
// as "" so equality/inCondition use their non-_id (array-containment) form — a dotted
// path is never _id. Still a SUPERSET, so the Go filter stays authoritative.
func pushdownDottedFieldCondition(expr string, v any) (string, []any, bool) {
	switch val := v.(type) {
	case string:
		if !pushdownSafeString(val) {
			return "", nil, false
		}

		return equalityCondition(expr, ""), []any{marshalPushdownValue(v)}, true

	case types.ObjectID:
		return equalityCondition(expr, ""), []any{marshalPushdownValue(v)}, true

	case *types.Document:
		if inAny, err := val.Get("$in"); err == nil {
			if arr, ok := inAny.(*types.Array); ok {
				return inCondition(expr, "", arr)
			}
		}

		return "", nil, false

	default:
		return "", nil, false
	}
}

// marshalPushdownValue renders a value the same way it is stored, so a compared
// -> expression matches byte-for-byte.
func marshalPushdownValue(v any) any {
	return string(must.NotFail(sjson.MarshalSingleValue(v)))
}

// equalityCondition matches {field: scalar}. Mongo equality on a non-_id field
// also matches an ARRAY containing the value, so keep an index-friendly arm for
// array values (array JSON starts with '['); _id is never an array.
func equalityCondition(expr, key string) string {
	if key == "_id" {
		return fmt.Sprintf(`%s = ?`, expr)
	}

	return fmt.Sprintf(`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`, expr)
}

// regexCondition pushes a plain literal {$regex} as a LIKE substring. LIKE on the
// stored JSON text matches a substring anywhere (including inside a string array,
// which the Go filter re-checks), and LIKE's ASCII case-insensitivity is a
// superset of a case-insensitive regex for an ASCII literal. `x` options
// (extended: whitespace changes meaning) are not pushed.
func regexCondition(expr, pattern, options string) (string, []any, bool) {
	if strings.ContainsRune(options, 'x') || !pushdownSafeLiteralSubstring(pattern) {
		return "", nil, false
	}

	return fmt.Sprintf(`%s LIKE ?`, expr), []any{"%" + pattern + "%"}, true
}

// operatorCondition pushes {field: {$in: [...]}} / {field: {$regex: ...}} from an
// operator expression. All operators in a field expression are ANDed, so pushing
// a SUPERSET of any ONE of them is a valid superset of the whole expression —
// coexisting operators ($ne, $nin, $options, …) do not make this unsafe.
func operatorCondition(expr, key string, doc *types.Document) (string, []any, bool) {
	if inAny, err := doc.Get("$in"); err == nil {
		if arr, ok := inAny.(*types.Array); ok {
			if cond, condArgs, ok := inCondition(expr, key, arr); ok {
				return cond, condArgs, true
			}
		}
	}

	if reAny, err := doc.Get("$regex"); err == nil {
		var pattern, options string

		switch p := reAny.(type) {
		case string:
			pattern = p
		case types.Regex:
			pattern, options = p.Pattern, p.Options
		}

		if optAny, err := doc.Get("$options"); err == nil {
			if o, ok := optAny.(string); ok {
				options = o
			}
		}

		if cond, condArgs, ok := regexCondition(expr, pattern, options); ok {
			return cond, condArgs, true
		}
	}

	// numeric/date range operators, extracted with ->> (see rangeConditions).
	scalarExpr := fmt.Sprintf(`%s->>%s`, metadata.DefaultColumn, quoteJSONLabel(key))

	return rangeConditions(scalarExpr, doc)
}

// rangeConditions pushes numeric/date range operators ($gt/$gte/$lt/$lte) using
// the ->> (SQL value) accessor, so SQLite compares the extracted number
// NUMERICALLY. The -> JSON-text accessor used for equality would compare
// lexically ("10" < "9"), which is wrong for numbers and dates — which is
// exactly why range was NOT pushed before. sjson stores int32/int64, doubles and
// dates all as JSON numbers (a date as its Unix-millis), so ->> yields a
// comparable value; a null/missing/string field yields NULL or a non-numeric
// that the comparison excludes — matching Mongo's type-bracketed $lt/$gt, and the
// Go filter stays authoritative regardless. Only NUMBER/DATE bounds are pushed;
// string ranges (collation/serialization subtleties) stay in Go.
func rangeConditions(scalarExpr string, doc *types.Document) (string, []any, bool) {
	ops := [...]struct{ key, sqlOp string }{
		{"$gt", ">"},
		{"$gte", ">="},
		{"$lt", "<"},
		{"$lte", "<="},
	}

	var parts []string

	var args []any

	for _, op := range ops {
		val, err := doc.Get(op.key)
		if err != nil {
			continue
		}

		arg, ok := numericBound(val)
		if !ok {
			continue
		}

		parts = append(parts, fmt.Sprintf(`%s %s ?`, scalarExpr, op.sqlOp))
		args = append(args, arg)
	}

	switch len(parts) {
	case 0:
		return "", nil, false
	case 1:
		return parts[0], args, true
	default:
		// e.g. a week filter {$gte: A, $lte: B}: both arms ANDed.
		return "(" + strings.Join(parts, " AND ") + ")", args, true
	}
}

// numericBound returns the SQL argument for a range bound that can be compared
// numerically by ->>, or ok=false for a non-number/date bound (left to Go). A
// date is compared as its Unix-millis, matching how sjson stores it; a BSON
// Timestamp is compared as its uint64, also matching how sjson stores it (as a
// JSON number). Pushing a Timestamp $gt/$gte down matters for a client that tails
// a capped collection with a `{ts: {$gt: <last>}}` cursor (e.g. a Meteor 3 driver
// tailing local.oplog.rs): without it, every awaitData poll had to sjson-decode
// and range-filter the whole collection in Go — a residual CPU load on an idle
// tail. The Go filter still re-applies the exact filter, so this only ever prunes
// rows the bound proves cannot match.
func numericBound(v any) (any, bool) {
	switch n := v.(type) {
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return n, true
	case time.Time:
		return n.UnixMilli(), true
	case types.Timestamp:
		// sjson stores a Timestamp as its uint64. Decline to push a value that
		// would not fit a signed 64-bit integer (only reachable in the far future),
		// so the SQL argument stays an exact integer comparison; the Go filter
		// remains authoritative, so declining is safe (never a subset).
		if uint64(n) > math.MaxInt64 {
			return nil, false
		}

		return int64(n), true
	default:
		return nil, false
	}
}

// inCondition pushes {field: {$in: [...]}} as a SUPERSET: it pushes the
// pushdown-safe string / ObjectID elements as an index-usable IN, and — because a
// `null` element of $in also matches a field that is null OR missing, both of which
// render as SQL NULL under ->  — adds an `expr IS NULL` arm for a null element. Both
// arms reference the exact indexed expression, so SQLite serves the whole thing as
// an OR-union of index seeks. This is why {boardId: {$in: [id, null]}} — the shape a
// board's card queries use when no subtasks-default board is set — still uses the
// index instead of full-scanning the collection (the previous version bailed out
// entirely on the null, dropping the WHERE and pinning CPU on a poll-and-diff client,
// e.g. a Meteor 3 driver: boards then loaded lists but never cards).
//
// It still bails (leaving the whole condition to the in-Go filter) when an element is
// something with no safe superset arm — a number, bool, unsafe string, or nested
// doc/array — since pushing only the other elements would make IN a SUBSET.
func inCondition(expr, key string, arr *types.Array) (string, []any, bool) {
	if arr.Len() == 0 {
		return "", nil, false
	}

	placeholders := make([]string, 0, arr.Len())
	condArgs := make([]any, 0, arr.Len())
	hasNull := false

	for i := 0; i < arr.Len(); i++ {
		e := must.NotFail(arr.Get(i))

		switch ev := e.(type) {
		case string:
			if !pushdownSafeString(ev) {
				return "", nil, false
			}
			placeholders = append(placeholders, "?")
			condArgs = append(condArgs, marshalPushdownValue(e))
		case types.ObjectID:
			placeholders = append(placeholders, "?")
			condArgs = append(condArgs, marshalPushdownValue(e))
		case types.NullType:
			hasNull = true
		default:
			return "", nil, false
		}
	}

	arms := make([]string, 0, 3)
	if len(placeholders) > 0 {
		arms = append(arms, fmt.Sprintf(`%s IN (%s)`, expr, strings.Join(placeholders, ", ")))
	}
	if hasNull {
		arms = append(arms, fmt.Sprintf(`%s IS NULL`, expr))
	}
	if len(arms) == 0 {
		return "", nil, false
	}

	// _id is never an array, so it needs no array-containment arm.
	if key != "_id" {
		arms = append(arms, fmt.Sprintf(`(%[1]s >= '[' AND %[1]s < '\')`, expr))
	}

	if len(arms) == 1 {
		return arms[0], condArgs, true
	}

	return "(" + strings.Join(arms, " OR ") + ")", condArgs, true
}

// quoteJSONLabel quotes a field name for the -> operator the same way
// Registry.indexesCreate does (%q), so the WHERE expression text matches the
// expression-index text and SQLite's index matcher can pair them up.
func quoteJSONLabel(field string) string {
	return fmt.Sprintf("%q", field)
}

// prepareOrderByClause returns ORDER BY clause for given sort document.
//
// The provided sort document should be already validated.
// Provided document should only contain a single value.
func prepareOrderByClause(sort *types.Document) string {
	if sort.Len() != 1 {
		return ""
	}

	v := must.NotFail(sort.Get("$natural"))
	var order string

	switch v.(int64) {
	case 1:
		// Ascending order
	case -1:
		order = " DESC"
	default:
		panic("not reachable")
	}

	return fmt.Sprintf(" ORDER BY %s%s", metadata.RecordIDColumn, order)
}
