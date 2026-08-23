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

package metadata

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
	"github.com/FerretDB/FerretDB/internal/util/testutil/teststress"
)

// testCollection creates, tests, and drops a unique collection in the existing database.
func testCollection(t *testing.T, ctx context.Context, r *Registry, db *fsql.DB, dbName, collectionName string) {
	t.Helper()

	c := r.CollectionGet(ctx, dbName, collectionName)
	require.Nil(t, c)

	created, err := r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err)
	require.True(t, created)

	created, err = r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err)
	require.False(t, created)

	c = r.CollectionGet(ctx, dbName, collectionName)
	require.NotNil(t, c)
	require.Equal(t, collectionName, c.Name)

	list, err := r.CollectionList(ctx, dbName)
	require.NoError(t, err)
	require.Contains(t, list, c)

	q := fmt.Sprintf("INSERT INTO %q (%s) VALUES(?)", c.TableName, DefaultColumn)
	doc := `{"$s": {"p": {"_id": {"t": "int"}}, "$k": ["_id"]}, "_id": 42}`
	_, err = db.ExecContext(ctx, q, doc)
	require.NoError(t, err)

	dropped, err := r.CollectionDrop(ctx, dbName, collectionName)
	require.NoError(t, err)
	require.True(t, dropped)

	dropped, err = r.CollectionDrop(ctx, dbName, collectionName)
	require.NoError(t, err)
	require.False(t, dropped)

	c = r.CollectionGet(ctx, dbName, collectionName)
	require.Nil(t, c)
}

func TestCreateDrop(t *testing.T) {
	t.Parallel()
	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	r, err := NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)

	dbName := testutil.DatabaseName(t)

	db, err := r.DatabaseGetOrCreate(ctx, dbName)
	require.NoError(t, err)
	require.NotNil(t, db)

	state := sp.Get()
	require.Equal(t, "SQLite", state.BackendName)
	require.Equal(t, "3.53.3", state.BackendVersion)

	collectionName := testutil.CollectionName(t)

	testCollection(t, ctx, r, db, dbName, collectionName)
}

// TestCollectionCreateAdoptsOrphanTable is the regression test for orphan-table adoption:
// collectionCreate must ADOPT a physical table that exists on disk but whose
// metadata row is gone (an orphan left by an interrupted migration or a crash),
// instead of failing with `table "<db>.<coll>_<hash>" already exists`. That
// error, raised from an upsert's CreateCollection, surfaced as an
// unhandled rejection in the client's scheduler and crash-looped the server so its web
// port never stayed open.
func TestCollectionCreateAdoptsOrphanTable(t *testing.T) {
	t.Parallel()
	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	r, err := NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)

	dbName := testutil.DatabaseName(t)

	db, err := r.DatabaseGetOrCreate(ctx, dbName)
	require.NoError(t, err)
	require.NotNil(t, db)

	collectionName := testutil.CollectionName(t)

	// Create normally: physical table + metadata row + in-memory registration.
	created, err := r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err)
	require.True(t, created)

	c := r.CollectionGet(ctx, dbName, collectionName)
	require.NotNil(t, c)
	tableName := c.TableName

	// Orphan the table: drop its metadata row and forget it in memory, but leave
	// the physical table on disk — exactly the desync that #6476 hit.
	_, err = db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %q WHERE table_name = ?", metadataTableName), tableName)
	require.NoError(t, err)

	r.rw.Lock()
	delete(r.colls[dbName], collectionName)
	r.rw.Unlock()

	require.Nil(t, r.CollectionGet(ctx, dbName, collectionName), "collection must look absent after orphaning")

	// Re-creating it (as an upsert's CreateCollection does) must SUCCEED by
	// adopting the orphaned table, not error with "table already exists".
	created, err = r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err, "collectionCreate must adopt an orphaned physical table, not fail on it")
	require.True(t, created)

	c = r.CollectionGet(ctx, dbName, collectionName)
	require.NotNil(t, c, "collection must be registered again")
	require.Equal(t, tableName, c.TableName, "must re-adopt the SAME deterministic table")

	// And the adopted collection must be usable (insert + read back).
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %q (%s) VALUES (?)", tableName, DefaultColumn), `{"_id":"x"}`)
	require.NoError(t, err)
}

// The capped oplog gets an expression index on its Timestamp value, and SQLite's
// planner USES it for the tail's {ts: {$gt: ?}} range — so an idle tail is an index
// range scan, not a full scan of the whole capped collection on every awaitData poll.
func TestOplogTsIndexUsedForTailRange(t *testing.T) {
	t.Parallel()
	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	r, err := NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)

	// The oplog ts index is created only for the capped local.oplog.rs.
	db, err := r.DatabaseGetOrCreate(ctx, "local")
	require.NoError(t, err)
	require.NotNil(t, db)

	created, err := r.CollectionCreate(ctx, &CollectionCreateParams{
		DBName: "local", Name: "oplog.rs", CappedSize: 16 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.True(t, created)

	c := r.CollectionGet(ctx, "local", "oplog.rs")
	require.NotNil(t, c)
	tableName := c.TableName
	indexName := tableName + "_ts"

	// The ts expression index exists on the oplog table.
	var idxCount int
	err = db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?`,
		tableName, indexName,
	).Scan(&idxCount)
	require.NoError(t, err)
	require.Equal(t, 1, idxCount, "the oplog ts expression index must be created")

	// The planner uses it for the range predicate query.go emits for {ts: {$gt: ?}}.
	q := fmt.Sprintf(
		`EXPLAIN QUERY PLAN SELECT %s FROM %q WHERE %s->>"ts" > ?`,
		DefaultColumn, tableName, DefaultColumn,
	)
	rows, err := db.QueryContext(ctx, q, 5)
	require.NoError(t, err)
	defer rows.Close()

	var usesIndex bool
	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan += detail + "\n"
		if strings.Contains(detail, indexName) {
			usesIndex = true
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, usesIndex, "the planner must use the oplog ts index for the tail range; plan was:\n%s", plan)
}

// A `{field: {$in: [id, null]}}` filter (a board's card scope when no subtasks
// default board is set) pushes down as `(expr IN (?) OR expr IS NULL OR array-arm)`,
// and SQLite's planner uses the field's expression index for it (an OR-union of
// index lookups) — so such a query is an index search, not a full-table scan that
// pins CPU on every poll. Regression guard for the 10.22 "lists load but cards never
// do" fix.
func TestInWithNullUsesIndex(t *testing.T) {
	t.Parallel()
	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	r, err := NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)

	dbName := testutil.DatabaseName(t)
	db, err := r.DatabaseGetOrCreate(ctx, dbName)
	require.NoError(t, err)

	collectionName := testutil.CollectionName(t)
	_, err = r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err)

	// The Mongo-level index the client declares on boardId (built as an expression index
	// on _ferretdb_sjson->"boardId", the same expression the WHERE references).
	require.NoError(t, r.indexesCreate(ctx, dbName, collectionName, []IndexInfo{{
		Name: "boardId",
		Key:  []IndexKeyPair{{Field: "boardId"}},
	}}))

	c := r.CollectionGet(ctx, dbName, collectionName)
	require.NotNil(t, c)
	tableName := c.TableName

	// The exact WHERE inCondition emits for {boardId: {$in: [id, null]}} (see
	// query_test.go InWithNullPushed): all three arms reference the indexed expression.
	e := fmt.Sprintf(`%s->"boardId"`, DefaultColumn)
	where := fmt.Sprintf(`(%[1]s IN (?) OR %[1]s IS NULL OR (%[1]s >= '[' AND %[1]s < '\'))`, e)
	q := fmt.Sprintf(`EXPLAIN QUERY PLAN SELECT %s FROM %q WHERE %s`, DefaultColumn, tableName, where)

	rows, err := db.QueryContext(ctx, q, `"B"`)
	require.NoError(t, err)
	defer rows.Close()

	var usesIndex bool
	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan += detail + "\n"
		if strings.Contains(detail, "USING INDEX") || strings.Contains(detail, "_boardId") {
			usesIndex = true
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, usesIndex, "the $in-with-null WHERE must use the boardId index, not SCAN; plan was:\n%s", plan)
	require.NotContains(t, plan, "SCAN", "must not full-scan the table; plan was:\n%s", plan)
}

// A dotted-path equality (e.g. a Meteor-Files attachment lookup `{'meta.cardId': X}`)
// pushes down as the NESTED expression `col->"meta"->"cardId"`, which matches the
// nested expression index the registry builds for a dotted index key — so SQLite
// serves it as an index search, not a full-table scan on every poll (the card-open
// 42s / idle-CPU fix).
func TestDottedPathEqualityUsesIndex(t *testing.T) {
	t.Parallel()
	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	r, err := NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)

	dbName := testutil.DatabaseName(t)
	db, err := r.DatabaseGetOrCreate(ctx, dbName)
	require.NoError(t, err)

	collectionName := testutil.CollectionName(t)
	_, err = r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
	require.NoError(t, err)

	// The nested index the client declares on the denormalized attachment key.
	require.NoError(t, r.indexesCreate(ctx, dbName, collectionName, []IndexInfo{{
		Name: "meta_cardId",
		Key:  []IndexKeyPair{{Field: "meta.cardId"}},
	}}))

	c := r.CollectionGet(ctx, dbName, collectionName)
	require.NotNil(t, c)
	tableName := c.TableName

	// The exact WHERE prepareWhereClause emits for {'meta.cardId': X} (see
	// query_test.go DottedPathEqualityPushed): the nested -> expression + array arm.
	e := fmt.Sprintf(`%s->"meta"->"cardId"`, DefaultColumn)
	where := fmt.Sprintf(`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`, e)
	q := fmt.Sprintf(`EXPLAIN QUERY PLAN SELECT %s FROM %q WHERE %s`, DefaultColumn, tableName, where)

	rows, err := db.QueryContext(ctx, q, `"C"`)
	require.NoError(t, err)
	defer rows.Close()

	var usesIndex bool
	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plan += detail + "\n"
		if strings.Contains(detail, "USING INDEX") || strings.Contains(detail, "meta_cardId") {
			usesIndex = true
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, usesIndex, "the dotted-path WHERE must use the nested meta.cardId index; plan was:\n%s", plan)
	require.NotContains(t, plan, "SCAN", "must not full-scan; plan was:\n%s", plan)
}

func TestCreateDropStress(t *testing.T) {
	// Otherwise, the test might fail with "database schema has changed".
	// That error code is SQLITE_SCHEMA (17).
	// See https://www.sqlite.org/rescode.html#schema and https://www.sqlite.org/compile.html#max_schema_retry
	const n = 50

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	for testName, params := range map[string]string{
		"dir":              "",
		"dir-immediate":    "?_txlock=immediate",
		"memory":           "?mode=memory",
		"memory-immediate": "?mode=memory&_txlock=immediate",
	} {
		t.Run(testName, func(t *testing.T) {
			uri := testutil.TestSQLiteURI(t, "") + params

			var r *Registry
			r, err = NewRegistry(uri, 100, testutil.Logger(t), sp)
			require.NoError(t, err)
			t.Cleanup(r.Close)

			dbName := "db"

			db, err := r.DatabaseGetOrCreate(ctx, dbName)
			require.NoError(t, err)
			require.NotNil(t, db)

			var i atomic.Int32

			teststress.StressN(t, n, func(ready chan<- struct{}, start <-chan struct{}) {
				collectionName := fmt.Sprintf("collection_%03d", i.Add(1))

				ready <- struct{}{}
				<-start

				testCollection(t, ctx, r, db, dbName, collectionName)
			})

			require.Equal(t, int32(n), i.Load())
		})
	}
}

func TestCreateSameStress(t *testing.T) {
	// Otherwise, the test might fail with "database schema has changed".
	// That error code is SQLITE_SCHEMA (17).
	// See https://www.sqlite.org/rescode.html#schema and https://www.sqlite.org/compile.html#max_schema_retry
	const n = 50

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	for testName, params := range map[string]string{
		"dir":              "",
		"dir-immediate":    "?_txlock=immediate",
		"memory":           "?mode=memory",
		"memory-immediate": "?mode=memory&_txlock=immediate",
	} {
		t.Run(testName, func(t *testing.T) {
			uri := testutil.TestSQLiteURI(t, "") + params

			var r *Registry
			r, err = NewRegistry(uri, 100, testutil.Logger(t), sp)
			require.NoError(t, err)
			t.Cleanup(r.Close)

			dbName := "db"

			db, err := r.DatabaseGetOrCreate(ctx, dbName)
			require.NoError(t, err)
			require.NotNil(t, db)

			collectionName := "collection"

			var i, createdTotal atomic.Int32

			teststress.StressN(t, n, func(ready chan<- struct{}, start <-chan struct{}) {
				id := i.Add(1)

				ready <- struct{}{}
				<-start

				created, err := r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
				require.NoError(t, err)
				if created {
					createdTotal.Add(1)
				}

				created, err = r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
				require.NoError(t, err)
				require.False(t, created)

				c := r.CollectionGet(ctx, dbName, collectionName)
				require.NotNil(t, c)
				require.Equal(t, collectionName, c.Name)

				list, err := r.CollectionList(ctx, dbName)
				require.NoError(t, err)
				require.Contains(t, list, c)

				q := fmt.Sprintf("INSERT INTO %q (%s) VALUES(?)", c.TableName, DefaultColumn)
				doc := fmt.Sprintf(`{"$s": {"p": {"_id": {"t": "int"}}, "$k": ["_id"]}, "_id": %d}`, id)
				_, err = db.ExecContext(ctx, q, doc)
				require.NoError(t, err)
			})

			require.Equal(t, int32(n), i.Load())
			require.Equal(t, int32(1), createdTotal.Load())
		})
	}
}

func TestDropSameStress(t *testing.T) {
	// Otherwise, the test might fail with "database schema has changed".
	// That error code is SQLITE_SCHEMA (17).
	// See https://www.sqlite.org/rescode.html#schema and https://www.sqlite.org/compile.html#max_schema_retry
	const n = 50

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	for testName, params := range map[string]string{
		"dir":              "",
		"dir-immediate":    "?_txlock=immediate",
		"memory":           "?mode=memory",
		"memory-immediate": "?mode=memory&_txlock=immediate",
	} {
		t.Run(testName, func(t *testing.T) {
			uri := testutil.TestSQLiteURI(t, "") + params

			var r *Registry
			r, err = NewRegistry(uri, 100, testutil.Logger(t), sp)
			require.NoError(t, err)
			t.Cleanup(r.Close)

			dbName := "db"

			db, err := r.DatabaseGetOrCreate(ctx, dbName)
			require.NoError(t, err)
			require.NotNil(t, db)

			collectionName := "collection"

			created, err := r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
			require.NoError(t, err)
			require.True(t, created)

			var droppedTotal atomic.Int32

			teststress.StressN(t, n, func(ready chan<- struct{}, start <-chan struct{}) {
				ready <- struct{}{}
				<-start

				dropped, err := r.CollectionDrop(ctx, dbName, collectionName)
				require.NoError(t, err)
				if dropped {
					droppedTotal.Add(1)
				}
			})

			require.Equal(t, int32(1), droppedTotal.Load())
		})
	}
}

func TestCreateDropSameStress(t *testing.T) {
	// Otherwise, the test might fail with "database schema has changed".
	// That error code is SQLITE_SCHEMA (17).
	// See https://www.sqlite.org/rescode.html#schema and https://www.sqlite.org/compile.html#max_schema_retry
	const n = 50

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	for testName, params := range map[string]string{
		"dir":              "",
		"dir-immediate":    "?_txlock=immediate",
		"memory":           "?mode=memory",
		"memory-immediate": "?mode=memory&_txlock=immediate",
	} {
		t.Run(testName, func(t *testing.T) {
			uri := testutil.TestSQLiteURI(t, "") + params

			var r *Registry
			r, err = NewRegistry(uri, 100, testutil.Logger(t), sp)
			require.NoError(t, err)
			t.Cleanup(r.Close)

			dbName := "db"

			db, err := r.DatabaseGetOrCreate(ctx, dbName)
			require.NoError(t, err)
			require.NotNil(t, db)

			collectionName := "collection"

			var i, createdTotal, droppedTotal atomic.Int32

			teststress.StressN(t, n, func(ready chan<- struct{}, start <-chan struct{}) {
				id := i.Add(1)

				ready <- struct{}{}
				<-start

				if id%2 == 0 {
					created, err := r.CollectionCreate(ctx, &CollectionCreateParams{DBName: dbName, Name: collectionName})
					require.NoError(t, err)
					if created {
						createdTotal.Add(1)
					}
				} else {
					dropped, err := r.CollectionDrop(ctx, dbName, collectionName)
					require.NoError(t, err)
					if dropped {
						droppedTotal.Add(1)
					}
				}
			})

			require.Equal(t, int32(n), i.Load())
			require.Less(t, int32(1), createdTotal.Load())
			require.Less(t, int32(1), droppedTotal.Load())
		})
	}
}

func TestIndexesCreateDrop(t *testing.T) {
	t.Parallel()
	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	r, err := NewRegistry(testutil.TestSQLiteURI(t, ""), 100, testutil.Logger(t), sp)
	require.NoError(t, err)
	t.Cleanup(r.Close)

	dbName := testutil.DatabaseName(t)

	db, err := r.DatabaseGetOrCreate(ctx, dbName)
	require.NoError(t, err)
	require.NotNil(t, db)

	collectionName := testutil.CollectionName(t)

	toCreate := []IndexInfo{{
		Name: "index_non_unique",
		Key: []IndexKeyPair{{
			Field:      "f1",
			Descending: false,
		}, {
			Field:      "f2",
			Descending: true,
		}},
	}, {
		Name: "index_unique",
		Key: []IndexKeyPair{{
			Field:      "foo",
			Descending: false,
		}},
		Unique: true,
	}, {
		Name: "nested_index",
		Key: []IndexKeyPair{{
			Field:      "foo.bar.baz",
			Descending: false,
		}},
	}}

	err = r.IndexesCreate(ctx, dbName, collectionName, toCreate)
	require.NoError(t, err)

	collection := r.CollectionGet(ctx, dbName, collectionName)

	t.Run("NonUniqueIndex", func(t *testing.T) {
		indexName := collection.TableName + "_index_non_unique"
		q := fmt.Sprintf("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = '%s'", indexName)
		row := db.QueryRowContext(ctx, q)

		var sql string
		require.NoError(t, row.Scan(&sql))

		expected := fmt.Sprintf(
			`CREATE INDEX "%s" ON "%s" (_ferretdb_sjson->"f1", _ferretdb_sjson->"f2" DESC)`,
			indexName, collection.TableName,
		)
		require.Equal(t, expected, sql)
	})

	t.Run("UniqueIndex", func(t *testing.T) {
		indexName := collection.TableName + "_index_unique"
		q := fmt.Sprintf("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = '%s'", indexName)
		row := db.QueryRowContext(ctx, q)

		var sql string
		require.NoError(t, row.Scan(&sql))

		expected := fmt.Sprintf(
			`CREATE UNIQUE INDEX "%s" ON "%s" (_ferretdb_sjson->"foo")`,
			indexName, collection.TableName,
		)
		require.Equal(t, expected, sql)
	})

	t.Run("DefaultIndex", func(t *testing.T) {
		indexName := collection.TableName + "__id_"
		q := "SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?"
		row := db.QueryRowContext(ctx, q, indexName)

		var sql string
		require.NoError(t, row.Scan(&sql))

		expected := fmt.Sprintf(
			`CREATE UNIQUE INDEX "%s" ON "%s" (_ferretdb_sjson->"_id")`,
			indexName, collection.TableName,
		)
		require.Equal(t, expected, sql)
	})

	t.Run("NestedIndex", func(t *testing.T) {
		indexName := collection.TableName + "_nested_index"
		q := "SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?"
		row := db.QueryRowContext(ctx, q, indexName)

		var sql string
		require.NoError(t, row.Scan(&sql))

		expected := fmt.Sprintf(
			`CREATE INDEX "%s" ON "%s" (_ferretdb_sjson->"foo"->"bar"->"baz")`,
			indexName, collection.TableName,
		)
		require.Equal(t, expected, sql)
	})

	t.Run("CheckSettingsAfterCreation", func(t *testing.T) {
		err = r.initCollections(ctx, dbName, db)
		require.NoError(t, err)

		collection = r.CollectionGet(ctx, dbName, collectionName)
		require.Equal(t, 4, len(collection.Settings.Indexes))
	})

	t.Run("DropIndexes", func(t *testing.T) {
		toDrop := []string{"index_non_unique", "index_unique", "nested_index"}
		err = r.IndexesDrop(ctx, dbName, collectionName, toDrop)
		require.NoError(t, err)

		q := "SELECT count(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ?"
		row := db.QueryRowContext(ctx, q, collection.TableName)

		var count int
		require.NoError(t, row.Scan(&count))
		require.Equal(t, 1, count) // only default index
	})

	t.Run("CheckSettingsAfterDrop", func(t *testing.T) {
		err = r.initCollections(ctx, dbName, db)
		require.NoError(t, err)

		collection = r.CollectionGet(ctx, dbName, collectionName)
		require.Equal(t, 1, len(collection.Settings.Indexes))
	})
}
