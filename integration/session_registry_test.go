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

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestSessionRegistry verifies that FerretDB v1 tracks logical sessions in a
// real server-side registry (ported/adapted from FerretDB v2). Because the
// registry is server-internal, the assertions only check what a MongoDB driver
// can observe: `startSession` returns a session id, two calls yield distinct
// ids, and the lifecycle commands (`endSessions`, `refreshSessions`,
// `killSessions`, `killAllSessions`, `killAllSessionsByPattern`) succeed with
// `{ok: 1}`. Transactions themselves remain no-ops in v1.
func TestSessionRegistry(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)
	db := collection.Database()

	// startSessionID runs `startSession` and returns the generated session id binary.
	startSessionID := func(t *testing.T) primitive.Binary {
		t.Helper()

		var res struct {
			ID struct {
				ID primitive.Binary `bson:"id"`
			} `bson:"id"`
			TimeoutMinutes int32   `bson:"timeoutMinutes"`
			OK             float64 `bson:"ok"`
		}

		err := db.RunCommand(ctx, bson.D{{"startSession", 1}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.OK)
		assert.Equal(t, int32(30), res.TimeoutMinutes, "timeoutMinutes must be 30")
		assert.NotEmpty(t, res.ID.ID.Data, "startSession must return a session id binary")
		assert.Equal(t, byte(4), res.ID.ID.Subtype, "session id must be a UUID binary")

		return res.ID.ID
	}

	// lsidFrom builds an lsid document from a session id binary.
	lsidFrom := func(id primitive.Binary) bson.D {
		return bson.D{{"id", id}}
	}

	t.Run("StartSessionDistinctIDs", func(t *testing.T) {
		id1 := startSessionID(t)
		id2 := startSessionID(t)

		assert.NotEqual(t, id1.Data, id2.Data, "two startSession calls must yield distinct ids")
	})

	t.Run("EndSessionsThenReuseOK", func(t *testing.T) {
		id := startSessionID(t)
		lsid := lsidFrom(id)

		var res bson.D
		err := db.RunCommand(ctx, bson.D{{"endSessions", bson.A{lsid}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])

		// After ending, referencing the same session again must still succeed
		// (it is recreated implicitly): the driver only observes ok:1.
		err = db.RunCommand(ctx, bson.D{{"refreshSessions", bson.A{lsid}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])

		err = db.RunCommand(ctx, bson.D{{"killSessions", bson.A{lsid}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])
	})

	t.Run("RefreshSessions", func(t *testing.T) {
		id := startSessionID(t)

		var res bson.D
		err := db.RunCommand(ctx, bson.D{{"refreshSessions", bson.A{lsidFrom(id)}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])
	})

	t.Run("KillSessionsSpecific", func(t *testing.T) {
		id := startSessionID(t)

		var res bson.D
		err := db.RunCommand(ctx, bson.D{{"killSessions", bson.A{lsidFrom(id)}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])
	})

	t.Run("KillSessionsEmpty", func(t *testing.T) {
		// An empty array kills all sessions of the current user.
		var res bson.D
		err := db.RunCommand(ctx, bson.D{{"killSessions", bson.A{}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])
	})

	t.Run("KillAllSessions", func(t *testing.T) {
		var res bson.D
		err := db.RunCommand(ctx, bson.D{{"killAllSessions", bson.A{}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])
	})

	t.Run("KillAllSessionsByPattern", func(t *testing.T) {
		var res bson.D
		err := db.RunCommand(ctx, bson.D{{"killAllSessionsByPattern", bson.A{}}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.Map()["ok"])
	})

	t.Run("EndSessionsNotArrayErrors", func(t *testing.T) {
		// Negative: a malformed `endSessions` argument (not an array) must error.
		err := db.RunCommand(ctx, bson.D{{"endSessions", "notanarray"}}).Err()
		require.Error(t, err)

		var cmdErr mongo.CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, int32(14), cmdErr.Code, "wrong type should return TypeMismatch (14)")
	})
}
