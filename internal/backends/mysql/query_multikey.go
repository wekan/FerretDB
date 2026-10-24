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

package mysql

import (
	"fmt"
	"strings"

	"github.com/FerretDB/FerretDB/internal/backends/mysql/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// multikeyWhere returns a condition, to be ANDed with the query's, that lets
// MySQL and MariaDB answer a membership test from a multikey side table
// (metadata/multikey.go), or "":
//
//   - a top-level {ARRAY: {$elemMatch: {FIELD: v | {$in: [...]}, ...}}} with a
//     complete side table for ARRAY.FIELD becomes
//     `_id IN (SELECT id FROM side WHERE v IN (...))`;
//   - a top-level $or becomes the OR of each branch's indexable condition - such
//     an $elemMatch, or an equality or $in on _id or an indexed field - when
//     every branch has one; a branch without one could match any row.
//
// Every condition is a SUPERSET of the matches; the Go filter still decides.
func multikeyWhere(dbName string, meta *metadata.Collection, ready func(string) bool, filter *types.Document) (string, []any) {
	if meta == nil || filter == nil || filter.Len() == 0 || meta.Capped() {
		return "", nil
	}

	arrays := map[string][]string{}

	for array, fields := range metadata.MultikeyArrays(meta.Indexes) {
		for _, field := range fields {
			if ready(metadata.MultikeyTableName(meta.TableName, array, field)) {
				arrays[array] = append(arrays[array], field)
			}
		}
	}

	if len(arrays) == 0 {
		return "", nil
	}

	m := &multikey{dbName: dbName, table: meta.TableName, arrays: arrays, indexed: indexedFields(meta.Indexes)}
	m.indexed["_id"] = struct{}{}

	var conds []string
	var args []any

	for _, key := range filter.Keys() {
		value := must.NotFail(filter.Get(key))

		var cond string
		var condArgs []any
		var ok bool

		if key == "$or" {
			cond, condArgs, ok = m.or(value)
		} else {
			cond, condArgs, ok = m.elemMatch(key, value)
		}

		if ok {
			conds = append(conds, cond)
			args = append(args, condArgs...)
		}
	}

	return strings.Join(conds, " AND "), args
}

// multikey is what multikeyWhere knows about one collection.
type multikey struct {
	dbName  string
	table   string
	arrays  map[string][]string // ARRAY -> FIELDs with a complete side table
	indexed map[string]struct{} // top-level fields with a generated column
}

// or is the OR of each branch's first indexable condition, or ok=false when a
// branch has none.
func (m *multikey) or(v any) (string, []any, bool) {
	branches, ok := v.(*types.Array)
	if !ok || branches.Len() == 0 {
		return "", nil, false
	}

	ors := make([]string, 0, branches.Len())

	var args []any

	for i := 0; i < branches.Len(); i++ {
		branch, ok := must.NotFail(branches.Get(i)).(*types.Document)
		if !ok || branch.Len() == 0 {
			return "", nil, false
		}

		found := false

		for _, key := range branch.Keys() {
			value := must.NotFail(branch.Get(key))

			cond, condArgs, ok := m.elemMatch(key, value)
			if !ok {
				single := must.NotFail(types.NewDocument(key, value))
				if cond, condArgs = indexedNarrowing(single, m.indexed); cond != "" {
					ok = true
				}
			}

			if ok {
				ors = append(ors, cond)
				args = append(args, condArgs...)
				found = true

				break
			}
		}

		if !found {
			return "", nil, false
		}
	}

	return "(" + strings.Join(ors, " OR ") + ")", args, true
}

// elemMatch is the side table condition of
// {array: {$elemMatch: {field: v | {$in: [...]}}}} for the first inner field
// with a complete side table whose values are all strings it can hold.
func (m *multikey) elemMatch(array string, v any) (string, []any, bool) {
	fields, ok := m.arrays[array]
	if !ok {
		return "", nil, false
	}

	doc, ok := v.(*types.Document)
	if !ok {
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
		known := false
		for _, f := range fields {
			known = known || f == field
		}

		if !known {
			continue
		}

		values, ok := narrowableValues(must.NotFail(elem.Get(field)))
		if !ok {
			continue
		}

		args := make([]any, 0, len(values))
		placeholders := make([]string, 0, len(values))

		for _, value := range values {
			text := jsonValue(value)
			// Only strings are in the table, and only those that fit it.
			if !strings.HasPrefix(text, `"`) || len(text) > 255 {
				ok = false
				break
			}

			args = append(args, text)
			placeholders = append(placeholders, "?")
		}

		if !ok {
			continue
		}

		return fmt.Sprintf(
			`%s IN (SELECT id FROM %s.%s WHERE v IN (%s))`,
			metadata.QuoteIdent(metadata.SafeColumnName("_id")),
			metadata.QuoteIdent(m.dbName),
			metadata.QuoteIdent(metadata.MultikeyTableName(m.table, array, field)),
			strings.Join(placeholders, ", "),
		), args, true
	}

	return "", nil, false
}
