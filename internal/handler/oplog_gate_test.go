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
	"github.com/FerretDB/FerretDB/internal/backends/sqlite"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// Recording a mutation into the OpLog and creating the OpLog used to be decided
// by two different questions, and after a reconfiguration they disagreed.
//
// The decorator asks only whether `local.oplog.rs` exists. Creating it asks
// whether a replica-set name is configured - ensureOplog() returns early without
// one, and `replSetInitiate` calls that same function, so a server with no
// replica-set name can never create the collection itself.
//
// Start once WITH a replica-set name and the collection appears; take the name
// away and restart, and every insert, update and delete went on being copied
// into it, for as long as the deployment lived. Nothing could read those copies:
// without a replica-set name `hello` advertises no replica set, so a driver's
// OpLog tailing cannot connect and falls back to poll-and-diff. It also cost a
// ListCollections on `local` per mutation.
//
// Reported on the SQLite backend: 3277 documents and 9 MiB of OpLog inside a
// 22 MiB local database, growing by about ten documents a minute, with no reader.
//
// These tests pin both directions, through New() rather than by constructing a
// Handler directly, because it is New() that decides whether to install the
// decorator at all.

// newTestBackendWithOplog returns a SQLite backend that ALREADY has a capped
// local.oplog.rs, which is the state a previously-replicated deployment is left
// in, and the only state in which this bug is reachable.
func newTestBackendWithOplog(t *testing.T) backends.Backend {
	t.Helper()

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := sqlite.NewBackend(&sqlite.NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	// Created the way the server creates it, so the fixture cannot drift.
	h := &Handler{NewOpts: &NewOpts{ReplSetName: "rs0", L: testutil.Logger(t)}, b: b}
	h.ensureOplog()

	return b
}

// oplogLen counts the documents currently in local.oplog.rs.
func oplogLen(t *testing.T, b backends.Backend) int {
	t.Helper()

	ctx := testutil.Ctx(t)

	db, err := b.Database("local")
	require.NoError(t, err)

	c, err := db.Collection("oplog.rs")
	require.NoError(t, err)

	res, err := c.Query(ctx, new(backends.QueryParams))
	require.NoError(t, err)

	defer res.Iter.Close()

	var n int

	for {
		if _, _, err = res.Iter.Next(); err != nil {
			break
		}

		n++
	}

	return n
}

// insertOne writes one document through the handler's backend, which is the
// decorated one when the decorator is installed.
func insertOne(t *testing.T, h *Handler, dbName, cName string) {
	t.Helper()

	ctx := testutil.Ctx(t)

	db, err := h.b.Database(dbName)
	require.NoError(t, err)

	require.NoError(t, db.CreateCollection(ctx, &backends.CreateCollectionParams{Name: cName}))

	c, err := db.Collection(cName)
	require.NoError(t, err)

	doc := must.NotFail(types.NewDocument("_id", types.NewObjectID()))

	_, err = c.InsertAll(ctx, &backends.InsertAllParams{Docs: []*types.Document{doc}})
	require.NoError(t, err)
}

func TestOpLogNotWrittenWithoutReplSetName(t *testing.T) {
	t.Parallel()

	b := newTestBackendWithOplog(t)
	before := oplogLen(t, b)

	h, err := New(&NewOpts{Backend: b, ReplSetName: "", L: testutil.Logger(t)})
	require.NoError(t, err)

	insertOne(t, h, "testdb", "testcoll")

	assert.Equal(t, before, oplogLen(t, b),
		"with no replica-set name nothing can read the OpLog - `hello` advertises no "+
			"replica set - so a leftover local.oplog.rs must stop being written to")
}

func TestOpLogWrittenWithReplSetName(t *testing.T) {
	t.Parallel()

	b := newTestBackendWithOplog(t)
	before := oplogLen(t, b)

	h, err := New(&NewOpts{Backend: b, ReplSetName: "rs0", L: testutil.Logger(t)})
	require.NoError(t, err)
	notifier, ok := h.b.(interface{ Notifications() <-chan struct{} })
	require.True(t, ok)
	changed := notifier.Notifications()

	insertOne(t, h, "testdb", "testcoll")
	select {
	case <-changed:
	default:
		assert.Fail(t, "successful OpLog append did not wake awaitData listeners")
	}

	assert.Greater(t, oplogLen(t, b), before,
		"the gate must not have turned OpLog recording off for the deployments that "+
			"do use it - this is what a driver tails instead of poll-and-diff")
}

func TestOpLogLeftOnDiskIsNotDeleted(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)
	b := newTestBackendWithOplog(t)

	_, err := New(&NewOpts{Backend: b, ReplSetName: "", L: testutil.Logger(t)})
	require.NoError(t, err)

	db, err := b.Database("local")
	require.NoError(t, err)

	cList, err := db.ListCollections(ctx, &backends.ListCollectionsParams{Name: "oplog.rs"})
	require.NoError(t, err)

	// Stopping the writes is this server's decision to make; deleting somebody's
	// collection is not, and a name put back later must find it where it was.
	require.Len(t, cList.Collections, 1, "the collection stays, it just goes quiet")
	assert.True(t, cList.Collections[0].Capped())
}
