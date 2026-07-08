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
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestReplSetInitiate verifies that FerretDB accepts the `replSetInitiate` command as
// a compatibility no-op. FerretDB v1 does not implement real replication: its oplog is
// tailing-only and must be configured manually (a capped `local.oplog.rs` collection
// plus `FERRETDB_REPL_SET_NAME`). The command must nonetheless succeed so that tools
// and drivers that bootstrap a replica set do not hard-fail. It does NOT create an
// oplog, elect a primary, or change server topology.
func TestReplSetInitiate(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)
	db := collection.Database()

	t.Run("WithoutConfig", func(t *testing.T) {
		t.Parallel()

		var res struct {
			OK float64 `bson:"ok"`
		}

		err := db.RunCommand(ctx, bson.D{{"replSetInitiate", 1}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.OK)
	})

	t.Run("WithConfig", func(t *testing.T) {
		t.Parallel()

		var res struct {
			OK      float64 `bson:"ok"`
			SetName string  `bson:"setName"`
		}

		config := bson.D{
			{"_id", "rs0"},
			{"members", bson.A{bson.D{{"_id", 0}, {"host", "localhost:27017"}}}},
		}

		err := db.RunCommand(ctx, bson.D{{"replSetInitiate", config}}).Decode(&res)
		require.NoError(t, err)
		assert.Equal(t, float64(1), res.OK)
		assert.Equal(t, "rs0", res.SetName, "the provided replica set name should be echoed back")
	})

	t.Run("UnknownCommandStillErrors", func(t *testing.T) {
		t.Parallel()

		// Negative: a similarly named but unregistered command must still error,
		// proving the registration is specific and command dispatch was not loosened.
		err := db.RunCommand(ctx, bson.D{{"replSetBogusInitiate", 1}}).Err()
		require.Error(t, err)

		var cmdErr mongo.CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, int32(59), cmdErr.Code, "unknown command should return CommandNotFound (59)")
	})
}
