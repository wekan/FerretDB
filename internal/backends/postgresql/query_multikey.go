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

package postgresql

import (
	"strings"

	"github.com/FerretDB/FerretDB/internal/backends/postgresql/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// multikeyWhere returns a condition, to be ANDed with prepareWhereClause's, that
// lets PostgreSQL answer a membership test from the array's GIN index
// (metadata/multikey.go), or "":
//
//   - a top-level {ARRAY: {$elemMatch: {FIELD: v, ...}}} with ARRAY.FIELD leading
//     an index becomes `_jsonb->'ARRAY' @> '[{"FIELD": v}]'` (or a FIELD that is
//     itself an array containing v: `@> '[{"FIELD": [v]}]'`);
//   - a top-level $or becomes the OR of each branch's indexable condition - such
//     an $elemMatch, or an equality or $in on _id or an indexed field - when
//     every branch has one; a branch without one could match any row.
//
// Every condition is a SUPERSET of the matches; the Go filter still decides.
func multikeyWhere(p *metadata.Placeholder, meta *metadata.Collection, filter *types.Document) (string, []any) {
	if meta == nil || filter == nil || filter.Len() == 0 || meta.Capped() {
		return "", nil
	}

	arrays := metadata.MultikeyArrays(meta.Indexes)
	indexed := map[string]struct{}{"_id": {}}

	for _, index := range meta.Indexes {
		if len(index.Key) > 0 && !strings.ContainsRune(index.Key[0].Field, '.') {
			indexed[index.Key[0].Field] = struct{}{}
		}
	}

	var conds []string
	var args []any

	for _, key := range filter.Keys() {
		value := must.NotFail(filter.Get(key))

		if key == "$or" {
			if cond, condArgs, ok := orCandidates(p, arrays, indexed, value); ok {
				conds = append(conds, cond)
				args = append(args, condArgs...)
			}

			continue
		}

		if cond, condArgs, ok := elemMatchCandidates(p, arrays, key, value); ok {
			conds = append(conds, cond)
			args = append(args, condArgs...)
		}
	}

	return strings.Join(conds, " AND "), args
}

// orCandidates is the OR of each branch's first indexable condition, or ok=false
// when a branch has none.
func orCandidates(p *metadata.Placeholder, arrays map[string][]string, indexed map[string]struct{}, v any) (string, []any, bool) {
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

			cond, condArgs, ok := elemMatchCandidates(p, arrays, key, value)
			if !ok {
				if _, isIndexed := indexed[key]; isIndexed {
					cond, condArgs, ok = equalityCandidates(p, key, value)
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

// equalityCandidates is the indexed equality or $in on a top-level field.
func equalityCandidates(p *metadata.Placeholder, key string, v any) (string, []any, bool) {
	if strings.HasPrefix(key, "$") || strings.ContainsRune(key, '.') {
		return "", nil, false
	}

	if doc, ok := v.(*types.Document); ok {
		if doc.Len() != 1 {
			return "", nil, false
		}

		switch doc.Keys()[0] {
		case "$eq":
			return filterEqualIndexed(p, key, must.NotFail(doc.Get("$eq")))
		case "$in":
			if arr, ok := must.NotFail(doc.Get("$in")).(*types.Array); ok && arr.Len() > 0 {
				return filterInIndexed(p, key, arr)
			}
		}

		return "", nil, false
	}

	return filterEqualIndexed(p, key, v)
}

// elemMatchCandidates is the containment condition of
// {array: {$elemMatch: {field: v | {$in: [...]}}}} for the first inner field
// whose ARRAY.FIELD has a GIN index and whose values can be compared.
func elemMatchCandidates(p *metadata.Placeholder, arrays map[string][]string, array string, v any) (string, []any, bool) {
	fields, ok := arrays[array]
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

		values := []any{must.NotFail(elem.Get(field))}

		if in, ok := values[0].(*types.Document); ok {
			if in.Len() != 1 || in.Keys()[0] != "$in" {
				continue
			}

			list, ok := must.NotFail(in.Get("$in")).(*types.Array)
			if !ok || list.Len() == 0 {
				continue
			}

			values = values[:0]
			for i := 0; i < list.Len(); i++ {
				values = append(values, must.NotFail(list.Get(i)))
			}
		}

		var arms []string
		var args []any
		usable := true

		expr := metadata.DefaultColumn + `->` + quoteLiteral(array)

		for _, value := range values {
			switch value.(type) {
			case string, types.ObjectID, bool:
			default:
				usable = false
			}

			if !usable {
				break
			}

			text := string(must.NotFail(sjson.MarshalSingleValue(value)))
			label := string(must.NotFail(sjson.MarshalSingleValue(field)))

			// The element's FIELD is the value, or an array that contains it.
			arms = append(arms, expr+` @> `+p.Next(), expr+` @> `+p.Next())
			args = append(args, `[{`+label+`:`+text+`}]`, `[{`+label+`:[`+text+`]}]`)
		}

		if usable {
			return "(" + strings.Join(arms, " OR ") + ")", args, true
		}
	}

	return "", nil, false
}
