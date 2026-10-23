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

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// multikeyCandidates narrows a query to the documents a multikey side table
// (metadata/multikey.go) names, when every document the filter matches must be
// one of them:
//
//   - a top-level {ARRAY: {$elemMatch: {FIELD: value, ...}}}, with an index on
//     ARRAY.FIELD - a matching document has an element whose FIELD is value, so
//     its _id is in the side table under that value;
//   - a top-level $or whose EVERY branch is such an $elemMatch or names its
//     _id ({_id: X} or {_id: {$in: [...]}}) - a matching document matches a
//     branch, so it is among that branch's _ids.
//
// It returns the condition, its arguments and the _id index to read them by
// (the side table yields _ids, and the unique _id index turns each into its
// row), or ok=false when the filter has no such shape. The candidates are a
// SUPERSET of the matches; the Go filter still decides.
func multikeyCandidates(meta *metadata.Collection, filter *types.Document) (string, []any, string, bool) {
	if filter == nil || filter.Len() == 0 || meta.Capped() {
		return "", nil, "", false
	}

	paths := map[[2]string]string{}
	for _, index := range meta.Settings.Indexes {
		if index.Hidden {
			continue
		}

		if array, field, ok := metadata.MultikeyPath(index); ok {
			paths[[2]string{array, field}] = metadata.MultikeyTableName(meta.TableName, array, field)
		}
	}

	if len(paths) == 0 {
		return "", nil, "", false
	}

	idIndex := meta.TableName + "_" + backends.DefaultIndexName
	idExpr := jsonPathExpr("_id")

	for _, key := range filter.Keys() {
		value := must.NotFail(filter.Get(key))

		if key == "$or" {
			if sub, args, ok := orCandidates(paths, value); ok {
				return idExpr + ` IN (` + sub + `)`, args, idIndex, true
			}

			continue
		}

		if sub, args, ok := elemMatchCandidates(paths, key, value); ok {
			return idExpr + ` IN (` + sub + `)`, args, idIndex, true
		}
	}

	return "", nil, "", false
}

// orCandidates is the union of every branch's candidates, or ok=false when a
// branch has none to offer (it could match a document no other branch names).
func orCandidates(paths map[[2]string]string, v any) (string, []any, bool) {
	branches, ok := v.(*types.Array)
	if !ok || branches.Len() == 0 {
		return "", nil, false
	}

	var selects []string
	var args []any

	for i := 0; i < branches.Len(); i++ {
		branch, ok := must.NotFail(branches.Get(i)).(*types.Document)
		if !ok || branch.Len() == 0 {
			return "", nil, false
		}

		found := false

		// The branch's conditions are ANDed: the candidates of any one of them
		// hold all of its matches.
		for _, key := range branch.Keys() {
			value := must.NotFail(branch.Get(key))

			var sub string
			var subArgs []any

			if key == "_id" {
				sub, subArgs, found = idCandidates(value)
			} else {
				sub, subArgs, found = elemMatchCandidates(paths, key, value)
			}

			if found {
				if sub != "" {
					selects = append(selects, sub)
					args = append(args, subArgs...)
				}

				break
			}
		}

		if !found {
			return "", nil, false
		}
	}

	if len(selects) == 0 {
		// Every branch names no _id at all: nothing can match.
		return `SELECT NULL WHERE 0`, nil, true
	}

	return strings.Join(selects, ` UNION `), args, true
}

// idCandidates returns the _ids {_id: X} or {_id: {$in: [...]}} names - sub is
// empty for an empty $in, which names none - or ok=false for any other _id
// condition, or a value whose stored text may differ from its pushdown text.
func idCandidates(v any) (string, []any, bool) {
	values := []any{v}

	if doc, ok := v.(*types.Document); ok {
		if doc.Len() != 1 || doc.Keys()[0] != "$in" {
			return "", nil, false
		}

		arr, ok := must.NotFail(doc.Get("$in")).(*types.Array)
		if !ok {
			return "", nil, false
		}

		values = values[:0]
		for i := 0; i < arr.Len(); i++ {
			values = append(values, must.NotFail(arr.Get(i)))
		}
	}

	if len(values) == 0 {
		return "", nil, true
	}

	rows := make([]string, len(values))
	args := make([]any, len(values))

	for i, value := range values {
		if !candidateValue(value) {
			return "", nil, false
		}

		rows[i] = `(?)`
		args[i] = marshalPushdownValue(value)
	}

	return `VALUES ` + strings.Join(rows, `, `), args, true
}

// elemMatchCandidates returns the _ids a side table holds for
// {array: {$elemMatch: {field: value | {$in: [...]}}}}, using the first inner
// field that has a side table and a value it can be looked up by.
func elemMatchCandidates(paths map[[2]string]string, array string, v any) (string, []any, bool) {
	doc, ok := v.(*types.Document)
	if !ok || strings.HasPrefix(array, "$") || strings.ContainsRune(array, '.') {
		return "", nil, false
	}

	elemAny, err := doc.Get("$elemMatch")
	if err != nil {
		return "", nil, false
	}

	elem, ok := elemAny.(*types.Document)
	if !ok {
		return "", nil, false
	}

	for _, field := range elem.Keys() {
		table, ok := paths[[2]string{array, field}]
		if !ok {
			continue
		}

		values := []any{must.NotFail(elem.Get(field))}

		if in, ok := values[0].(*types.Document); ok {
			if in.Len() != 1 || in.Keys()[0] != "$in" {
				continue
			}

			arr, ok := must.NotFail(in.Get("$in")).(*types.Array)
			if !ok || arr.Len() == 0 {
				continue
			}

			values = values[:0]
			for i := 0; i < arr.Len(); i++ {
				values = append(values, must.NotFail(arr.Get(i)))
			}
		}

		args := make([]any, 0, len(values))
		usable := true

		for _, value := range values {
			if !candidateValue(value) {
				usable = false
				break
			}

			args = append(args, marshalPushdownValue(value))
		}

		if !usable {
			continue
		}

		placeholders := strings.TrimSuffix(strings.Repeat(`?, `, len(args)), `, `)

		return fmt.Sprintf(`SELECT id FROM %q WHERE v IN (%s)`, table, placeholders), args, true
	}

	return "", nil, false
}

// candidateValue reports a value whose stored JSON text is exactly its
// pushdown text: the same rule the equality pushdown follows.
func candidateValue(v any) bool {
	switch v := v.(type) {
	case string:
		return pushdownSafeString(v)
	case types.ObjectID, bool:
		return true
	default:
		return false
	}
}
