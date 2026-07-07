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

// TestAggregateExprArray covers the array aggregation expression operators
// added on top of FerretDB v1.24.2: $size, $arrayElemAt, $concatArrays,
// $isArray, $in, $reverseArray, $slice, $range, $indexOfArray, $arrayToObject,
// $filter, $reduce, $sortArray, $setUnion, $setIntersection, $setDifference,
// $setEquals, $setIsSubset and $zip.
func TestAggregateExprArray(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	doc := bson.D{
		{"_id", "doc1"},
		{"arr", bson.A{int32(1), int32(2), int32(3)}},
		{"setA", bson.A{int32(1), int32(2), int32(3)}},
		{"setB", bson.A{int32(2), int32(3), int32(4)}},
		{"pairs", bson.A{
			bson.D{{"k", "x"}, {"v", int32(1)}},
			bson.D{{"k", "y"}, {"v", int32(2)}},
		}},
		{"nested", bson.A{
			bson.D{{"n", "b"}, {"q", int32(2)}},
			bson.D{{"n", "a"}, {"q", int32(1)}},
		}},
		{"obj", bson.D{{"a", int32(1)}, {"b", int32(2)}}},
		{"str", "hello"},
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
			"Size": {
				expr: bson.D{{"$size", "$arr"}},
				res:  bson.D{{"r", int32(3)}},
			},
			"ArrayElemAt": {
				expr: bson.D{{"$arrayElemAt", bson.A{"$arr", int32(1)}}},
				res:  bson.D{{"r", int32(2)}},
			},
			"ArrayElemAtNegative": {
				expr: bson.D{{"$arrayElemAt", bson.A{"$arr", int32(-1)}}},
				res:  bson.D{{"r", int32(3)}},
			},
			"ConcatArrays": {
				expr: bson.D{{"$concatArrays", bson.A{"$arr", bson.A{int32(4), int32(5)}}}},
				res:  bson.D{{"r", bson.A{int32(1), int32(2), int32(3), int32(4), int32(5)}}},
			},
			"ConcatArraysNull": {
				expr: bson.D{{"$concatArrays", bson.A{"$arr", "$nullField"}}},
				res:  bson.D{{"r", nil}},
			},
			"IsArrayTrue": {
				expr: bson.D{{"$isArray", "$arr"}},
				res:  bson.D{{"r", true}},
			},
			"IsArrayFalse": {
				expr: bson.D{{"$isArray", "$str"}},
				res:  bson.D{{"r", false}},
			},
			"InTrue": {
				expr: bson.D{{"$in", bson.A{int32(2), "$arr"}}},
				res:  bson.D{{"r", true}},
			},
			"InFalse": {
				expr: bson.D{{"$in", bson.A{int32(9), "$arr"}}},
				res:  bson.D{{"r", false}},
			},
			"ReverseArray": {
				expr: bson.D{{"$reverseArray", "$arr"}},
				res:  bson.D{{"r", bson.A{int32(3), int32(2), int32(1)}}},
			},
			"Slice2": {
				expr: bson.D{{"$slice", bson.A{"$arr", int32(2)}}},
				res:  bson.D{{"r", bson.A{int32(1), int32(2)}}},
			},
			"Slice2Negative": {
				expr: bson.D{{"$slice", bson.A{"$arr", int32(-2)}}},
				res:  bson.D{{"r", bson.A{int32(2), int32(3)}}},
			},
			"Slice3": {
				expr: bson.D{{"$slice", bson.A{"$arr", int32(1), int32(2)}}},
				res:  bson.D{{"r", bson.A{int32(2), int32(3)}}},
			},
			"Range": {
				expr: bson.D{{"$range", bson.A{int32(0), int32(4)}}},
				res:  bson.D{{"r", bson.A{int32(0), int32(1), int32(2), int32(3)}}},
			},
			"RangeStep": {
				expr: bson.D{{"$range", bson.A{int32(0), int32(10), int32(2)}}},
				res:  bson.D{{"r", bson.A{int32(0), int32(2), int32(4), int32(6), int32(8)}}},
			},
			"RangeNegativeStep": {
				expr: bson.D{{"$range", bson.A{int32(5), int32(0), int32(-2)}}},
				res:  bson.D{{"r", bson.A{int32(5), int32(3), int32(1)}}},
			},
			"IndexOfArray": {
				expr: bson.D{{"$indexOfArray", bson.A{"$arr", int32(2)}}},
				res:  bson.D{{"r", int32(1)}},
			},
			"IndexOfArrayMiss": {
				expr: bson.D{{"$indexOfArray", bson.A{"$arr", int32(9)}}},
				res:  bson.D{{"r", int32(-1)}},
			},
			"ArrayToObject": {
				expr: bson.D{{"$arrayToObject", "$pairs"}},
				res:  bson.D{{"r", bson.D{{"x", int32(1)}, {"y", int32(2)}}}},
			},
			// $map builds an array of two-element [k, v] arrays at runtime, which
			// $arrayToObject then converts back to a document.
			"ArrayToObjectPairs": {
				expr: bson.D{{"$arrayToObject", bson.D{{"$map", bson.D{
					{"input", "$pairs"},
					{"in", bson.A{"$$this.k", "$$this.v"}},
				}}}}},
				res: bson.D{{"r", bson.D{{"x", int32(1)}, {"y", int32(2)}}}},
			},
			"ArrayToObjectRoundTrip": {
				expr: bson.D{{"$arrayToObject", bson.D{{"$objectToArray", "$obj"}}}},
				res:  bson.D{{"r", bson.D{{"a", int32(1)}, {"b", int32(2)}}}},
			},
			"Filter": {
				expr: bson.D{{"$filter", bson.D{
					{"input", "$arr"},
					{"cond", bson.D{{"$gt", bson.A{"$$this", int32(1)}}}},
				}}},
				res: bson.D{{"r", bson.A{int32(2), int32(3)}}},
			},
			"FilterAs": {
				expr: bson.D{{"$filter", bson.D{
					{"input", "$arr"},
					{"as", "num"},
					{"cond", bson.D{{"$gte", bson.A{"$$num", int32(2)}}}},
				}}},
				res: bson.D{{"r", bson.A{int32(2), int32(3)}}},
			},
			"FilterLimit": {
				expr: bson.D{{"$filter", bson.D{
					{"input", "$arr"},
					{"cond", bson.D{{"$gt", bson.A{"$$this", int32(0)}}}},
					{"limit", int32(2)},
				}}},
				res: bson.D{{"r", bson.A{int32(1), int32(2)}}},
			},
			"Reduce": {
				expr: bson.D{{"$reduce", bson.D{
					{"input", "$arr"},
					{"initialValue", int32(0)},
					{"in", bson.D{{"$add", bson.A{"$$value", "$$this"}}}},
				}}},
				res: bson.D{{"r", int32(6)}},
			},
			"ReduceConcat": {
				expr: bson.D{{"$reduce", bson.D{
					{"input", bson.A{"a", "b", "c"}},
					{"initialValue", ""},
					{"in", bson.D{{"$concat", bson.A{"$$value", "$$this"}}}},
				}}},
				res: bson.D{{"r", "abc"}},
			},
			"SortArrayWhole": {
				expr: bson.D{{"$sortArray", bson.D{{"input", "$setB"}, {"sortBy", int32(-1)}}}},
				res:  bson.D{{"r", bson.A{int32(4), int32(3), int32(2)}}},
			},
			"SortArrayField": {
				expr: bson.D{{"$sortArray", bson.D{{"input", "$nested"}, {"sortBy", bson.D{{"n", int32(1)}}}}}},
				res: bson.D{{"r", bson.A{
					bson.D{{"n", "a"}, {"q", int32(1)}},
					bson.D{{"n", "b"}, {"q", int32(2)}},
				}}},
			},
			"SetUnion": {
				expr: bson.D{{"$setUnion", bson.A{"$setA", "$setB"}}},
				res:  bson.D{{"r", bson.A{int32(1), int32(2), int32(3), int32(4)}}},
			},
			"SetIntersection": {
				expr: bson.D{{"$setIntersection", bson.A{"$setA", "$setB"}}},
				res:  bson.D{{"r", bson.A{int32(2), int32(3)}}},
			},
			"SetDifference": {
				expr: bson.D{{"$setDifference", bson.A{"$setA", "$setB"}}},
				res:  bson.D{{"r", bson.A{int32(1)}}},
			},
			"SetEqualsTrue": {
				expr: bson.D{{"$setEquals", bson.A{"$setA", bson.A{int32(3), int32(2), int32(1), int32(1)}}}},
				res:  bson.D{{"r", true}},
			},
			"SetEqualsFalse": {
				expr: bson.D{{"$setEquals", bson.A{"$setA", "$setB"}}},
				res:  bson.D{{"r", false}},
			},
			"SetIsSubsetTrue": {
				expr: bson.D{{"$setIsSubset", bson.A{bson.A{int32(1), int32(2)}, "$setA"}}},
				res:  bson.D{{"r", true}},
			},
			"SetIsSubsetFalse": {
				expr: bson.D{{"$setIsSubset", bson.A{"$setB", "$setA"}}},
				res:  bson.D{{"r", false}},
			},
			"Zip": {
				expr: bson.D{{"$zip", bson.D{{"inputs", bson.A{"$arr", bson.A{"a", "b", "c"}}}}}},
				res: bson.D{{"r", bson.A{
					bson.A{int32(1), "a"},
					bson.A{int32(2), "b"},
					bson.A{int32(3), "c"},
				}}},
			},
			"ZipLongest": {
				expr: bson.D{{"$zip", bson.D{
					{"inputs", bson.A{"$arr", bson.A{"a"}}},
					{"useLongestLength", true},
					{"defaults", bson.A{int32(0), "z"}},
				}}},
				res: bson.D{{"r", bson.A{
					bson.A{int32(1), "a"},
					bson.A{int32(2), "z"},
					bson.A{int32(3), "z"},
				}}},
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
			"SizeNonArray": {
				expr: bson.D{{"$size", "$str"}},
			},
			"RangeStepZero": {
				expr: bson.D{{"$range", bson.A{int32(0), int32(5), int32(0)}}},
			},
			"ReduceMissingIn": {
				expr: bson.D{{"$reduce", bson.D{{"input", "$arr"}, {"initialValue", int32(0)}}}},
			},
			"FilterMissingCond": {
				expr: bson.D{{"$filter", bson.D{{"input", "$arr"}}}},
			},
			"SetUnionNonArray": {
				expr: bson.D{{"$setUnion", bson.A{"$str"}}},
			},
			"ArrayElemAtWrongArgsLen": {
				expr: bson.D{{"$arrayElemAt", bson.A{"$arr"}}},
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
