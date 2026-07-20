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

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestAggregateLookup covers the basic equality-join form of the $lookup aggregation stage
// commonly used by clients: { $lookup: { from, localField, foreignField, as } }.
func TestAggregateLookup(t *testing.T) {
	t.Parallel()

	ctx, cards := setup.Setup(t)

	// The "from" collection joined against.
	boards := cards.Database().Collection("boards_" + cards.Name())

	_, err := boards.InsertMany(ctx, []any{
		bson.D{{"_id", "b1"}, {"title", "Board One"}},
		bson.D{{"_id", "b2"}, {"title", "Board Two"}},
	})
	require.NoError(t, err)

	_, err = cards.InsertMany(ctx, []any{
		bson.D{{"_id", "c1"}, {"boardId", "b1"}},
		bson.D{{"_id", "c2"}, {"boardId", "b2"}},
		bson.D{{"_id", "c3"}, {"boardId", "missing"}},
	})
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		pipeline := bson.A{
			bson.D{{"$sort", bson.D{{"_id", int32(1)}}}},
			bson.D{{"$lookup", bson.D{
				{"from", boards.Name()},
				{"localField", "boardId"},
				{"foreignField", "_id"},
				{"as", "board"},
			}}},
		}

		cursor, err := cards.Aggregate(ctx, pipeline)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var res []bson.D
		err = cursor.All(ctx, &res)
		require.NoError(t, err)

		expected := []bson.D{
			{
				{"_id", "c1"},
				{"boardId", "b1"},
				{"board", bson.A{bson.D{{"_id", "b1"}, {"title", "Board One"}}}},
			},
			{
				{"_id", "c2"},
				{"boardId", "b2"},
				{"board", bson.A{bson.D{{"_id", "b2"}, {"title", "Board Two"}}}},
			},
			{
				// no matching board -> empty array
				{"_id", "c3"},
				{"boardId", "missing"},
				{"board", bson.A{}},
			},
		}

		require.Equal(t, expected, res)
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			spec any // $lookup specification that must fail
		}{
			"MissingFrom": {
				spec: bson.D{
					{"localField", "boardId"},
					{"foreignField", "_id"},
					{"as", "board"},
				},
			},
			"MissingAs": {
				spec: bson.D{
					{"from", boards.Name()},
					{"localField", "boardId"},
					{"foreignField", "_id"},
				},
			},
			"PipelineSubForm": {
				spec: bson.D{
					{"from", boards.Name()},
					{"pipeline", bson.A{}},
					{"as", "board"},
				},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{
					bson.D{{"$lookup", tc.spec}},
				}

				cursor, err := cards.Aggregate(ctx, pipeline)
				if err == nil {
					err = cursor.All(ctx, &[]bson.D{})
					cursor.Close(ctx)
				}

				require.Error(t, err)
			})
		}
	})
}
