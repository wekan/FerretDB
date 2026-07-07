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
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestUpdatePushModifiers tests the $push modifiers $each, $position, $sort and $slice.
func TestUpdatePushModifiers(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		initial bson.D            // required, document inserted before the update
		update  bson.D            // required, used for the update parameter
		findRes bson.D            // optional, expected document after the update
		err     *mongo.WriteError // optional, expected error from the update
	}{
		"SlicePositiveKeepsFirstN": {
			initial: bson.D{{"_id", "slice-pos"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(4), int32(5)}},
				{"$slice", int32(3)},
			}}}}},
			findRes: bson.D{{"_id", "slice-pos"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
		},
		"SliceNegativeKeepsLastN": {
			initial: bson.D{{"_id", "slice-neg"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(4), int32(5)}},
				{"$slice", int32(-2)},
			}}}}},
			findRes: bson.D{{"_id", "slice-neg"}, {"v", bson.A{int32(4), int32(5)}}},
		},
		"SliceZeroEmptiesArray": {
			initial: bson.D{{"_id", "slice-zero"}, {"v", bson.A{int32(1), int32(2)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(3)}},
				{"$slice", int32(0)},
			}}}}},
			findRes: bson.D{{"_id", "slice-zero"}, {"v", bson.A{}}},
		},
		"SortScalarAscending": {
			initial: bson.D{{"_id", "sort-asc"}, {"v", bson.A{int32(3), int32(1)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(2), int32(5), int32(4)}},
				{"$sort", int32(1)},
			}}}}},
			findRes: bson.D{{"_id", "sort-asc"}, {"v", bson.A{int32(1), int32(2), int32(3), int32(4), int32(5)}}},
		},
		"SortScalarDescending": {
			initial: bson.D{{"_id", "sort-desc"}, {"v", bson.A{int32(3), int32(1)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(2)}},
				{"$sort", int32(-1)},
			}}}}},
			findRes: bson.D{{"_id", "sort-desc"}, {"v", bson.A{int32(3), int32(2), int32(1)}}},
		},
		"SortBySubDocumentField": {
			initial: bson.D{{"_id", "sort-subdoc"}, {"v", bson.A{
				bson.D{{"score", int32(3)}},
				bson.D{{"score", int32(1)}},
			}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{bson.D{{"score", int32(2)}}}},
				{"$sort", bson.D{{"score", int32(1)}}},
			}}}}},
			findRes: bson.D{{"_id", "sort-subdoc"}, {"v", bson.A{
				bson.D{{"score", int32(1)}},
				bson.D{{"score", int32(2)}},
				bson.D{{"score", int32(3)}},
			}}},
		},
		"PositionInsertsAtIndex": {
			initial: bson.D{{"_id", "position"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(10), int32(20)}},
				{"$position", int32(1)},
			}}}}},
			findRes: bson.D{{"_id", "position"}, {"v", bson.A{int32(1), int32(10), int32(20), int32(2), int32(3)}}},
		},
		"PositionNegative": {
			initial: bson.D{{"_id", "position-neg"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(99)}},
				{"$position", int32(-1)},
			}}}}},
			findRes: bson.D{{"_id", "position-neg"}, {"v", bson.A{int32(1), int32(2), int32(99), int32(3)}}},
		},
		"PositionSortSlice": {
			initial: bson.D{{"_id", "combined"}, {"v", bson.A{int32(5), int32(1)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(3), int32(4), int32(2)}},
				{"$position", int32(0)},
				{"$sort", int32(1)},
				{"$slice", int32(3)},
			}}}}},
			findRes: bson.D{{"_id", "combined"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
		},
		"SliceNonNumeric": {
			initial: bson.D{{"_id", "slice-bad"}, {"v", bson.A{int32(1)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(2)}},
				{"$slice", "foo"},
			}}}}},
			err: &mongo.WriteError{
				Code:    2,
				Message: "The value for $slice must be a numeric value but was given type: string",
			},
		},
		"SortInvalidValue": {
			initial: bson.D{{"_id", "sort-bad"}, {"v", bson.A{int32(1)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(2)}},
				{"$sort", int32(2)},
			}}}}},
			err: &mongo.WriteError{
				Code:    2,
				Message: "The $sort element value must be either 1 or -1",
			},
		},
		"UnknownModifier": {
			initial: bson.D{{"_id", "unknown"}, {"v", bson.A{int32(1)}}},
			update: bson.D{{"$push", bson.D{{"v", bson.D{
				{"$each", bson.A{int32(2)}},
				{"$unknown", int32(1)},
			}}}}},
			err: &mongo.WriteError{
				Code:    9,
				Message: "Unrecognized clause in $push: $unknown",
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotNil(t, tc.initial, "initial should be set")
			require.NotNil(t, tc.update, "update should be set")

			ctx, collection := setup.Setup(t)

			_, err := collection.InsertOne(ctx, tc.initial)
			require.NoError(t, err)

			id := tc.initial[0].Value

			_, err = collection.UpdateOne(ctx, bson.D{{"_id", id}}, tc.update)

			if tc.err != nil {
				AssertEqualWriteError(t, *tc.err, err)
				return
			}

			require.NoError(t, err)

			var actual bson.D
			err = collection.FindOne(ctx, bson.D{{"_id", id}}).Decode(&actual)
			require.NoError(t, err)
			AssertEqualDocuments(t, tc.findRes, actual)
		})
	}
}
