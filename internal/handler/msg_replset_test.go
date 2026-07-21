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
	"context"
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

// newReplSetTestHandler builds a minimal Handler for unit-testing the
// replica-set/OpLog compatibility surface. No backend is attached: the code
// under test (hello field-building and the replSet* guards) does not touch it.
func newReplSetTestHandler(replSet string) *Handler {
	return &Handler{NewOpts: &NewOpts{
		TCPHost:                "127.0.0.1:27017",
		ReplSetName:            replSet,
		MaxBsonObjectSizeBytes: types.MaxDocumentLen,
	}}
}

// TestHelloReplicaSetFields verifies that hello/isMaster advertise the
// single-node-primary replica-set identity Meteor's driver needs to accept the
// server as PRIMARY and start OpLog tailing (#6480/#6481).
func TestHelloReplicaSetFields(t *testing.T) {
	ctx := context.Background()

	t.Run("PrimaryFieldsWhenReplSetConfigured", func(t *testing.T) {
		h := newReplSetTestHandler("rs0")
		doc := must.NotFail(types.NewDocument("hello", int32(1)))

		res, err := h.hello(ctx, doc, h.TCPHost, h.ReplSetName)
		require.NoError(t, err)

		for k, want := range map[string]any{
			"setName":           "rs0",
			"me":                "127.0.0.1:27017",
			"primary":           "127.0.0.1:27017",
			"secondary":         false,
			"setVersion":        int32(1),
			"isWritablePrimary": true,
		} {
			got, _ := res.Get(k)
			assert.Equal(t, want, got, "hello field %q", k)
		}

		hosts, _ := res.Get("hosts")
		arr, ok := hosts.(*types.Array)
		require.True(t, ok, "hosts must be an array")
		require.Equal(t, 1, arr.Len())
		h0, _ := arr.Get(0)
		assert.Equal(t, "127.0.0.1:27017", h0)
	})

	t.Run("NegativeNoReplSetNoRSFields", func(t *testing.T) {
		h := newReplSetTestHandler("")
		doc := must.NotFail(types.NewDocument("hello", int32(1)))

		res, err := h.hello(ctx, doc, h.TCPHost, "")
		require.NoError(t, err)

		for _, k := range []string{"setName", "me", "primary", "secondary", "setVersion", "hosts"} {
			got, _ := res.Get(k)
			assert.Nil(t, got, "field %q must be absent without a replica set", k)
		}
	})

	t.Run("ColonOnlyHostNormalizedToLocalhost", func(t *testing.T) {
		h := newReplSetTestHandler("rs0")
		doc := must.NotFail(types.NewDocument("isMaster", int32(1)))

		res, err := h.hello(ctx, doc, ":27017", "rs0")
		require.NoError(t, err)

		me, _ := res.Get("me")
		assert.Equal(t, "localhost:27017", me)
		ismaster, _ := res.Get("ismaster")
		assert.Equal(t, true, ismaster)
	})
}

// TestMsgReplSetGetStatus checks the replSetGetStatus compatibility handler.
func TestMsgReplSetGetStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("PrimaryStatus", func(t *testing.T) {
		h := newReplSetTestHandler("rs0")
		in := must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("replSetGetStatus", int32(1)))))

		reply, err := h.MsgReplSetGetStatus(ctx, in)
		require.NoError(t, err)

		doc := must.NotFail(opMsgDocument(reply))

		set, _ := doc.Get("set")
		assert.Equal(t, "rs0", set)
		myState, _ := doc.Get("myState")
		assert.Equal(t, int32(1), myState)
		ok, _ := doc.Get("ok")
		assert.Equal(t, float64(1), ok)

		members, _ := doc.Get("members")
		arr, isArr := members.(*types.Array)
		require.True(t, isArr)
		require.Equal(t, 1, arr.Len())

		m0, _ := arr.Get(0)
		member, isDoc := m0.(*types.Document)
		require.True(t, isDoc)
		stateStr, _ := member.Get("stateStr")
		assert.Equal(t, "PRIMARY", stateStr)
		self, _ := member.Get("self")
		assert.Equal(t, true, self)
	})

	t.Run("NegativeErrorsWithoutReplSet", func(t *testing.T) {
		h := newReplSetTestHandler("")
		in := must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("replSetGetStatus", int32(1)))))

		_, err := h.MsgReplSetGetStatus(ctx, in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replica set")
	})
}

// TestMsgReplSetGetConfig checks the replSetGetConfig compatibility handler.
func TestMsgReplSetGetConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("SingleMemberConfig", func(t *testing.T) {
		h := newReplSetTestHandler("rs0")
		in := must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("replSetGetConfig", int32(1)))))

		reply, err := h.MsgReplSetGetConfig(ctx, in)
		require.NoError(t, err)

		doc := must.NotFail(opMsgDocument(reply))
		ok, _ := doc.Get("ok")
		assert.Equal(t, float64(1), ok)

		cfg, _ := doc.Get("config")
		config, isDoc := cfg.(*types.Document)
		require.True(t, isDoc)
		id, _ := config.Get("_id")
		assert.Equal(t, "rs0", id)

		members, _ := config.Get("members")
		arr, isArr := members.(*types.Array)
		require.True(t, isArr)
		require.Equal(t, 1, arr.Len())
		m0, _ := arr.Get(0)
		member, isMemberDoc := m0.(*types.Document)
		require.True(t, isMemberDoc)
		host, _ := member.Get("host")
		assert.Equal(t, "127.0.0.1:27017", host)
	})

	t.Run("NegativeErrorsWithoutReplSet", func(t *testing.T) {
		h := newReplSetTestHandler("")
		in := must.NotFail(documentOpMsg(must.NotFail(types.NewDocument("replSetGetConfig", int32(1)))))

		_, err := h.MsgReplSetGetConfig(ctx, in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replica set")
	})
}

// TestEnsureOplogNoopWithoutReplSet is a negative test: with no replica-set name,
// ensureOplog must return immediately without touching the (here nil) backend.
func TestEnsureOplogNoopWithoutReplSet(t *testing.T) {
	h := &Handler{NewOpts: &NewOpts{}} // no ReplSetName, no backend
	assert.NotPanics(t, func() { h.ensureOplog() })
}

// TestOplogCappedSizeBounded checks the auto-created local.oplog.rs cap size
// (wekan/wekan#6492). On the SQLite backend a large oplog kept local.sqlite big and
// made Meteor's constant tailing/locking expensive, pegging FerretDB CPU at 300%+
// even when idle, so the cap must stay SMALL — but still be a real, positive size so
// the collection is genuinely capped (a 0 size would create an UNBOUNDED oplog).
func TestOplogCappedSizeBounded(t *testing.T) {
	t.Parallel()

	const mib = int64(1024 * 1024)

	// Positive: a real, positive cap so the oplog is genuinely capped and tailable.
	assert.Positive(t, oplogCappedSizeBytes, "oplog cap must be a positive size")

	// Positive: small enough to keep local.sqlite tiny and the tail/scan cheap.
	assert.LessOrEqual(t, oplogCappedSizeBytes, 16*mib,
		"oplog cap must stay small (<=16 MiB) to bound local.sqlite and CPU (#6492)")

	// Negative: must NOT be back at (or above) the old oversized 128 MiB that bloated
	// local.sqlite and drove the high CPU.
	assert.Less(t, oplogCappedSizeBytes, 128*mib,
		"oplog cap must be well below the old 128 MiB (#6492)")

	// Negative: a 0 cap would make the oplog uncapped/unbounded — never allow it.
	assert.NotZero(t, oplogCappedSizeBytes, "a 0 cap would make the oplog unbounded")
}

// TestEnsureOplogCreatesCappedOplog is an integration test against a real SQLite
// backend: with a replica-set name, ensureOplog must create local.oplog.rs as a
// CAPPED collection sized at oplogCappedSizeBytes (and that size must stay small so
// the SQLite oplog cannot bloat — wekan/wekan#6492).
func TestEnsureOplogCreatesCappedOplog(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := sqlite.NewBackend(&sqlite.NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	h := &Handler{NewOpts: &NewOpts{ReplSetName: "rs0", L: testutil.Logger(t)}, b: b}

	h.ensureOplog()

	db, err := b.Database("local")
	require.NoError(t, err)

	cList, err := db.ListCollections(ctx, &backends.ListCollectionsParams{Name: "oplog.rs"})
	require.NoError(t, err)
	require.Len(t, cList.Collections, 1, "local.oplog.rs must be auto-created")

	c := cList.Collections[0]
	assert.True(t, c.Capped(), "local.oplog.rs must be a CAPPED collection")
	assert.Equal(t, oplogCappedSizeBytes, c.CappedSize, "cap size must be oplogCappedSizeBytes")
	assert.LessOrEqual(t, c.CappedSize, int64(16*1024*1024), "oplog cap must stay small (#6492)")
}

// TestEnsureOplogNoopWithBackendButNoReplSet is a negative integration test: with a
// real backend but NO replica-set name, ensureOplog must create no oplog at all.
func TestEnsureOplogNoopWithBackendButNoReplSet(t *testing.T) {
	t.Parallel()

	ctx := testutil.Ctx(t)

	sp, err := state.NewProvider("")
	require.NoError(t, err)

	b, err := sqlite.NewBackend(&sqlite.NewBackendParams{
		URI: testutil.TestSQLiteURI(t, ""), L: testutil.Logger(t), P: sp, BatchSize: 100,
	})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	h := &Handler{NewOpts: &NewOpts{ReplSetName: "", L: testutil.Logger(t)}, b: b}

	h.ensureOplog()

	db, err := b.Database("local")
	require.NoError(t, err)

	cList, err := db.ListCollections(ctx, &backends.ListCollectionsParams{Name: "oplog.rs"})
	require.NoError(t, err)
	assert.Empty(t, cList.Collections, "no oplog must be created without a replica-set name")
}
