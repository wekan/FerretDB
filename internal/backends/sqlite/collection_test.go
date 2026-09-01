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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
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

func TestQueryDistinctFieldPushdown(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	sp, err := state.NewProvider("")
	require.NoError(t, err)
	b, err := NewBackend(&NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)
	coll, err := db.Collection(testutil.CollectionName(t))
	require.NoError(t, err)

	large := must.NotFail(types.NewArray())
	for i := 0; i < 100; i++ {
		large.Append(must.NotFail(types.NewDocument("unused", int64(i))))
	}
	docs := []*types.Document{
		must.NotFail(types.NewDocument("_id", "1", "swimlaneId", "s1", "archived", false, "payload", large)),
		must.NotFail(types.NewDocument("_id", "2", "swimlaneId", "s1", "archived", false, "payload", large)),
		must.NotFail(types.NewDocument("_id", "3", "swimlaneId", "s2", "archived", false, "payload", large)),
		must.NotFail(types.NewDocument("_id", "4", "swimlaneId", "s3", "archived", true, "payload", large)),
		must.NotFail(types.NewDocument("_id", "5", "archived", false, "payload", large)),
	}
	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: docs})
	require.NoError(t, err)

	res, err := coll.Query(ctx, &backends.QueryParams{
		Filter:        must.NotFail(types.NewDocument("archived", false)),
		DecodeFields:  []string{"archived", "swimlaneId"},
		DistinctField: "swimlaneId",
	})
	require.NoError(t, err)
	actual, err := iterator.ConsumeValues[struct{}, *types.Document](res.Iter)
	require.NoError(t, err)
	require.Len(t, actual, 2, "SQLite must collapse duplicate keys before SJSON decoding")

	values := []string{
		must.NotFail(actual[0].Get("swimlaneId")).(string),
		must.NotFail(actual[1].Get("swimlaneId")).(string),
	}
	assert.ElementsMatch(t, []string{"s1", "s2"}, values)
	for _, doc := range actual {
		assert.False(t, doc.Has("payload"), "unconsumed fields must not cross the SQLite iterator")
		assert.Equal(t, false, must.NotFail(doc.Get("archived")))
	}
}

func TestQueryNumericTypeNorRangePushdown(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	sp, err := state.NewProvider("")
	require.NoError(t, err)
	r, err := metadata.NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)
	dbName, collectionName := testutil.DatabaseName(t), testutil.CollectionName(t)
	sqlDB, err := r.DatabaseGetOrCreate(ctx, dbName)
	require.NoError(t, err)
	_, err = r.CollectionCreate(ctx, &metadata.CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err)
	require.NoError(t, r.IndexesCreate(ctx, dbName, collectionName, []metadata.IndexInfo{{
		Name: "boardId_1_sort_1",
		Key:  []metadata.IndexKeyPair{{Field: "boardId"}, {Field: "sort"}},
	}}))
	coll := newCollection(r, dbName, collectionName)

	docs := []*types.Document{
		must.NotFail(types.NewDocument("_id", "nan", "sort", math.NaN())),
		must.NotFail(types.NewDocument("_id", "finite-double", "sort", float64(1.5))),
		must.NotFail(types.NewDocument("_id", "finite-int", "sort", int32(2))),
		must.NotFail(types.NewDocument("_id", "string", "sort", "NaN")),
		must.NotFail(types.NewDocument("_id", "missing")),
		must.NotFail(types.NewDocument("_id", "array", "sort", must.NotFail(types.NewArray(int32(3))))),
	}
	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: docs})
	require.NoError(t, err)

	max := 1.7976931348623157e308
	filter := must.NotFail(types.NewDocument(
		"sort", must.NotFail(types.NewDocument("$type", "number")),
		"$nor", must.NotFail(types.NewArray(must.NotFail(types.NewDocument(
			"sort", must.NotFail(types.NewDocument("$gte", -max, "$lte", max)),
		)))),
	))
	res, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
	require.NoError(t, err)
	actual, err := iterator.ConsumeValues[struct{}, *types.Document](res.Iter)
	require.NoError(t, err)

	ids := make([]string, 0, len(actual))
	for _, doc := range actual {
		ids = append(ids, must.NotFail(doc.Get("_id")).(string))
	}
	assert.ElementsMatch(t, []string{"nan", "string", "array"}, ids,
		"non-finite numbers and safe string/array supersets remain; finite scalars and missing fields are pruned")
	indexName, _ := preferredNumericRangeIndex(r.CollectionGet(ctx, dbName, collectionName).TableName,
		r.CollectionGet(ctx, dbName, collectionName).Settings.Indexes, filter)
	var physicalIndex string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&physicalIndex))
	assert.Equal(t, indexName, physicalIndex, "the eligible query creates its private scalar access path")
}

func TestQueryExistsPushdown(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	sp, err := state.NewProvider("")
	require.NoError(t, err)
	b, err := NewBackend(&NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)
	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)
	coll, err := db.Collection(testutil.CollectionName(t))
	require.NoError(t, err)
	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: []*types.Document{
		must.NotFail(types.NewDocument("_id", "missing")),
		must.NotFail(types.NewDocument("_id", "null", "archived", types.Null)),
		must.NotFail(types.NewDocument("_id", "false", "archived", false)),
	}})
	require.NoError(t, err)

	queryIDs := func(exists bool) []string {
		filter := must.NotFail(types.NewDocument(
			"archived", must.NotFail(types.NewDocument("$exists", exists)),
		))
		res, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
		require.NoError(t, err)
		docs, err := iterator.ConsumeValues[struct{}, *types.Document](res.Iter)
		require.NoError(t, err)
		ids := make([]string, 0, len(docs))
		for _, doc := range docs {
			ids = append(ids, must.NotFail(doc.Get("_id")).(string))
		}
		return ids
	}

	assert.Equal(t, []string{"missing"}, queryIDs(false), "explicit null exists and must not match $exists:false")
	assert.ElementsMatch(t, []string{"null", "false"}, queryIDs(true))
}

// TestQueryOrPushdown verifies end-to-end that a top-level $or is pushed down
// CORRECTLY, which for an OR means one thing above all: NOTHING THAT MATCHES IS
// LOST. Every other pushdown narrows, and the Go filter removes whatever extra
// the SQL let through; an OR that drops a branch removes rows the Go filter
// never sees. Query applies ONLY the pushdown WHERE, so what comes back here is
// exactly what the clause selected.
func TestQueryOrPushdown(t *testing.T) {
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
	coll, err := db.Collection(cName)
	require.NoError(t, err)

	// Three documents, each matching a different branch, and one matching none.
	docs := []*types.Document{
		must.NotFail(types.NewDocument("_id", "public-board", "permission", "public", "owner", "ann")),
		must.NotFail(types.NewDocument("_id", "anns-board", "permission", "private", "owner", "ann")),
		must.NotFail(types.NewDocument("_id", "bobs-board", "permission", "private", "owner", "bob")),
	}

	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: docs})
	require.NoError(t, err)

	// The shape this exists for: the selective terms are inside the $or.
	filter := must.NotFail(types.NewDocument("$or", must.NotFail(types.NewArray(
		must.NotFail(types.NewDocument("permission", "public")),
		must.NotFail(types.NewDocument("owner", "ann")),
	))))

	res, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
	require.NoError(t, err)

	got := map[string]struct{}{}

	for {
		_, doc, err := res.Iter.Next()
		if err != nil {
			break
		}

		id, _ := must.NotFail(doc.Get("_id")).(string)
		got[id] = struct{}{}
	}

	res.Iter.Close()

	// BOTH branches' matches come back. If either were dropped, one of these is
	// missing and the query silently returns fewer boards than the user has.
	assert.Contains(t, got, "public-board", "the permission branch must not be lost")
	assert.Contains(t, got, "anns-board", "the owner branch must not be lost")

	// And the clause really narrowed: a document matching NEITHER branch is not
	// returned, which is what makes this worth pushing down at all.
	assert.NotContains(t, got, "bobs-board", "the clause must exclude non-matches")

	explainRes, err := coll.Explain(ctx, &backends.ExplainParams{Filter: filter})
	require.NoError(t, err)
	assert.True(t, explainRes.FilterPushdown, "the $or must be reported as pushed down")
}

func TestQueryOrWithDottedArrayPaths(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	sp, err := state.NewProvider("")
	require.NoError(t, err)
	b, err := NewBackend(&NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)
	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)
	coll, err := db.Collection(testutil.CollectionName(t))
	require.NoError(t, err)

	tokens := must.NotFail(types.NewArray(must.NotFail(types.NewDocument("hashedToken", "wanted"))))
	resume := must.NotFail(types.NewDocument("loginTokens", tokens))
	services := must.NotFail(types.NewDocument("resume", resume))
	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: []*types.Document{
		must.NotFail(types.NewDocument("_id", "matching", "services", services)),
		must.NotFail(types.NewDocument("_id", "other")),
	}})
	require.NoError(t, err)

	filter := must.NotFail(types.NewDocument("$or", must.NotFail(types.NewArray(
		must.NotFail(types.NewDocument("services.resume.loginTokens.hashedToken", "wanted")),
		must.NotFail(types.NewDocument("services.resume.loginTokens.token", "missing")),
	))))
	res, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
	require.NoError(t, err)
	docs, err := iterator.ConsumeValues[struct{}, *types.Document](res.Iter)
	require.NoError(t, err)
	require.Len(t, docs, 2, "an unpushed OR must leave every candidate for the authoritative filter")

	explainRes, err := coll.Explain(ctx, &backends.ExplainParams{Filter: filter})
	require.NoError(t, err)
	assert.False(t, explainRes.FilterPushdown)
}

// TestQueryElemMatchPushdown verifies that all pushed predicates are applied to
// one array element. A document with the requested values split across two
// elements must not pass the SQLite WHERE clause.
func TestQueryElemMatchPushdown(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := NewBackend(&NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	db, err := b.Database(testutil.DatabaseName(t))
	require.NoError(t, err)
	coll, err := db.Collection(testutil.CollectionName(t))
	require.NoError(t, err)

	docs := []*types.Document{
		must.NotFail(types.NewDocument(
			"_id", "same-element",
			"members", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("userId", "u1", "isActive", true)),
			)),
		)),
		must.NotFail(types.NewDocument(
			"_id", "split-elements",
			"members", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("userId", "u1", "isActive", false)),
				must.NotFail(types.NewDocument("userId", "u2", "isActive", true)),
			)),
		)),
	}

	_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: docs})
	require.NoError(t, err)

	filter := must.NotFail(types.NewDocument(
		"members", must.NotFail(types.NewDocument(
			"$elemMatch", must.NotFail(types.NewDocument("userId", "u1", "isActive", true)),
		)),
	))
	res, err := coll.Query(ctx, &backends.QueryParams{Filter: filter})
	require.NoError(t, err)
	defer res.Iter.Close()

	var ids []string
	for {
		_, doc, nextErr := res.Iter.Next()
		if nextErr != nil {
			break
		}
		ids = append(ids, must.NotFail(doc.Get("_id")).(string))
	}

	assert.Equal(t, []string{"same-element"}, ids)

	explainRes, err := coll.Explain(ctx, &backends.ExplainParams{Filter: filter})
	require.NoError(t, err)
	assert.True(t, explainRes.FilterPushdown)
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
