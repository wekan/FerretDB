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

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// Dropping a collection or a database is recorded in the OpLog as MongoDB
// records it - op "c" on "<db>.$cmd" with {drop: <name>} or {dropDatabase: 1}.
// Before, only document writes were recorded, so a client replaying the OpLog
// (a point-in-time backup, a mirror) kept every document of a collection that
// had been dropped.

// oplogCommands returns the ns and o of every "c" record in local.oplog.rs.
func oplogCommands(t *testing.T, b backends.Backend) [][2]any {
	t.Helper()

	ctx := testutil.Ctx(t)

	c, err := must.NotFail(b.Database("local")).Collection("oplog.rs")
	require.NoError(t, err)

	res, err := c.Query(ctx, new(backends.QueryParams))
	require.NoError(t, err)

	defer res.Iter.Close()

	var out [][2]any

	for {
		_, doc, err := res.Iter.Next()
		if err != nil {
			break
		}

		if must.NotFail(doc.Get("op")) != "c" {
			continue
		}

		o := must.NotFail(doc.Get("o")).(*types.Document)
		out = append(out, [2]any{must.NotFail(doc.Get("ns")), o.Keys()[0] + "=" + toString(must.NotFail(o.Get(o.Keys()[0])))})
	}

	return out
}

func toString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case int32:
		if v == 1 {
			return "1"
		}
	}

	return "?"
}

func TestOpLogRecordsDrops(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	b := newTestBackendWithOplog(t)

	h, err := New(&NewOpts{Backend: b, ReplSetName: "rs0", L: testutil.Logger(t)})
	require.NoError(t, err)

	insertOne(t, h, "testdb", "gone")
	insertOne(t, h, "testdb", "kept")

	notifier := h.b.(interface{ Notifications() <-chan struct{} })
	changed := notifier.Notifications()

	db, err := h.b.Database("testdb")
	require.NoError(t, err)
	require.NoError(t, db.DropCollection(ctx, &backends.DropCollectionParams{Name: "gone"}))

	select {
	case <-changed:
	default:
		assert.Fail(t, "a recorded drop did not wake awaitData listeners")
	}

	require.NoError(t, h.b.DropDatabase(ctx, &backends.DropDatabaseParams{Name: "testdb"}))

	assert.Equal(t, [][2]any{{"testdb.$cmd", "drop=gone"}, {"testdb.$cmd", "dropDatabase=1"}}, oplogCommands(t, b))
}

func TestOpLogRecordsNoFailedOrUnreadableDrops(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	b := newTestBackendWithOplog(t)

	// No replica-set name: no decorator, nothing recorded (TestOpLogNotWrittenWithoutReplSetName).
	plain, err := New(&NewOpts{Backend: b, ReplSetName: "", L: testutil.Logger(t)})
	require.NoError(t, err)
	insertOne(t, plain, "plaindb", "c")
	require.NoError(t, plain.b.DropDatabase(ctx, &backends.DropDatabaseParams{Name: "plaindb"}))

	h, err := New(&NewOpts{Backend: b, ReplSetName: "rs0", L: testutil.Logger(t)})
	require.NoError(t, err)

	// A drop that fails is not recorded.
	db, err := h.b.Database("testdb")
	require.NoError(t, err)
	assert.Error(t, db.DropCollection(ctx, &backends.DropCollectionParams{Name: "missing"}))

	// Nor is anything done to the database that holds the OpLog itself.
	insertOne(t, h, "local", "scratch")
	local, err := h.b.Database("local")
	require.NoError(t, err)
	require.NoError(t, local.DropCollection(ctx, &backends.DropCollectionParams{Name: "scratch"}))

	assert.Empty(t, oplogCommands(t, b))
}
