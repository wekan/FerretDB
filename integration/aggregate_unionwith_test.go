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

// TestAggregateUnionWith covers the $unionWith aggregation stage: the short string form and the
// object form with an optional sub-pipeline.
func TestAggregateUnionWith(t *testing.T) {
	t.Parallel()

	ctx, cards := setup.Setup(t)

	// The other ("union") collection appended to the current stream.
	boards := cards.Database().Collection("boards_" + cards.Name())

	_, err := boards.InsertMany(ctx, []any{
		bson.D{{"_id", "b1"}, {"kind", "board"}, {"archived", false}},
		bson.D{{"_id", "b2"}, {"kind", "board"}, {"archived", true}},
		bson.D{{"_id", "b3"}, {"kind", "board"}, {"archived", false}},
	})
	require.NoError(t, err)

	_, err = cards.InsertMany(ctx, []any{
		bson.D{{"_id", "c1"}, {"kind", "card"}},
		bson.D{{"_id", "c2"}, {"kind", "card"}},
	})
	require.NoError(t, err)

	t.Run("StringForm", func(t *testing.T) {
		t.Parallel()

		// All boards documents appended after all cards documents.
		pipeline := bson.A{
			bson.D{{"$unionWith", boards.Name()}},
		}

		cursor, err := cards.Aggregate(ctx, pipeline)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var res []bson.D
		err = cursor.All(ctx, &res)
		require.NoError(t, err)

		// 2 cards + 3 boards = 5 documents.
		require.Len(t, res, 5)

		ids := make([]string, len(res))
		for i, doc := range res {
			m := doc.Map()
			ids[i] = m["_id"].(string)
		}

		require.ElementsMatch(t, []string{"c1", "c2", "b1", "b2", "b3"}, ids)
	})

	t.Run("PipelineForm", func(t *testing.T) {
		t.Parallel()

		// Only the non-archived boards are appended.
		pipeline := bson.A{
			bson.D{{"$unionWith", bson.D{
				{"coll", boards.Name()},
				{"pipeline", bson.A{
					bson.D{{"$match", bson.D{{"archived", false}}}},
				}},
			}}},
		}

		cursor, err := cards.Aggregate(ctx, pipeline)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var res []bson.D
		err = cursor.All(ctx, &res)
		require.NoError(t, err)

		// 2 cards + 2 non-archived boards (b1, b3) = 4 documents.
		require.Len(t, res, 4)

		ids := make([]string, len(res))
		for i, doc := range res {
			m := doc.Map()
			ids[i] = m["_id"].(string)
		}

		require.ElementsMatch(t, []string{"c1", "c2", "b1", "b3"}, ids)
	})

	t.Run("PipelineFormWithCount", func(t *testing.T) {
		t.Parallel()

		// Union the cards with the archived boards, then count the total.
		pipeline := bson.A{
			bson.D{{"$unionWith", bson.D{
				{"coll", boards.Name()},
				{"pipeline", bson.A{
					bson.D{{"$match", bson.D{{"archived", true}}}},
				}},
			}}},
			bson.D{{"$count", "total"}},
		}

		cursor, err := cards.Aggregate(ctx, pipeline)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var res []bson.D
		err = cursor.All(ctx, &res)
		require.NoError(t, err)

		// 2 cards + 1 archived board (b2) = 3.
		expected := []bson.D{{{"total", int32(3)}}}
		require.Equal(t, expected, res)
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			spec any // $unionWith specification that must fail
		}{
			"NeitherStringNorObject": {
				spec: int32(42),
			},
			"MissingColl": {
				spec: bson.D{
					{"pipeline", bson.A{}},
				},
			},
			"CollNotString": {
				spec: bson.D{
					{"coll", int32(1)},
				},
			},
			"PipelineNotArray": {
				spec: bson.D{
					{"coll", boards.Name()},
					{"pipeline", "notAnArray"},
				},
			},
			"PipelineWithOut": {
				spec: bson.D{
					{"coll", boards.Name()},
					{"pipeline", bson.A{
						bson.D{{"$out", "somewhere"}},
					}},
				},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{
					bson.D{{"$unionWith", tc.spec}},
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
