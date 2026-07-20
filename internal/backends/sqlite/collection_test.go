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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

func TestCappedCollectionInsertAllQueryExplain(t *testing.T) {
	// remove this test
	// TODO https://github.com/FerretDB/FerretDB/issues/3181

	t.Parallel()

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := NewBackend(&NewBackendParams{URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)

	cName := testutil.CollectionName(t)
	err = db.CreateCollection(ctx, &backends.CreateCollectionParams{
		Name:       cName,
		CappedSize: 8192,
	})
	require.NoError(t, err)

	cappedColl, err := db.Collection(cName)
	require.NoError(t, err)

	insertDocs := []*types.Document{
		must.NotFail(types.NewDocument("_id", types.ObjectID{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})),
		must.NotFail(types.NewDocument("_id", types.ObjectID{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})),
		must.NotFail(types.NewDocument("_id", types.ObjectID{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})),
	}

	_, err = cappedColl.InsertAll(ctx, &backends.InsertAllParams{Docs: insertDocs})
	require.NoError(t, err)

	t.Run("CappedCollectionSort", func(t *testing.T) {
		t.Parallel()

		sort := must.NotFail(types.NewDocument("$natural", int64(1)))
		queryRes, err := cappedColl.Query(ctx, &backends.QueryParams{Sort: sort})
		require.NoError(t, err)

		docs, err := iterator.ConsumeValues[struct{}, *types.Document](queryRes.Iter)
		require.NoError(t, err)
		testutil.AssertEqualSlices(t, insertDocs, docs)
		for i, doc := range docs {
			assert.Equal(t, insertDocs[i].RecordID(), doc.RecordID())
			assert.NotZero(t, doc.RecordID())
		}

		explainRes, err := cappedColl.Explain(ctx, &backends.ExplainParams{Sort: sort})
		require.NoError(t, err)
		assert.True(t, explainRes.SortPushdown)
	})
}

// TestQueryRangePushdownDates verifies end-to-end that a date range filter is
// pushed down to SQLite CORRECTLY: the collection's Query applies ONLY the
// pushdown WHERE (the Go filter runs later in the handler), so its result set is
// exactly what the pushed clause selects. It confirms `->>` extracts sjson's
// millis-encoded date and compares it numerically, and that the clause is a
// correct SUPERSET — a real in-range match is always returned, while
// null/missing/out-of-range dates are excluded (a "filter by date").
func TestQueryRangePushdownDates(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := NewBackend(&NewBackendParams{URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)

	cName := testutil.CollectionName(t)
	require.NoError(t, db.CreateCollection(ctx, &backends.CreateCollectionParams{Name: cName}))

	coll, err := db.Collection(cName)
	require.NoError(t, err)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: []*types.Document{
		must.NotFail(types.NewDocument("_id", "past", "dueAt", base.Add(-time.Hour))),
		must.NotFail(types.NewDocument("_id", "future", "dueAt", base.Add(time.Hour))),
		must.NotFail(types.NewDocument("_id", "nulldate", "dueAt", types.Null)),
		must.NotFail(types.NewDocument("_id", "nodate")),
	}})
	require.NoError(t, err)

	queryIDs := func(filter *types.Document) map[string]bool {
		res, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
		require.NoError(t, err)

		docs, err := iterator.ConsumeValues[struct{}, *types.Document](res.Iter)
		require.NoError(t, err)

		ids := map[string]bool{}
		for _, d := range docs {
			id, _ := d.Get("_id")
			ids[id.(string)] = true
		}

		return ids
	}

	t.Run("Overdue_lte", func(t *testing.T) {
		t.Parallel()

		// {dueAt: {$lte: base}} — an "overdue" filter once past is our reference.
		ids := queryIDs(must.NotFail(types.NewDocument("dueAt",
			must.NotFail(types.NewDocument("$lte", base)))))

		assert.True(t, ids["past"], "a real overdue date must be returned (superset)")
		assert.False(t, ids["future"], "a later date must be pruned")
		assert.False(t, ids["nulldate"], "a null due date must be pruned")
		assert.False(t, ids["nodate"], "a missing due date must be pruned")
	})

	t.Run("Upcoming_gte", func(t *testing.T) {
		t.Parallel()

		ids := queryIDs(must.NotFail(types.NewDocument("dueAt",
			must.NotFail(types.NewDocument("$gte", base)))))

		assert.True(t, ids["future"], "a real later date must be returned (superset)")
		assert.False(t, ids["past"], "an earlier date must be pruned")
		assert.False(t, ids["nulldate"])
		assert.False(t, ids["nodate"])
	})

	t.Run("FilterPushdownReported", func(t *testing.T) {
		t.Parallel()

		explainRes, err := coll.Explain(ctx, &backends.ExplainParams{
			Filter: must.NotFail(types.NewDocument("dueAt",
				must.NotFail(types.NewDocument("$lte", base)))),
		})
		require.NoError(t, err)
		assert.True(t, explainRes.FilterPushdown, "the date range must be pushed down")
	})
}
