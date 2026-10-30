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

// A distinct whose filter or key names a field some documents do not have - a
// collection written by an older client version - failed with
// "sjson.UnmarshalFields: missing schema for key", because the pushdown wrote
// the absent field as "key":null in the schema. The rows the real SQL returns
// must decode, with the absent field missing and an explicit null kept.
func TestDistinctPushdownAbsentField(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file::memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.Exec(fmt.Sprintf(`CREATE TABLE "lists" (%s TEXT)`, metadata.DefaultColumn))
	require.NoError(t, err)

	for _, d := range []*types.Document{
		// written by an old client: no swimlaneId, no type
		must.NotFail(types.NewDocument("_id", "old", "boardId", "b1")),
		// an explicit null is a value, not an absent field
		must.NotFail(types.NewDocument("_id", "null", "boardId", "b2", "swimlaneId", types.Null, "type", "list")),
		must.NotFail(types.NewDocument("_id", "new", "boardId", "b3", "swimlaneId", "s1", "type", "list")),
	} {
		b, err := sjson.Marshal(d)
		require.NoError(t, err)
		_, err = db.Exec(fmt.Sprintf(`INSERT INTO "lists" (%s) VALUES (jsonb(?))`, metadata.DefaultColumn), string(b))
		require.NoError(t, err)
	}

	fields := []string{"boardId", "swimlaneId", "type"}
	rows, err := db.Query(prepareDistinctSelectClause("lists", "", fields, ""))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rows.Close()) })

	got := map[string]*types.Document{}
	for rows.Next() {
		var b []byte
		require.NoError(t, rows.Scan(&b))

		b, err = storedJSON(b)
		require.NoError(t, err)

		for name, decode := range map[string]func([]byte) (*types.Document, error){
			"UnmarshalFields": func(b []byte) (*types.Document, error) { return sjson.UnmarshalFields(b, fields) },
			"Unmarshal":       sjson.Unmarshal,
		} {
			doc, err := decode(b)
			require.NoError(t, err, "%s of %s", name, b)
			got[must.NotFail(doc.Get("boardId")).(string)+" "+name] = doc
		}
	}
	require.NoError(t, rows.Err())

	for _, name := range []string{"UnmarshalFields", "Unmarshal"} {
		old := got["b1 "+name]
		require.NotNil(t, old, name)
		assert.False(t, old.Has("swimlaneId"), "%s: an absent field stays absent", name)
		assert.False(t, old.Has("type"), name)

		explicit := got["b2 "+name]
		require.NotNil(t, explicit, name)
		assert.Equal(t, types.Null, must.NotFail(explicit.Get("swimlaneId")), "%s: an explicit null is kept", name)

		assert.Equal(t, "s1", must.NotFail(got["b3 "+name].Get("swimlaneId")), name)
	}
}
