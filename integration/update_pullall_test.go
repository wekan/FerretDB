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

// TestUpdatePullAll checks the $pullAll update operator, which removes all
// occurrences of each listed value from an array field.
func TestUpdatePullAll(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		doc    bson.D // document to insert before the update
		update bson.D // update to apply

		res     *mongo.UpdateResult // optional, expected result of the update
		findRes bson.D              // optional, expected document after the update
	}{
		"RemoveDuplicates": {
			doc:     bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2), int32(3), int32(2), int32(1), int32(4)}}},
			update:  bson.D{{"$pullAll", bson.D{{"v", bson.A{int32(1), int32(2)}}}}},
			findRes: bson.D{{"_id", "pullall"}, {"v", bson.A{int32(3), int32(4)}}},
			res: &mongo.UpdateResult{
				MatchedCount:  1,
				ModifiedCount: 1,
			},
		},
		"RemoveSingle": {
			doc:     bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update:  bson.D{{"$pullAll", bson.D{{"v", bson.A{int32(2)}}}}},
			findRes: bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(3)}}},
			res: &mongo.UpdateResult{
				MatchedCount:  1,
				ModifiedCount: 1,
			},
		},
		"ValueNotPresent": {
			doc:     bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update:  bson.D{{"$pullAll", bson.D{{"v", bson.A{int32(9)}}}}},
			findRes: bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			res: &mongo.UpdateResult{
				MatchedCount:  1,
				ModifiedCount: 0,
			},
		},
		"FieldNotPresent": {
			doc:     bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			update:  bson.D{{"$pullAll", bson.D{{"missing", bson.A{int32(1)}}}}},
			findRes: bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2), int32(3)}}},
			res: &mongo.UpdateResult{
				MatchedCount:  1,
				ModifiedCount: 0,
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, collection := setup.Setup(t)

			_, err := collection.InsertOne(ctx, tc.doc)
			require.NoError(t, err)

			res, err := collection.UpdateOne(ctx, bson.D{{"_id", "pullall"}}, tc.update)
			require.NoError(t, err)
			require.Equal(t, tc.res, res)

			var actual bson.D
			err = collection.FindOne(ctx, bson.D{{"_id", "pullall"}}).Decode(&actual)
			require.NoError(t, err)
			AssertEqualDocuments(t, tc.findRes, actual)
		})
	}
}

// TestUpdatePullAllErrors checks the error cases of the $pullAll update operator.
func TestUpdatePullAllErrors(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		doc    bson.D // document to insert before the update
		update bson.D // update to apply

		err *mongo.WriteError // required, expected error from the update
	}{
		"NonArrayArgument": {
			doc:    bson.D{{"_id", "pullall"}, {"v", bson.A{int32(1), int32(2)}}},
			update: bson.D{{"$pullAll", bson.D{{"v", "string"}}}},
			err: &mongo.WriteError{
				Code:    2,
				Message: "The field 'v' must be an array but is of type 'string'",
			},
		},
		"NonArrayTarget": {
			doc:    bson.D{{"_id", "pullall"}, {"v", int32(42)}},
			update: bson.D{{"$pullAll", bson.D{{"v", bson.A{int32(1)}}}}},
			err: &mongo.WriteError{
				Code:    2,
				Message: `The field 'v' must be an array but is of type 'int' in document {_id: "pullall"}`,
			},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, collection := setup.Setup(t)

			_, err := collection.InsertOne(ctx, tc.doc)
			require.NoError(t, err)

			_, err = collection.UpdateOne(ctx, bson.D{{"_id", "pullall"}}, tc.update)
			require.NotNil(t, tc.err, "err should be set")
			AssertEqualWriteError(t, *tc.err, err)
		})
	}
}
