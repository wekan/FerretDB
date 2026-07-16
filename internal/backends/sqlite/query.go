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
	"strings"

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

// prepareWhereClause builds a WHERE clause selecting a SUPERSET of the documents
// matching the given filter, from the filter's top-level string/ObjectID
// equality conditions. Exact filtering still happens in Go afterwards
// (common.FilterIterator re-applies the whole filter), so a superset is always
// correct — but evaluating the cheap conditions inside SQLite avoids decoding
// every document's sjson in Go, which was pinning the CPU on busy WeKan boards
// (WeKan #6467, #6468: every {boardId: X} query decoded the whole collection).
//
// The expressions are built EXACTLY like Registry.indexesCreate builds its
// expression indexes (_ferretdb_sjson->"field"), so SQLite can satisfy them from
// an existing index: {_id: X} uses the unique _id index, and Mongo-level indexes
// that WeKan declares (boardId, listId, ...) accelerate their fields too.
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
		// dotted paths and operator expressions stay in the Go filter
		if k == "" || strings.HasPrefix(k, "$") || strings.ContainsRune(k, '.') {
			continue
		}

		v := must.NotFail(filter.Get(k))

		switch val := v.(type) {
		case string:
			if !pushdownSafeString(val) {
				continue
			}
		case types.ObjectID:
			// hex-encoded by sjson; always byte-identical in both serializations
		default:
			continue
		}

		// the same expression Registry.indexesCreate indexes
		expr := fmt.Sprintf(`%s->%s`, metadata.DefaultColumn, quoteJSONLabel(k))

		if k == "_id" {
			conds = append(conds, fmt.Sprintf(`%s = ?`, expr))
		} else {
			conds = append(conds, fmt.Sprintf(`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`, expr))
		}

		args = append(args, string(must.NotFail(sjson.MarshalSingleValue(v))))
	}

	if len(conds) == 0 {
		return "", nil
	}

	return ` WHERE ` + strings.Join(conds, ` AND `), args
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
