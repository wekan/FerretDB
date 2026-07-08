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

// TestSessionsTransactions verifies that FerretDB accepts logical sessions and the
// transaction command family as compatibility commands. FerretDB v1 with the SQLite
// backend has no real multi-document transactions: every write auto-commits, so the
// transaction commands are no-ops that must nonetheless succeed so that MongoDB
// drivers (and Meteor) do not error. It also verifies that ordinary write commands
// accept the retryable-write / session fields (`lsid`, `txnNumber`).
func TestSessionsTransactions(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)
	db := collection.Database()

	// a well-formed 16-byte UUID value for lsid.id
	lsidUUID := primitive.Binary{
		Subtype: 4, // UUID
		Data:    []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
	}
	lsid := bson.D{{"id", lsidUUID}}

	t.Run("StartSession", func(t *testing.T) {
		t.Parallel()

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
		assert.NotEmpty(t, res.ID.ID.Data, "startSession must return a session id binary")
		assert.Equal(t, byte(4), res.ID.ID.Subtype, "session id must be a UUID binary")
	})

	t.Run("CommitTransaction", func(t *testing.T) {
		t.Parallel()

		var res bson.D
		err := db.RunCommand(ctx, bson.D{
			{"commitTransaction", 1},
			{"lsid", lsid},
			{"txnNumber", int64(1)},
			{"autocommit", false},
		}).Decode(&res)
		require.NoError(t, err)

		m := res.Map()
		assert.Equal(t, float64(1), m["ok"])
	})

	t.Run("AbortTransaction", func(t *testing.T) {
		t.Parallel()

		var res bson.D
		err := db.RunCommand(ctx, bson.D{
			{"abortTransaction", 1},
			{"lsid", lsid},
			{"txnNumber", int64(1)},
			{"autocommit", false},
		}).Decode(&res)
		require.NoError(t, err)

		m := res.Map()
		assert.Equal(t, float64(1), m["ok"])
	})

	t.Run("EndSessions", func(t *testing.T) {
		t.Parallel()

		var res bson.D
		err := db.RunCommand(ctx, bson.D{
			{"endSessions", bson.A{lsid}},
		}).Decode(&res)
		require.NoError(t, err)

		m := res.Map()
		assert.Equal(t, float64(1), m["ok"])
	})

	t.Run("RefreshSessions", func(t *testing.T) {
		t.Parallel()

		var res bson.D
		err := db.RunCommand(ctx, bson.D{
			{"refreshSessions", bson.A{lsid}},
		}).Decode(&res)
		require.NoError(t, err)

		m := res.Map()
		assert.Equal(t, float64(1), m["ok"])
	})

	t.Run("RetryableWriteInsert", func(t *testing.T) {
		t.Parallel()

		// An `insert` carrying `lsid` and `txnNumber` (as a retryable write does)
		// must be accepted and must actually insert the document.
		var res struct {
			N  int32   `bson:"n"`
			OK float64 `bson:"ok"`
		}

		err := db.RunCommand(ctx, bson.D{
			{"insert", collection.Name()},
			{"documents", bson.A{bson.D{{"_id", "retryable1"}, {"v", int32(1)}}}},
			{"lsid", lsid},
			{"txnNumber", int64(2)},
		}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.OK)
		assert.Equal(t, int32(1), res.N)

		var found bson.D
		err = collection.FindOne(ctx, bson.D{{"_id", "retryable1"}}).Decode(&found)
		require.NoError(t, err)
		AssertEqualDocuments(t, bson.D{{"_id", "retryable1"}, {"v", int32(1)}}, found)
	})

	t.Run("PlainSession", func(t *testing.T) {
		t.Parallel()

		// A plain logical session (NOT a transaction): the driver attaches lsid to
		// the operations. This exercises lsid on real InsertOne/FindOne ops.
		sess, err := collection.Database().Client().StartSession()
		require.NoError(t, err)
		defer sess.EndSession(ctx)

		err = mongo.WithSession(ctx, sess, func(sc mongo.SessionContext) error {
			if _, e := collection.InsertOne(sc, bson.D{{"_id", "sessdoc"}, {"v", int32(2)}}); e != nil {
				return e
			}

			return collection.FindOne(sc, bson.D{{"_id", "sessdoc"}}).Err()
		})
		require.NoError(t, err)
	})

	t.Run("UnknownCommandStillErrors", func(t *testing.T) {
		t.Parallel()

		// Negative: an unrelated unknown command must still error, proving command
		// dispatch/parsing was not loosened.
		err := db.RunCommand(ctx, bson.D{{"totallyBogusCommand", 1}}).Err()
		require.Error(t, err)

		var cmdErr mongo.CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, int32(59), cmdErr.Code, "unknown command should return CommandNotFound (59)")
	})
}
