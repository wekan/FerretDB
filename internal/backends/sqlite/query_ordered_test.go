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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// orderedValues are simple values of every type the ORDER BY sorts, mixed, with
// ties, so the database order can be compared with the Go sort.
func orderedValues() []any {
	date := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	return []any{
		int32(5), int64(-3), float64(2.5), int64(5), float64(-0.5), int32(0),
		"b", "a", "", "B", "äö", "a",
		types.ObjectID{1, 2, 3}, types.ObjectID{0, 255},
		true, false,
		date, date.Add(-time.Hour),
		types.Null, nil, // nil: the field is missing
		int64(1 << 40), float64(1e10),
	}
}

func orderedTestCollection(t *testing.T, values []any, field string) backends.Collection {
	t.Helper()

	ctx := testutil.Ctx(t)
	sp, err := state.NewProvider("")
	require.NoError(t, err)
	b, err := NewBackend(&NewBackendParams{URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)
	coll, err := db.Collection(testutil.CollectionName(t))
	require.NoError(t, err)

	docs := make([]*types.Document, 0, len(values))
	for i, v := range values {
		doc := must.NotFail(types.NewDocument("_id", int32(i), "second", int32(i%3)))
		if v != nil {
			doc.Set(field, v)
		}
		docs = append(docs, doc)
	}

	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: docs})
	require.NoError(t, err)

	return coll
}

// sortedBoth returns the documents in the order the handler returns them with
// the database sorting (OrderedSortIterator over an Ordered result) and with
// Go sorting everything.
func sortedBoth(t *testing.T, coll backends.Collection, sortDoc *types.Document, keep int64) (db, golang []*types.Document) {
	t.Helper()

	ctx := testutil.Ctx(t)

	res, err := coll.Query(ctx, &backends.QueryParams{OrderBy: sortDoc})
	require.NoError(t, err)
	require.True(t, res.Ordered, "a plain sort of top-level fields is sorted by SQLite")

	closer := iterator.NewMultiCloser()
	defer closer.Close()

	iter, err := common.OrderedSortIterator(res.Iter, closer, sortDoc, keep)
	require.NoError(t, err)
	db, err = iterator.ConsumeValues(iter)
	require.NoError(t, err)

	res, err = coll.Query(ctx, nil)
	require.NoError(t, err)
	golang, err = iterator.ConsumeValues(res.Iter)
	require.NoError(t, err)
	require.NoError(t, common.SortDocuments(golang, sortDoc))

	if keep > 0 && int64(len(db)) > keep {
		db = db[:keep]
	}
	if keep > 0 && int64(len(golang)) > keep {
		golang = golang[:keep]
	}

	return db, golang
}

// sortKeys are the values of the sort fields, the part of the order that is
// defined (ties may come in any order). Values MongoDB sorts as equal - null
// and a missing field, the same number as int32, int64 or double - are made
// the same.
func sortKeys(docs []*types.Document, sortDoc *types.Document) [][]string {
	res := make([][]string, len(docs))
	for i, doc := range docs {
		for _, key := range sortDoc.Keys() {
			v, _ := doc.Get(key)
			switch n := v.(type) {
			case nil:
				v = types.Null
			case int32:
				v = float64(n)
			case int64:
				v = float64(n)
			}
			res[i] = append(res[i], fmt.Sprintf("%T %v", v, v)) // NaN, and documents by content
		}
	}

	return res
}

func TestOrderedQueryMatchesGoSort(t *testing.T) {
	t.Parallel()

	for name, sortDoc := range map[string]*types.Document{
		"asc":         must.NotFail(types.NewDocument("v", int64(1))),
		"desc":        must.NotFail(types.NewDocument("v", int64(-1))),
		"compound":    must.NotFail(types.NewDocument("second", int64(-1), "v", int64(1))),
		"missingOnly": must.NotFail(types.NewDocument("nothere", int64(1))),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			coll := orderedTestCollection(t, orderedValues(), "v")

			for _, keep := range []int64{0, 1, 7} {
				db, golang := sortedBoth(t, coll, sortDoc, keep)
				require.Len(t, db, len(golang))
				assert.Equal(t, sortKeys(golang, sortDoc), sortKeys(db, sortDoc), "keep %d", keep)
			}
		})
	}
}

func TestOrderedQueryComplexValuesFallBackToGoSort(t *testing.T) {
	// Negative: values SQLite cannot order (documents, arrays, NaN) must not be
	// streamed in database order - they come first, and the handler sorts.
	t.Parallel()

	values := append(orderedValues(),
		must.NotFail(types.NewArray(int64(7), int64(-10))),
		must.NotFail(types.NewDocument("x", int64(1))),
		math.NaN(),
		math.Inf(-1),
	)

	for _, dir := range []int64{1, -1} {
		coll := orderedTestCollection(t, values, "v")
		sortDoc := must.NotFail(types.NewDocument("v", dir))

		res, err := coll.Query(testutil.Ctx(t), &backends.QueryParams{OrderBy: sortDoc})
		require.NoError(t, err)
		require.True(t, res.Ordered)
		raw, err := iterator.ConsumeValues(res.Iter)
		require.NoError(t, err)
		require.NotEmpty(t, raw)
		first := must.NotFail(raw[0].Get("v"))
		switch first.(type) {
		case *types.Array, *types.Document:
		case float64:
			assert.True(t, math.IsNaN(first.(float64)) || math.IsInf(first.(float64), 0), "%v", first)
		default:
			t.Fatalf("a complex value must come first, got %v", first)
		}

		for _, keep := range []int64{0, 3} {
			db, golang := sortedBoth(t, coll, sortDoc, keep)
			require.Len(t, db, len(golang))
			assert.Equal(t, sortKeys(golang, sortDoc), sortKeys(db, sortDoc), "dir %d keep %d", dir, keep)
		}
	}
}

func TestOrderedQueryNotUsed(t *testing.T) {
	// Negative: a dotted path, or a sort on top of an existing Sort, is left
	// to the handler.
	t.Parallel()

	coll := orderedTestCollection(t, orderedValues(), "v")
	ctx := testutil.Ctx(t)

	// Each iterator is closed: one left open panics in the debug build's
	// finalizer, failing the whole package whenever the GC happens to run.
	res, err := coll.Query(ctx, &backends.QueryParams{OrderBy: must.NotFail(types.NewDocument("v.x", int64(1)))})
	require.NoError(t, err)
	res.Iter.Close()
	assert.False(t, res.Ordered)

	res, err = coll.Query(ctx, &backends.QueryParams{OrderBy: must.NotFail(types.NewDocument("v", int64(1))), Limit: 2})
	require.NoError(t, err)
	res.Iter.Close()
	assert.False(t, res.Ordered)
}
