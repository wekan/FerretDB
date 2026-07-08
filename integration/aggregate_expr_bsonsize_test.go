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

// TestAggregateExprBsonSize covers the `$bsonSize` aggregation expression
// operator added on top of FerretDB v1: it returns the size in bytes of the
// BSON encoding of a document, null for a null argument, and errors for a
// non-document argument or a wrong number of arguments.
func TestAggregateExprBsonSize(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	subdoc := bson.D{
		{"x", int32(1)},
		{"y", "hello"},
		{"z", true},
		{"w", 2.5},
		{"nested", bson.D{{"a", int64(7)}, {"b", "world"}}},
	}

	doc := bson.D{
		{"_id", "doc1"},
		{"subdoc", subdoc},
		{"str", "abc"},
		{"num", int32(42)},
		{"nullField", nil},
	}

	_, err := collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	// Expected BSON size of the subdocument, computed with the driver.
	subdocBytes, err := bson.Marshal(subdoc)
	require.NoError(t, err)
	subdocSize := int32(len(subdocBytes))

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any    // computed expression for field "r"
			res  bson.D // expected single result document
		}{
			"Subdoc": {
				expr: bson.D{{"$bsonSize", "$subdoc"}},
				res:  bson.D{{"r", subdocSize}},
			},
			"Null": {
				expr: bson.D{{"$bsonSize", "$nullField"}},
				res:  bson.D{{"r", nil}},
			},
			"MissingField": {
				// A missing field path evaluates to null, so $bsonSize is null.
				expr: bson.D{{"$bsonSize", "$noSuchField"}},
				res:  bson.D{{"r", nil}},
			},
			"Literal": {
				expr: bson.D{{"$bsonSize", bson.D{{"$literal", nil}}}},
				res:  bson.D{{"r", nil}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{
					bson.D{{"$project", bson.D{{"_id", false}, {"r", tc.expr}}}},
				}

				cursor, err := collection.Aggregate(ctx, pipeline)
				require.NoError(t, err)
				defer cursor.Close(ctx)

				var res []bson.D
				err = cursor.All(ctx, &res)
				require.NoError(t, err)
				require.Equal(t, []bson.D{tc.res}, res)
			})
		}
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any // computed expression that must fail
		}{
			"NonDocumentString": {
				expr: bson.D{{"$bsonSize", "$str"}},
			},
			"NonDocumentInt": {
				expr: bson.D{{"$bsonSize", "$num"}},
			},
			"TooManyArgs": {
				expr: bson.D{{"$bsonSize", bson.A{"$subdoc", "$subdoc"}}},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{
					bson.D{{"$project", bson.D{{"_id", false}, {"r", tc.expr}}}},
				}

				cursor, err := collection.Aggregate(ctx, pipeline)
				if err == nil {
					// error may surface while draining the cursor
					err = cursor.All(ctx, &[]bson.D{})
					cursor.Close(ctx)
				}

				require.Error(t, err)
			})
		}
	})
}
