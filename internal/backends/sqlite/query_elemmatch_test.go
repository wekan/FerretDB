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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// elemMatchOnTheColumn: the path form of json_each and json_type must find exactly
// the rows the text form (->) found, on the documents that make it difficult - a
// string in the array beside the objects, a field that is not an array, one that is
// missing, a JSON null - and on a field name that cannot go into a path.
func TestElemMatchReadsTheColumnAndFindsTheSameRows(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`CREATE TABLE t (` + metadata.DefaultColumn + ` BLOB)`)
	require.NoError(t, err)

	docs := []string{
		`{"_id":"a","members":[{"userId":"u1","isActive":true}]}`,
		`{"_id":"b","members":[{"userId":"u1","isActive":false}]}`,
		`{"_id":"c","members":["u1",{"userId":"u1","isActive":true}]}`,   // a string beside the objects
		`{"_id":"d","members":[{"userId":["u1","u2"],"isActive":true}]}`, // an array value
		`{"_id":"e","members":{"userId":"u1","isActive":true}}`,          // not an array
		`{"_id":"f"}`,                // missing
		`{"_id":"g","members":null}`, // JSON null
		`{"_id":"h","members":[]}`,   // empty
		`{"_id":"i","members":[{"userId":"u2","isActive":true}]}`,               // another user
		`{"_id":"j","a-b":[{"userId":"u1","isActive":true}],"members":[1,2,3]}`, // a dash in a name
	}
	for _, d := range docs {
		_, err = db.Exec(`INSERT INTO t VALUES (jsonb(?))`, d)
		require.NoError(t, err)
	}

	ids := func(where string, args []any) []string {
		rows, err := db.Query(`SELECT json_extract(`+metadata.DefaultColumn+`, '$._id') FROM t WHERE `+where+` ORDER BY rowid`, args...)
		require.NoError(t, err)
		defer rows.Close()

		var out []string
		for rows.Next() {
			var id string
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}
		require.NoError(t, rows.Err())

		return out
	}

	for _, field := range []string{"members", "a-b"} {
		filter := must.NotFail(types.NewDocument(field, must.NotFail(types.NewDocument("$elemMatch", must.NotFail(types.NewDocument("userId", "u1", "isActive", true))))))
		where, args := prepareWhereClause(filter)
		require.NotEmpty(t, where)
		where = strings.TrimPrefix(where, " WHERE ")

		// The old form: the array as the text of ->.
		oldWhere := strings.NewReplacer(
			fmt.Sprintf(`json_type(%s, '$."%s"')`, metadata.DefaultColumn, field), fmt.Sprintf(`json_type(%s->%q)`, metadata.DefaultColumn, field),
			fmt.Sprintf(`json_each(%s, '$."%s"')`, metadata.DefaultColumn, field), fmt.Sprintf(`json_each(%s->%q)`, metadata.DefaultColumn, field),
		).Replace(where)
		require.NotEqual(t, where, oldWhere, "the new form is in the clause")

		assert.Equal(t, ids(oldWhere, args), ids(where, args), field)
		assert.NotEmpty(t, ids(where, args), field)
	}

	// negative: a name that does not go into a path unquoted keeps the text form.
	odd := must.NotFail(types.NewDocument(`we"ird`, must.NotFail(types.NewDocument("$elemMatch", must.NotFail(types.NewDocument("userId", "u1"))))))
	where, _ := prepareWhereClause(odd)
	assert.NotContains(t, where, `'$."`, "no path is built from a name with a quote in it")
}
