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
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// storedRows returns rowid -> the stored document as text, and the column's type.
func storedRows(t *testing.T, raw *sql.DB, table string) (map[int64]string, string) {
	t.Helper()

	var typ string
	require.NoError(t, raw.QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name = ?`, table, metadata.DefaultColumn).Scan(&typ))

	rows, err := raw.Query(`SELECT rowid, json(` + metadata.DefaultColumn + `) FROM "` + table + `"`)
	require.NoError(t, err)

	defer rows.Close()

	res := map[int64]string{}

	for rows.Next() {
		var id int64
		var doc string
		require.NoError(t, rows.Scan(&id, &doc))
		res[id] = doc
	}

	require.NoError(t, rows.Err())

	return res, typ
}

// toText turns the table back into what an older version created: sjson TEXT
// in a STRICT TEXT column, the same rowids, indexes and triggers, and no
// storage in the settings.
func toText(t *testing.T, raw *sql.DB, table string) {
	t.Helper()

	var schema []string

	rows, err := raw.Query(`SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY type, name`, table)
	require.NoError(t, err)

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		schema = append(schema, s)
	}

	require.NoError(t, rows.Close())

	for _, q := range append([]string{
		`CREATE TABLE old_text (` + metadata.DefaultColumn + ` TEXT NOT NULL CHECK(` + metadata.DefaultColumn + ` != '')) STRICT`,
		`INSERT INTO old_text (rowid, ` + metadata.DefaultColumn + `) SELECT rowid, json(` + metadata.DefaultColumn + `) FROM "` + table + `"`,
		`DROP TABLE "` + table + `"`,
		`ALTER TABLE old_text RENAME TO "` + table + `"`,
		`UPDATE _ferretdb_collections SET settings = json_remove(settings, '$.storage')`,
	}, schema...) {
		_, err = raw.Exec(q)
		require.NoError(t, err, q)
	}
}

func TestStorageConvertsTextTables(t *testing.T) {
	t.Parallel()

	uri := testutil.TestSQLiteURI(t, "")
	dbName := testutil.DatabaseName(t)

	e := newMultikeyEnv(t, uri, dbName)
	_, err := e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: multikeyDocuments()})
	require.NoError(t, err)
	_, err = e.coll.CreateIndexes(e.ctx, &backends.CreateIndexesParams{Indexes: []backends.IndexInfo{
		{Name: "members.userId_1", Key: []backends.IndexKeyPair{{Field: "members.userId"}}},
		{Name: "permission_1", Key: []backends.IndexKeyPair{{Field: "permission"}}},
	}})
	require.NoError(t, err)
	e.b.Close()

	raw := e.raw(t, "rw")

	var table string
	require.NoError(t, raw.QueryRow(`SELECT table_name FROM _ferretdb_collections WHERE name = 'boards'`).Scan(&table))

	jsonbRows, typ := storedRows(t, raw, table)
	require.Equal(t, "BLOB", typ, "a new collection is JSONB")

	// A database an older version wrote.
	toText(t, raw, table)
	textRows, typ := storedRows(t, raw, table)
	require.Equal(t, "TEXT", typ)
	require.Equal(t, jsonbRows, textRows)

	var stored string
	require.NoError(t, raw.QueryRow(`SELECT typeof(`+metadata.DefaultColumn+`) FROM "`+table+`" LIMIT 1`).Scan(&stored))
	require.Equal(t, "text", stored)
	require.NoError(t, raw.Close())

	// Opening it converts it: the same rows, at the same rowids, as JSONB.
	e = newMultikeyEnv(t, uri, dbName)
	t.Cleanup(e.b.Close)

	raw = e.raw(t, "ro")
	defer raw.Close()

	convertedRows, typ := storedRows(t, raw, table)
	assert.Equal(t, "BLOB", typ)
	assert.Equal(t, textRows, convertedRows, "every document at its own rowid")
	require.NoError(t, raw.QueryRow(`SELECT typeof(`+metadata.DefaultColumn+`) FROM "`+table+`" LIMIT 1`).Scan(&stored))
	assert.Equal(t, "blob", stored)

	var indexes, triggers int
	require.NoError(t, raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE tbl_name = ? AND type = 'index' AND sql IS NOT NULL`, table).Scan(&indexes))
	require.NoError(t, raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE tbl_name = ? AND type = 'trigger'`, table).Scan(&triggers))
	assert.Equal(t, 3, indexes, "_id, members.userId and permission")
	assert.Equal(t, 4, triggers, "the multikey side table's")

	var settings string
	require.NoError(t, raw.QueryRow(`SELECT settings->>'storage' FROM _ferretdb_collections WHERE name = 'boards'`).Scan(&settings))
	assert.Equal(t, metadata.StorageJSONB, settings)

	// Queries answer as before - through the side table, so its triggers came
	// back too: a new member is found.
	_, matched := e.candidates(t, elemMatchUser("u1"))
	assert.Equal(t, []string{"field-array", "member", "second"}, matched)

	_, err = e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: []*types.Document{
		mkDoc("_id", "new", "members", mkArr(mkDoc("userId", "u1", "isActive", true))),
	}})
	require.NoError(t, err)
	_, matched = e.candidates(t, elemMatchUser("u1"))
	assert.Equal(t, []string{"field-array", "member", "new", "second"}, matched)
}

func TestStorageConversionFailureKeepsText(t *testing.T) {
	// Negative: a table whose conversion fails stays exactly as it was, as text,
	// and is still written as text. The failure here is the last step: a view
	// naming a column the new table does not have makes the rename refuse.
	t.Parallel()

	uri := testutil.TestSQLiteURI(t, "")
	dbName := testutil.DatabaseName(t)

	e := newMultikeyEnv(t, uri, dbName)
	_, err := e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: multikeyDocuments()[:2]})
	require.NoError(t, err)
	e.b.Close()

	raw := e.raw(t, "rw")

	var table string
	require.NoError(t, raw.QueryRow(`SELECT table_name FROM _ferretdb_collections WHERE name = 'boards'`).Scan(&table))
	toText(t, raw, table)

	_, err = raw.Exec(`PRAGMA legacy_alter_table = OFF`)
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE VIEW conversion_blocker AS SELECT no_such_column FROM "` + table + `"`)
	if err != nil {
		t.Skipf("SQLite checks the view when it is created: %v", err)
	}
	before, _ := storedRows2(t, raw, table)
	require.NoError(t, raw.Close())

	e = newMultikeyEnv(t, uri, dbName)
	t.Cleanup(e.b.Close)

	raw = e.raw(t, "ro")
	defer raw.Close()

	after, typ := storedRows2(t, raw, table)
	assert.Equal(t, "TEXT", typ)
	assert.Equal(t, before, after, "nothing changed")

	// Writes still go in as text.
	require.NoError(t, raw.Close())
	_, err = e.coll.InsertAll(e.ctx, &backends.InsertAllParams{Docs: multikeyDocuments()[2:3]})
	require.NoError(t, err)

	raw = e.raw(t, "ro")
	var count int
	require.NoError(t, raw.QueryRow(`SELECT COUNT(*) FROM "`+table+`" WHERE typeof(`+metadata.DefaultColumn+`) = 'text'`).Scan(&count))
	assert.Equal(t, 3, count)
}

// storedRows2 is storedRows without json(), for a table holding text json()
// refuses.
func storedRows2(t *testing.T, raw *sql.DB, table string) (map[int64]string, string) {
	t.Helper()

	var typ string
	require.NoError(t, raw.QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name = ?`, table, metadata.DefaultColumn).Scan(&typ))

	rows, err := raw.Query(`SELECT rowid, ` + metadata.DefaultColumn + ` FROM "` + table + `"`)
	require.NoError(t, err)

	defer rows.Close()

	res := map[int64]string{}

	for rows.Next() {
		var id int64
		var doc string
		require.NoError(t, rows.Scan(&id, &doc))
		res[id] = doc
	}

	return res, typ
}
