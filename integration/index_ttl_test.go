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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestIndexTTL covers TTL indexes: createIndexes parsing and validation,
// listIndexes reporting of expireAfterSeconds, and the background reaper.
func TestIndexTTL(t *testing.T) {
	t.Parallel()

	// A short cleanup interval makes the reaper act quickly; the interval is only
	// honored by an in-process FerretDB (i.e. not when targeting an external URI or MongoDB).
	s := setup.SetupWithOpts(t, &setup.SetupOpts{
		BackendOptions: &setup.BackendOpts{
			TTLCleanupInterval: 100 * time.Millisecond,
		},
	})

	ctx := s.Ctx
	db := s.Collection.Database()

	t.Run("Reaper", func(t *testing.T) {
		setup.SkipForMongoDB(t, "TTL reaper interval is a FerretDB-specific test option")

		collName := "ttl_reaper"
		coll := db.Collection(collName)

		past := time.Now().Add(-time.Hour)
		future := time.Now().Add(time.Hour)

		_, err := coll.InsertMany(ctx, []any{
			bson.D{{"_id", "past"}, {"createdAt", past}},
			bson.D{{"_id", "future"}, {"createdAt", future}},
		})
		require.NoError(t, err)

		// expireAfterSeconds: 0 => documents expire as soon as createdAt <= now.
		createRes := db.RunCommand(ctx, bson.D{
			{"createIndexes", collName},
			{"indexes", bson.A{bson.D{
				{"key", bson.D{{"createdAt", 1}}},
				{"name", "createdAt_1"},
				{"expireAfterSeconds", int32(0)},
			}}},
		})
		require.NoError(t, createRes.Err())

		// The reaper (100ms interval) should delete only the past document.
		require.Eventually(t, func() bool {
			n, err := coll.CountDocuments(ctx, bson.D{})
			require.NoError(t, err)

			return n == 1
		}, 10*time.Second, 100*time.Millisecond, "reaper did not delete the expired document")

		// Only the future document must remain.
		var remaining []bson.D
		cur, err := coll.Find(ctx, bson.D{})
		require.NoError(t, err)
		require.NoError(t, cur.All(ctx, &remaining))

		require.Len(t, remaining, 1)

		id, err := ConvertDocument(t, remaining[0]).Get("_id")
		require.NoError(t, err)
		assert.Equal(t, "future", id)
	})

	t.Run("ListIndexesReportsExpireAfterSeconds", func(t *testing.T) {
		collName := "ttl_list"
		coll := db.Collection(collName)

		_, err := coll.InsertOne(ctx, bson.D{{"_id", "x"}, {"createdAt", time.Now().Add(time.Hour)}})
		require.NoError(t, err)

		createRes := db.RunCommand(ctx, bson.D{
			{"createIndexes", collName},
			{"indexes", bson.A{bson.D{
				{"key", bson.D{{"createdAt", 1}}},
				{"name", "createdAt_1"},
				{"expireAfterSeconds", int32(3600)},
			}}},
		})
		require.NoError(t, createRes.Err())

		cur, err := coll.Indexes().List(ctx)
		require.NoError(t, err)

		var indexes []bson.M
		require.NoError(t, cur.All(ctx, &indexes))

		var found bool
		for _, idx := range indexes {
			if idx["name"] == "createdAt_1" {
				found = true
				v, ok := idx["expireAfterSeconds"]
				require.True(t, ok, "expireAfterSeconds must be reported by listIndexes")
				assert.EqualValues(t, 3600, v)
			}
		}

		require.True(t, found, "TTL index not found in listIndexes output")
	})

	t.Run("CompoundTTLErrors", func(t *testing.T) {
		collName := "ttl_compound"

		res := db.RunCommand(ctx, bson.D{
			{"createIndexes", collName},
			{"indexes", bson.A{bson.D{
				{"key", bson.D{{"a", 1}, {"b", 1}}},
				{"name", "a_1_b_1"},
				{"expireAfterSeconds", int32(100)},
			}}},
		})
		require.Error(t, res.Err(), "expireAfterSeconds on a compound index must be rejected")
	})

	t.Run("NegativeExpireAfterSecondsErrors", func(t *testing.T) {
		collName := "ttl_negative"

		res := db.RunCommand(ctx, bson.D{
			{"createIndexes", collName},
			{"indexes", bson.A{bson.D{
				{"key", bson.D{{"createdAt", 1}}},
				{"name", "createdAt_1"},
				{"expireAfterSeconds", int32(-1)},
			}}},
		})
		require.Error(t, res.Err(), "negative expireAfterSeconds must be rejected")
	})

	t.Run("NonNumericExpireAfterSecondsErrors", func(t *testing.T) {
		collName := "ttl_nonnumeric"

		res := db.RunCommand(ctx, bson.D{
			{"createIndexes", collName},
			{"indexes", bson.A{bson.D{
				{"key", bson.D{{"createdAt", 1}}},
				{"name", "createdAt_1"},
				{"expireAfterSeconds", "abc"},
			}}},
		})
		require.Error(t, res.Err(), "non-numeric expireAfterSeconds must be rejected")
	})
}
