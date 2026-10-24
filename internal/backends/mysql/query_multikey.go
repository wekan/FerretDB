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

	return m.conds(filter)
}

// conds is multikeyWhere for one conjunction: the filter itself, or a branch of
// a top-level $and, which must hold just the same.
func (m *multikey) conds(filter *types.Document) (string, []any) {
	var conds []string
	var args []any

	for _, key := range filter.Keys() {
		value := must.NotFail(filter.Get(key))

		var cond string
		var condArgs []any
		var ok bool

		if key == "$and" {
			if branches, isArray := value.(*types.Array); isArray {
				for i := 0; i < branches.Len(); i++ {
					if branch, isDoc := must.NotFail(branches.Get(i)).(*types.Document); isDoc {
						if c, a := m.conds(branch); c != "" {
							conds = append(conds, c)
							args = append(args, a...)
						}
					}
				}
			}

			continue
		}

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
//
// When every branch is a set of _ids - a side table lookup, or _id itself -
// the sets are one UNION ALL, in a derived table: `_id IN (SELECT id FROM
// (... UNION ALL ...) AS mkids)`. MySQL makes a semijoin of an IN subquery only
// when it is not under an OR, and All Boards with a starred board -
// `_id IN (SELECT id FROM side ...) OR _id IN (...)` - ran the subquery for each
// of 92,000 boards: 800 ms instead of 4.
func (m *multikey) or(v any) (string, []any, bool) {
	branches, ok := v.(*types.Array)
	if !ok || branches.Len() == 0 {
		return "", nil, false
	}

	type arm struct {
		ids  string // a SELECT of _id texts, or ""
		cond string // otherwise, a condition
		args []any
	}

	arms := make([]arm, 0, branches.Len())
	allIDs := true

	for i := 0; i < branches.Len(); i++ {
		branch, ok := must.NotFail(branches.Get(i)).(*types.Document)
		if !ok || branch.Len() == 0 {
			return "", nil, false
		}

		var a arm

		for _, key := range branch.Keys() {
			if a.ids, a.args, ok = m.idSet(key, must.NotFail(branch.Get(key))); ok {
				break
			}
		}

		if a.ids == "" {
			for _, key := range branch.Keys() {
				single := must.NotFail(types.NewDocument(key, must.NotFail(branch.Get(key))))
				if a.cond, a.args = indexedNarrowing(single, m.indexed); a.cond != "" {
					break
				}
			}

			if a.cond == "" {
				return "", nil, false
			}

			allIDs = false
		}

		arms = append(arms, a)
	}

	var args []any

	if allIDs {
		selects := make([]string, len(arms))
		for i, a := range arms {
			selects[i] = a.ids
			args = append(args, a.args...)
		}

		return fmt.Sprintf(`%s IN (SELECT id FROM (%s) AS mkids)`, idColumn(), strings.Join(selects, " UNION ALL ")), args, true
	}

	ors := make([]string, len(arms))
	for i, a := range arms {
		ors[i] = a.cond
		if a.ids != "" {
			ors[i] = fmt.Sprintf(`%s IN (%s)`, idColumn(), a.ids)
		}
		args = append(args, a.args...)
	}

	return "(" + strings.Join(ors, " OR ") + ")", args, true
}

// idColumn is the _id generated column.
func idColumn() string {
	return metadata.QuoteIdent(metadata.SafeColumnName("_id"))
}

// idSet returns a SELECT of the _id texts {key: v} can match, when it is
// one: an $elemMatch a side table answers, or an equality or $in on _id.
func (m *multikey) idSet(key string, v any) (string, []any, bool) {
	if key == "_id" {
		values, ok := narrowableValues(v)
		if !ok {
			return "", nil, false
		}

		selects := make([]string, len(values))
		args := make([]any, len(values))

		for i, value := range values {
			selects[i] = "SELECT ?"
			args[i] = jsonValue(value)
		}

		// The first SELECT names the column, and gives it the side table's type.
		selects[0] = "SELECT CAST(? AS CHAR CHARACTER SET utf8mb4) AS id"

		return strings.Join(selects, " UNION ALL "), args, true
	}

	return m.elemMatchIDs(key, v)
}

// elemMatch is the side table condition of
// {array: {$elemMatch: {field: v | {$in: [...]}}}}, as elemMatchIDs.
func (m *multikey) elemMatch(array string, v any) (string, []any, bool) {
	ids, args, ok := m.elemMatchIDs(array, v)
	if !ok {
		return "", nil, false
	}

	return fmt.Sprintf(`%s IN (%s)`, idColumn(), ids), args, true
}

// elemMatchIDs is the SELECT of the _ids of
// {array: {$elemMatch: {field: v | {$in: [...]}}}} from the side table of the
// first inner field that has a complete one, when its values are all strings
// the table can hold.
func (m *multikey) elemMatchIDs(array string, v any) (string, []any, bool) {
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
			`SELECT id FROM %s.%s WHERE v IN (%s)`,
			metadata.QuoteIdent(m.dbName),
			metadata.QuoteIdent(metadata.MultikeyTableName(m.table, array, field)),
			strings.Join(placeholders, ", "),
		), args, true
	}

	return "", nil, false
}
