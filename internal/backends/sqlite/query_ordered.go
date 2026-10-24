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
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// Sorting in the database (backends.QueryResult.Ordered).
//
// The handler sorted every query in Go: ten newest users, the first page of a
// list, a card window - every matching document was fetched, decoded and sorted
// to return a few. SQLite sorts by a key that reproduces MongoDB's order for
// simple values - the BSON type class (null and missing < numbers < strings <
// ObjectID < bool < date), then the value - with any document whose sort field
// holds something else (a document, an array, NaN, ...) FIRST, so the handler
// sees it before anything it could otherwise stream, and sorts as before. Ties
// keep storage order (rowid).
//
// Only rowids are sorted - the sorter would otherwise carry every whole
// document - and the documents are then fetched by rowid, a batch at a time, in
// that order: a client that stops after ten reads ten documents.

// orderedBatch is how many documents are fetched by rowid at a time.
const orderedBatch = 100

// orderableField is a top-level field name the ORDER BY may name.
var orderableField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// orderByClause returns the ORDER BY for sort, or "" when it cannot be applied.
//
// Each field's sort key is two expressions, evaluated once per row: its type
// CLASS and its value. A value SQLite cannot order (complexClass) gets the class
// that sorts FIRST in that field's direction - 0 ascending, 10 descending - so a
// single-field sort needs nothing more. A compound sort puts every document with
// such a value in ANY of its fields before the rest, which takes one more term.
//
// Each expression reads its own JSON path in one step: a chain of -> writes
// each step's JSON back out as text for the next to parse again, and the first
// version of this - a chain, and the type read three times - made the ten
// newest of 30,000 users take three times as long as decoding all of them in Go.
func orderByClause(sort *types.Document) string {
	if sort == nil || sort.Len() == 0 {
		return ""
	}

	var complexes, keys []string

	for _, field := range sort.Keys() {
		if !orderableField.MatchString(field) {
			return ""
		}

		dir, complexClass := "", 0

		switch must.NotFail(sort.Get(field)) {
		case int64(1), int32(1), float64(1):
		case int64(-1), int32(-1), float64(-1):
			dir, complexClass = " DESC", 10
		default:
			return ""
		}

		t := `COALESCE(` + metadata.DefaultColumn + `->>'$."$s"."p"."` + field + `"."t"', 'null')`
		value := metadata.DefaultColumn + `->>` + metadata.LabelLiteral(field)

		// NaN and the infinities are doubles stored as JSON strings.
		class := fmt.Sprintf(`(CASE %[1]s WHEN 'null' THEN 1 WHEN 'int' THEN 2 WHEN 'long' THEN 2 `+
			`WHEN 'double' THEN (CASE WHEN json_type(%[2]s, '$."%[3]s"') = 'text' THEN %[4]d ELSE 2 END) `+
			`WHEN 'string' THEN 3 WHEN 'objectId' THEN 7 WHEN 'bool' THEN 8 WHEN 'date' THEN 9 ELSE %[4]d END)`,
			t, metadata.DefaultColumn, field, complexClass)

		complexes = append(complexes, fmt.Sprintf(`%s = %d`, class, complexClass))
		keys = append(keys, class+dir, value+dir)
	}

	order := ` ORDER BY `
	if len(complexes) > 1 {
		order += `(` + strings.Join(complexes, ` OR `) + `) DESC, `
	}

	return order + strings.Join(keys, `, `) + `, rowid`
}

// orderedIterator returns the documents of the given rowids, in that order,
// fetching them a batch at a time.
type orderedIterator struct {
	ctx          context.Context
	db           *fsql.DB
	table        string
	decodeFields []string
	m            sync.Mutex
	rowids       []int64
	batch        []*types.Document
}

func (iter *orderedIterator) Next() (struct{}, *types.Document, error) {
	iter.m.Lock()
	defer iter.m.Unlock()

	if len(iter.batch) == 0 {
		if len(iter.rowids) == 0 {
			return struct{}{}, nil, iterator.ErrIteratorDone
		}

		if err := iter.fetch(); err != nil {
			iter.rowids = nil
			return struct{}{}, nil, lazyerrors.Error(err)
		}
	}

	doc := iter.batch[0]
	iter.batch = iter.batch[1:]

	return struct{}{}, doc, nil
}

func (iter *orderedIterator) fetch() error {
	n := min(orderedBatch, len(iter.rowids))
	ids := iter.rowids[:n]
	iter.rowids = iter.rowids[n:]

	placeholders := strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
	args := make([]any, n)
	for i, id := range ids {
		args[i] = id
	}

	rows, err := iter.db.QueryContext(iter.ctx,
		fmt.Sprintf(`SELECT rowid, %s FROM %q WHERE rowid IN (%s)`, metadata.DefaultColumn, iter.table, placeholders),
		args...)
	if err != nil {
		return lazyerrors.Error(err)
	}
	defer rows.Close()

	byID := make(map[int64][]byte, n)
	for rows.Next() {
		var id int64
		var b []byte
		if err = rows.Scan(&id, &b); err != nil {
			return lazyerrors.Error(err)
		}
		byID[id] = b
	}

	if err = rows.Err(); err != nil {
		return lazyerrors.Error(err)
	}

	iter.batch = iter.batch[:0]

	for _, id := range ids {
		b, ok := byID[id]
		if !ok {
			continue // deleted between the two statements
		}

		var doc *types.Document
		if len(iter.decodeFields) == 0 {
			doc, err = sjson.Unmarshal(b)
		} else {
			doc, err = sjson.UnmarshalFields(b, iter.decodeFields)
		}

		if err != nil {
			return lazyerrors.Error(err)
		}

		iter.batch = append(iter.batch, doc)
	}

	return nil
}

func (iter *orderedIterator) Close() {
	iter.m.Lock()
	defer iter.m.Unlock()

	iter.rowids = nil
	iter.batch = nil
}

// queryOrdered runs the query sorted by rowid key: the sorted rowids first, then
// the documents.
func queryOrdered(ctx context.Context, db *fsql.DB, table, suffix string, args []any, decodeFields []string) (types.DocumentsIterator, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT rowid FROM %q%s`, table, suffix), args...)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}
	defer rows.Close()

	var rowids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, lazyerrors.Error(err)
		}
		rowids = append(rowids, id)
	}

	if err = rows.Err(); err != nil {
		return nil, lazyerrors.Error(err)
	}

	return &orderedIterator{ctx: ctx, db: db, table: table, decodeFields: decodeFields, rowids: rowids}, nil
}
