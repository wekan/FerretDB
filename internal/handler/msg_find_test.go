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

package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite"
	"github.com/FerretDB/FerretDB/internal/clientconn/conninfo"
	"github.com/FerretDB/FerretDB/internal/clientconn/cursor"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// TestFindTailableKeepsCursorOpenOnEmptyCapped is an integration test for the #6480 fix:
// a tailable+awaitData find on an EMPTY capped collection must keep the cursor OPEN
// (return a non-zero cursor id) so the client resumes waiting with getMore, instead of
// closing it and returning id 0 — which forced a client tailing an otherwise-idle capped
// collection (e.g. a Meteor 3 driver tailing local.oplog.rs) to re-issue find, and a
// fresh collection scan, continuously. A NORMAL find on the same empty collection must
// still be exhausted (id 0), so the change stays scoped to tailable cursors.
func TestFindTailableKeepsCursorOpenOnEmptyCapped(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := sqlite.NewBackend(&sqlite.NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	h := &Handler{
		NewOpts: &NewOpts{ReplSetName: "rs0", L: testutil.Logger(t)},
		b:       b,
		cursors: cursor.NewRegistry(testutil.Logger(t)),
	}
	t.Cleanup(h.cursors.Close)

	h.ensureOplog() // creates the capped local.oplog.rs

	connCtx := conninfo.Ctx(ctx, conninfo.New())

	// find runs a find command against local.oplog.rs and returns (cursor id, firstBatch length).
	find := func(extra ...any) (int64, int) {
		fields := append([]any{"find", "oplog.rs", "filter", must.NotFail(types.NewDocument())}, extra...)
		fields = append(fields, "$db", "local")

		resp, err := h.MsgFind(connCtx, must.NotFail(documentOpMsg(must.NotFail(types.NewDocument(fields...)))))
		require.NoError(t, err)

		respDoc, err := opMsgDocument(resp)
		require.NoError(t, err)

		curSor := must.NotFail(respDoc.Get("cursor")).(*types.Document)
		id := must.NotFail(curSor.Get("id")).(int64)
		firstBatch := must.NotFail(curSor.Get("firstBatch")).(*types.Array)

		return id, firstBatch.Len()
	}

	t.Run("tailable stays open", func(t *testing.T) {
		id, batch := find("tailable", true, "awaitData", true)
		assert.Equal(t, 0, batch, "an idle tail returns an empty first batch")
		assert.NotZero(t, id, "tailable find on an empty capped collection must keep the cursor open (non-zero id)")

		// The cursor is intentionally still registered (that is the fix): assert it is
		// there, then close it so the registry's Close — which waits for every cursor to
		// be removed — does not block at cleanup.
		c := h.cursors.Get(id)
		require.NotNil(t, c, "the tailable cursor must still be registered (kept open)")
		h.cursors.CloseAndRemove(c)
	})

	t.Run("normal find is still exhausted", func(t *testing.T) {
		id, batch := find()
		assert.Equal(t, 0, batch, "empty collection returns an empty batch")
		assert.Zero(t, id, "a normal, exhausted find must still return id 0")
	})
}
