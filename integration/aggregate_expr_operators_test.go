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

// TestAggregateExprOperators covers the aggregation expression operators used by
// WeKan that were added on top of FerretDB v1.24.2:
// $eq, $ne, $or, $ifNull, $anyElementTrue, $objectToArray and $map.
func TestAggregateExprOperators(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	doc := bson.D{
		{"_id", "doc1"},
		{"a", int32(5)},
		{"b", int32(5)},
		{"c", int32(7)},
		{"nested", bson.D{{"x", int32(1)}, {"y", int32(2)}}},
		{"arr", bson.A{int32(1), int32(2), int32(3)}},
		{"flags", bson.A{false, false, true}},
		{"nullField", nil},
	}

	_, err := collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any    // computed expression for field "r"
			res  bson.D // expected single result document
		}{
			"EqTrue": {
				expr: bson.D{{"$eq", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", true}},
			},
			"EqFalse": {
				expr: bson.D{{"$eq", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", false}},
			},
			"EqNumericCrossType": {
				expr: bson.D{{"$eq", bson.A{"$a", 5.0}}},
				res:  bson.D{{"r", true}},
			},
			"NeTrue": {
				expr: bson.D{{"$ne", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", true}},
			},
			"NeFalse": {
				expr: bson.D{{"$ne", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", false}},
			},
			"OrTrue": {
				expr: bson.D{{"$or", bson.A{false, "$missing", "$a"}}},
				res:  bson.D{{"r", true}},
			},
			"OrFalse": {
				expr: bson.D{{"$or", bson.A{false, int32(0), "$nullField"}}},
				res:  bson.D{{"r", false}},
			},
			"IfNullPresent": {
				expr: bson.D{{"$ifNull", bson.A{"$a", int32(99)}}},
				res:  bson.D{{"r", int32(5)}},
			},
			"IfNullReplace": {
				expr: bson.D{{"$ifNull", bson.A{"$missing", int32(99)}}},
				res:  bson.D{{"r", int32(99)}},
			},
			"IfNullMultiInput": {
				expr: bson.D{{"$ifNull", bson.A{"$missing", "$nullField", int32(7)}}},
				res:  bson.D{{"r", int32(7)}},
			},
			"AnyElementTrueField": {
				expr: bson.D{{"$anyElementTrue", bson.A{"$flags"}}},
				res:  bson.D{{"r", true}},
			},
			"AnyElementTrueFalse": {
				expr: bson.D{{"$anyElementTrue", bson.A{bson.A{false, int32(0)}}}},
				res:  bson.D{{"r", false}},
			},
			"ObjectToArray": {
				expr: bson.D{{"$objectToArray", "$nested"}},
				res: bson.D{{"r", bson.A{
					bson.D{{"k", "x"}, {"v", int32(1)}},
					bson.D{{"k", "y"}, {"v", int32(2)}},
				}}},
			},
			"MapDefaultThis": {
				expr: bson.D{{"$map", bson.D{
					{"input", "$arr"},
					{"in", "$$this"},
				}}},
				res: bson.D{{"r", bson.A{int32(1), int32(2), int32(3)}}},
			},
			"MapNamedVarOperator": {
				expr: bson.D{{"$map", bson.D{
					{"input", "$arr"},
					{"as", "num"},
					{"in", bson.D{{"$ne", bson.A{"$$num", int32(2)}}}},
				}}},
				res: bson.D{{"r", bson.A{true, false, true}}},
			},
			"MapNullInput": {
				expr: bson.D{{"$map", bson.D{
					{"input", "$missing"},
					{"in", "$$this"},
				}}},
				res: bson.D{{"r", nil}},
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
			"EqWrongArgsLen": {
				expr: bson.D{{"$eq", bson.A{"$a"}}},
			},
			"ObjectToArrayNonDocument": {
				expr: bson.D{{"$objectToArray", "$a"}},
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
