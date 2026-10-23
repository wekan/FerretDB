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
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

func mkDoc(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
func mkArr(values ...any) *types.Array   { return must.NotFail(types.NewArray(values...)) }

// multikeyDocuments are the shapes a members array takes, and the ones it
// should not be confused with.
func multikeyDocuments() []*types.Document {
	return []*types.Document{
		mkDoc("_id", "member", "members", mkArr(mkDoc("userId", "u1", "isActive", true))),
		mkDoc("_id", "inactive", "members", mkArr(mkDoc("userId", "u1", "isActive", false))),
		mkDoc("_id", "other", "members", mkArr(mkDoc("userId", "u2", "isActive", true))),
		mkDoc("_id", "second", "members", mkArr(mkDoc("userId", "u2"), mkDoc("userId", "u1", "isActive", true))),
		mkDoc("_id", "field-array", "members", mkArr(mkDoc("userId", mkArr("u3", "u1"), "isActive", true))),
		mkDoc("_id", "object", "members", mkDoc("userId", "u1", "isActive", true)),
		mkDoc("_id", "scalars", "members", mkArr("u1")),
		mkDoc("_id", "number", "members", mkArr(mkDoc("userId", int32(1)))),
		mkDoc("_id", "missing", "title", "no members"),
		mkDoc("_id", "starred", "members", mkArr(), "permission", "public"),
	}
}

type multikeyEnv struct {
	ctx  context.Context
	b    backends.Backend
	db   backends.Database
	coll backends.Collection
	uri  string
	name string
}

func newMultikeyEnv(t *testing.T, uri string, dbName string) *multikeyEnv {
	t.Helper()

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := NewBackend(&NewBackendParams{URI: uri, L: testutil.Logger(t), P: sp, BatchSize: 100})
	require.NoError(t, err)

	db, err := b.Database(dbName)
	require.NoError(t, err)

	coll, err := db.Collection("boards")
	require.NoError(t, err)

	return &multikeyEnv{ctx: testutil.Ctx(t), b: b, db: db, coll: coll, uri: uri, name: dbName}
}

// candidates returns the _ids the backend hands to the Go filter, and those
// the filter keeps.
func (e *multikeyEnv) candidates(t *testing.T, filter *types.Document) ([]string, []string) {
	t.Helper()

	res, err := e.coll.Query(e.ctx, &backends.QueryParams{Filter: filter})
	require.NoError(t, err)

	defer res.Iter.Close()

	var all, matched []string

	for {
		_, doc, err := res.Iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		require.NoError(t, err)

		id := must.NotFail(doc.Get("_id")).(string)
		all = append(all, id)

		if ok, err := common.FilterDocument(doc, filter); err == nil && ok {
			matched = append(matched, id)
		}
	}

	slices.Sort(all)
	slices.Sort(matched)

	return all, matched
}

// raw opens the database file directly, beside FerretDB.
func (e *multikeyEnv) raw(t *testing.T, mode string) *sql.DB {
	t.Helper()

	path := strings.TrimPrefix(strings.SplitN(e.uri, "?", 2)[0], "file:")
	raw, err := sql.Open("sqlite", "file:"+path+e.name+".sqlite?mode="+mode)
	require.NoError(t, err)

	return raw
}

// sideTables lists the multikey tables of the database file.
func (e *multikeyEnv) sideTables(t *testing.T) []string {
	t.Helper()

	raw := e.raw(t, "ro")
	defer raw.Close()

	rows, err := raw.QueryContext(e.ctx, `SELECT name FROM sqlite_master WHERE name LIKE '%\_mk\_%' ESCAPE '\'`)
	require.NoError(t, err)

	defer rows.Close()

	var names []string

	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}

	return names
}

// planOf returns SQLite's query plan for the filter.
func planOf(t *testing.T, e *multikeyEnv, filter *types.Document) string {
	t.Helper()

	explain, err := e.coll.Explain(e.ctx, &backends.ExplainParams{Filter: filter})
	require.NoError(t, err)

	plan := must.NotFail(explain.QueryPlanner.Get("Plan")).(*types.Array)

	var lines []string
	for i := 0; i < plan.Len(); i++ {
		lines = append(lines, must.NotFail(plan.Get(i)).(string))
	}

	return strings.Join(lines, "\n")
}

func elemMatchUser(user string) *types.Document {
	return mkDoc("members", mkDoc("$elemMatch", mkDoc("userId", user, "isActive", true)))
}

func TestMultikeyElemMatch(t *testing.T) {
	t.Parallel()

	e := newMultikeyEnv(t, testutil.TestSQLiteURI(t, ""), testutil.DatabaseName(t))
	t.Cleanup(e.b.Close)

	_, err := e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: multikeyDocuments()})
	require.NoError(t, err)

	// Before the index: no side table, every document is a candidate. This is
	// also the regression test of the $elemMatch pushdown itself: the "scalars"
	// document's array of strings made it fail with "malformed JSON".
	all, matched := e.candidates(t, elemMatchUser("u1"))
	want := []string{"field-array", "member", "second"}
	assert.Equal(t, want, matched)
	assert.Empty(t, e.sideTables(t))

	// The index is created on existing documents: they are backfilled.
	_, err = e.coll.CreateIndexes(e.ctx, &backends.CreateIndexesParams{Indexes: []backends.IndexInfo{{
		Name: "members.userId_1", Key: []backends.IndexKeyPair{{Field: "members.userId"}},
	}}})
	require.NoError(t, err)
	assert.Len(t, e.sideTables(t), 6, "the table, its id index and four triggers")

	all, matched = e.candidates(t, elemMatchUser("u1"))
	assert.Equal(t, want, matched, "the same matches")
	assert.Subset(t, all, want, "every match is a candidate")
	assert.NotContains(t, all, "other", "negative: a board of another user is not even read")
	assert.NotContains(t, all, "missing")
	assert.NotContains(t, all, "object", "$elemMatch needs an array")
	assert.NotContains(t, all, "scalars")

	assert.Contains(t, planOf(t, e, elemMatchUser("u1")), "__id_", "read through the _id index")
	assert.Contains(t, planOf(t, e, elemMatchUser("u1")), "_mk_", "with the side table")

	// $in inside the $elemMatch.
	_, matched = e.candidates(t, mkDoc("members", mkDoc("$elemMatch", mkDoc("userId", mkDoc("$in", mkArr("u2", "u3"))))))
	assert.Equal(t, []string{"field-array", "other", "second"}, matched)

	// Writes keep the side table current: an update that removes u1, an insert
	// that adds u1, a delete.
	_, err = e.coll.UpdateAll(e.ctx, &backends.UpdateAllParams{Docs: []*types.Document{
		mkDoc("_id", "member", "members", mkArr(mkDoc("userId", "u9", "isActive", true))),
		mkDoc("_id", "other", "members", mkArr(mkDoc("userId", "u1", "isActive", true))),
	}})
	require.NoError(t, err)

	_, err = e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: []*types.Document{
		mkDoc("_id", "new", "members", mkArr(mkDoc("userId", "u1", "isActive", true))),
	}})
	require.NoError(t, err)

	_, err = e.coll.DeleteAll(e.ctx, &backends.DeleteAllParams{IDs: []any{"second"}})
	require.NoError(t, err)

	all, matched = e.candidates(t, elemMatchUser("u1"))
	assert.Equal(t, []string{"field-array", "new", "other"}, matched)
	assert.NotContains(t, all, "member", "the update removed its u1")
	assert.NotContains(t, all, "second", "deleted")

	// A top-level $or of a membership and the user's starred _ids: the shape
	// of a "which boards may this user see" selector.
	or := mkDoc("$or", mkArr(elemMatchUser("u1"), mkDoc("_id", mkDoc("$in", mkArr("starred")), "permission", "public")))
	all, _ = e.candidates(t, or)
	assert.Subset(t, all, []string{"field-array", "new", "other", "starred"})
	assert.NotContains(t, all, "missing", "negative: neither a member nor starred")

	// Negative: an $or with a branch that names no _ids does not use the side
	// table - any document could match that branch - and still answers right.
	open := mkDoc("$or", mkArr(elemMatchUser("u1"), mkDoc("permission", "public")))
	_, matched = e.candidates(t, open)
	assert.Equal(t, []string{"field-array", "new", "other", "starred"}, matched)
	assert.NotContains(t, planOf(t, e, open), "_mk_")

	// Dropping the index drops the side table.
	_, err = e.coll.DropIndexes(e.ctx, &backends.DropIndexesParams{Indexes: []string{"members.userId_1"}})
	require.NoError(t, err)
	assert.Empty(t, e.sideTables(t))
	_, matched = e.candidates(t, elemMatchUser("u1"))
	assert.Equal(t, []string{"field-array", "new", "other"}, matched, "and the answer stays the same")
}

func TestMultikeyCollectionDropAndUpgrade(t *testing.T) {
	t.Parallel()

	uri := testutil.TestSQLiteURI(t, "")
	dbName := testutil.DatabaseName(t)

	e := newMultikeyEnv(t, uri, dbName)
	_, err := e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: multikeyDocuments()})
	require.NoError(t, err)
	_, err = e.coll.CreateIndexes(e.ctx, &backends.CreateIndexesParams{Indexes: []backends.IndexInfo{{
		Name: "members.userId_1", Key: []backends.IndexKeyPair{{Field: "members.userId"}},
	}}})
	require.NoError(t, err)

	// A database written by a version without side tables: the table removed and
	// the stored index format set back to 2.
	e.b.Close()

	raw := e.raw(t, "rw")
	var table string
	require.NoError(t, raw.QueryRowContext(e.ctx, `SELECT table_name FROM _ferretdb_collections WHERE name = 'boards'`).Scan(&table))
	mk := metadata.MultikeyTableName(table, "members", "userId")
	for _, q := range []string{
		`DROP TRIGGER "` + mk + `_ai"`,
		`DROP TRIGGER "` + mk + `_bu"`,
		`DROP TRIGGER "` + mk + `_au"`,
		`DROP TRIGGER "` + mk + `_ad"`,
		`DROP TABLE "` + mk + `"`,
		`UPDATE _ferretdb_collections SET settings = json_set(settings, '$.indexFormat', 2)`,
	} {
		_, err = raw.ExecContext(e.ctx, q)
		require.NoError(t, err, q)
	}
	require.NoError(t, raw.Close())
	assert.Empty(t, e.sideTables(t))

	// Opening it upgrades it: the side table is built from the documents.
	e = newMultikeyEnv(t, uri, dbName)
	t.Cleanup(e.b.Close)
	assert.Len(t, e.sideTables(t), 6)

	all, matched := e.candidates(t, elemMatchUser("u1"))
	assert.Equal(t, []string{"field-array", "member", "second"}, matched)
	assert.NotContains(t, all, "other")

	// Dropping the collection drops its side table.
	err = e.db.DropCollection(e.ctx, &backends.DropCollectionParams{Name: "boards"})
	require.NoError(t, err)
	assert.Empty(t, e.sideTables(t))
}

func TestMultikeyPath(t *testing.T) {
	t.Parallel()

	for field, ok := range map[string]bool{
		"members.userId": true, "a.b": true, "orgs.org-id": true,
		"members": false, "a.b.c": false, ".b": false, "a.": false, "$a.b": false,
		`a.b"c`: false, `a\b.c`: false, "a b.c": false,
	} {
		_, _, got := metadata.MultikeyPath(metadata.IndexInfo{Key: []metadata.IndexKeyPair{{Field: field}}})
		assert.Equal(t, ok, got, field)
	}
}

// A field named like a json_each column - key, value, type, id, path - used to
// BE that column inside the $elemMatch pushdown and the side table: an
// ambiguous column, or a comparison with the element's index instead of its
// field, which lost real matches.
func TestElemMatchFieldNamedLikeJSONEachColumn(t *testing.T) {
	t.Parallel()

	e := newMultikeyEnv(t, testutil.TestSQLiteURI(t, ""), testutil.DatabaseName(t))
	t.Cleanup(e.b.Close)

	_, err := e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: []*types.Document{
		mkDoc("_id", "yes", "items", mkArr(mkDoc("key", "k1", "id", "i1", "value", "v1", "type", "t1", "path", "p1"))),
		mkDoc("_id", "no", "items", mkArr(mkDoc("key", "k2", "id", "i2", "value", "v2", "type", "t2", "path", "p2"))),
		mkDoc("_id", "index", "items", mkArr(mkDoc("key", int32(0)))),
	}})
	require.NoError(t, err)

	check := func(name string) {
		for _, field := range []string{"key", "id", "value", "type", "path"} {
			value := map[string]string{"key": "k1", "id": "i1", "value": "v1", "type": "t1", "path": "p1"}[field]
			_, matched := e.candidates(t, mkDoc("items", mkDoc("$elemMatch", mkDoc(field, value))))
			assert.Equal(t, []string{"yes"}, matched, "%s: %s", name, field)
		}
	}

	check("pushdown")

	// And with side tables on each of them.
	var indexes []backends.IndexInfo
	for _, field := range []string{"key", "id", "value", "type", "path"} {
		indexes = append(indexes, backends.IndexInfo{Name: "items." + field + "_1", Key: []backends.IndexKeyPair{{Field: "items." + field}}})
	}

	_, err = e.coll.CreateIndexes(e.ctx, &backends.CreateIndexesParams{Indexes: indexes})
	require.NoError(t, err)

	check("side table")

	all, _ := e.candidates(t, mkDoc("items", mkDoc("$elemMatch", mkDoc("id", "i1"))))
	assert.Equal(t, []string{"yes"}, all, "narrowed to the one board")
}
