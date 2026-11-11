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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// A field named like one of SQLite's own columns - rowId, oid, _rowid_ - was
// read as that column: `col->"rowId"` took ROWID's integer as the path, so an
// equality filter on the field matched nothing and every document with it
// was invisible to a query by it (the client's History completion receipts,
// queried by rowId). The pushed-down WHERE and an expression index on such a
// field now read the field; any other name keeps its double-quoted form, so
// indexes already created still serve their queries (negative).
func TestFieldNamedLikeASQLiteColumn(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file::memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.Exec(fmt.Sprintf(`CREATE TABLE "receipts" (%s TEXT)`, metadata.DefaultColumn))
	require.NoError(t, err)

	for _, d := range []*types.Document{
		must.NotFail(types.NewDocument("_id", "a", "rowId", "r1", "oid", "o1", "_rowid_", "x1", "boardId", "b1")),
		must.NotFail(types.NewDocument("_id", "b", "rowId", "r2", "oid", "o2", "_rowid_", "x2", "boardId", "b2")),
	} {
		b, err := sjson.Marshal(d)
		require.NoError(t, err)
		_, err = db.Exec(fmt.Sprintf(`INSERT INTO "receipts" (%s) VALUES (jsonb(?))`, metadata.DefaultColumn), string(b))
		require.NoError(t, err)
	}

	ids := func(filter *types.Document) []string {
		where, args := prepareWhereClause(filter)
		rows, err := db.Query(`SELECT json_extract(`+metadata.DefaultColumn+`, '$._id') FROM "receipts"`+where+` ORDER BY rowid`, args...)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, rows.Close()) })
		var out []string
		for rows.Next() {
			var id string
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}
		require.NoError(t, rows.Err())
		return out
	}

	for _, field := range []string{"rowId", "oid", "_rowid_", "ROWID"} {
		value := map[string]string{"rowId": "r2", "oid": "o2", "_rowid_": "x2", "ROWID": "r2"}[field]
		key := field
		if field == "ROWID" {
			key = "rowId"
		}
		assert.Equal(t, []string{"b"}, ids(must.NotFail(types.NewDocument(key, value))), "a filter on %q", field)
	}

	// An expression index on the field indexes the field, not ROWID.
	idx := indexCreateSQLForTest("receipts", "rowId")
	_, err = db.Exec(idx)
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, ids(must.NotFail(types.NewDocument("rowId", "r2"))))

	// Negative: any other name is written as before, which the indexes on disk use.
	assert.Equal(t, `"boardId"`, metadata.JSONLabel("boardId"))
	assert.Equal(t, `'rowId'`, metadata.JSONLabel("rowId"))
	assert.Equal(t, []string{"a"}, ids(must.NotFail(types.NewDocument("boardId", "b1"))))
}

// indexCreateSQLForTest is the expression index Registry.indexesCreate writes
// for one ascending field.
func indexCreateSQLForTest(table, field string) string {
	return fmt.Sprintf(`CREATE INDEX "%s_%s" ON %q (%s->%s)`, table, field, table, metadata.DefaultColumn, metadata.JSONLabel(field))
}
