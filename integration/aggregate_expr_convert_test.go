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

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestAggregateExprConvert covers the type-conversion and related aggregation
// expression operators added on top of FerretDB v1.24.2: $toString, $toInt,
// $toLong, $toDouble, $toBool, $toObjectId, $toDate, $convert, $isNumber,
// $literal, $let, $getField, $setField, $unsetField, $binarySize and $rand.
func TestAggregateExprConvert(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	oid, err := primitive.ObjectIDFromHex("5ab9cbfa31c2ab715d42129e")
	require.NoError(t, err)

	date := time.Date(2021, time.January, 2, 3, 4, 5, 0, time.UTC)

	doc := bson.D{
		{"_id", "doc1"},
		{"intField", int32(42)},
		{"longField", int64(9000000000)},
		{"doubleField", 3.75},
		{"strInt", "123"},
		{"strDouble", "2.5"},
		{"boolField", true},
		{"dateField", date},
		{"oid", oid},
		{"obj", bson.D{{"a", int32(1)}, {"b", int32(2)}}},
		{"str", "hello"},
		{"nullField", nil},
	}

	_, err = collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any    // computed expression for field "r"
			res  bson.D // expected single result document
		}{
			"ToStringFromInt": {
				expr: bson.D{{"$toString", "$intField"}},
				res:  bson.D{{"r", "42"}},
			},
			"ToStringFromDate": {
				expr: bson.D{{"$toString", "$dateField"}},
				res:  bson.D{{"r", "2021-01-02T03:04:05.000Z"}},
			},
			"ToStringFromObjectId": {
				expr: bson.D{{"$toString", "$oid"}},
				res:  bson.D{{"r", "5ab9cbfa31c2ab715d42129e"}},
			},
			"ToStringNull": {
				expr: bson.D{{"$toString", "$nullField"}},
				res:  bson.D{{"r", nil}},
			},
			"ToIntFromString": {
				expr: bson.D{{"$toInt", "$strInt"}},
				res:  bson.D{{"r", int32(123)}},
			},
			"ToIntFromDouble": {
				expr: bson.D{{"$toInt", "$doubleField"}},
				res:  bson.D{{"r", int32(3)}},
			},
			"ToLongFromString": {
				expr: bson.D{{"$toLong", "$strInt"}},
				res:  bson.D{{"r", int64(123)}},
			},
			"ToDoubleFromString": {
				expr: bson.D{{"$toDouble", "$strDouble"}},
				res:  bson.D{{"r", 2.5}},
			},
			"ToBoolFromInt": {
				expr: bson.D{{"$toBool", "$intField"}},
				res:  bson.D{{"r", true}},
			},
			"ToBoolFromZero": {
				expr: bson.D{{"$toBool", int32(0)}},
				res:  bson.D{{"r", false}},
			},
			"ToDateFromMillis": {
				expr: bson.D{{"$toDate", int64(1609556645000)}},
				res:  bson.D{{"r", primitive.NewDateTimeFromTime(date)}},
			},
			"ToObjectIdFromHex": {
				expr: bson.D{{"$toObjectId", "5ab9cbfa31c2ab715d42129e"}},
				res:  bson.D{{"r", oid}},
			},
			"ConvertToInt": {
				expr: bson.D{{"$convert", bson.D{{"input", "$strInt"}, {"to", "int"}}}},
				res:  bson.D{{"r", int32(123)}},
			},
			"ConvertOnError": {
				expr: bson.D{{"$convert", bson.D{
					{"input", "$str"},
					{"to", "int"},
					{"onError", "bad"},
				}}},
				res: bson.D{{"r", "bad"}},
			},
			"ConvertOnNull": {
				expr: bson.D{{"$convert", bson.D{
					{"input", "$nullField"},
					{"to", "int"},
					{"onNull", int32(-1)},
				}}},
				res: bson.D{{"r", int32(-1)}},
			},
			"IsNumberTrue": {
				expr: bson.D{{"$isNumber", "$intField"}},
				res:  bson.D{{"r", true}},
			},
			"IsNumberFalse": {
				expr: bson.D{{"$isNumber", "$str"}},
				res:  bson.D{{"r", false}},
			},
			"Literal": {
				expr: bson.D{{"$literal", "$str"}},
				res:  bson.D{{"r", "$str"}},
			},
			"LiteralNumber": {
				expr: bson.D{{"$literal", int32(1)}},
				res:  bson.D{{"r", int32(1)}},
			},
			"Let": {
				expr: bson.D{{"$let", bson.D{
					{"vars", bson.D{{"a", int32(2)}, {"b", int32(3)}}},
					{"in", bson.D{{"$add", bson.A{"$$a", "$$b"}}}},
				}}},
				res: bson.D{{"r", int32(5)}},
			},
			"LetWithField": {
				expr: bson.D{{"$let", bson.D{
					{"vars", bson.D{{"x", "$intField"}, {"y", int32(8)}}},
					{"in", bson.D{{"$multiply", bson.A{"$$x", "$$y"}}}},
				}}},
				res: bson.D{{"r", int32(336)}},
			},
			"GetField": {
				expr: bson.D{{"$getField", bson.D{{"field", "a"}, {"input", "$obj"}}}},
				res:  bson.D{{"r", int32(1)}},
			},
			"GetFieldShorthand": {
				expr: bson.D{{"$getField", "intField"}},
				res:  bson.D{{"r", int32(42)}},
			},
			"SetField": {
				expr: bson.D{{"$setField", bson.D{
					{"field", "c"},
					{"input", "$obj"},
					{"value", int32(3)},
				}}},
				res: bson.D{{"r", bson.D{{"a", int32(1)}, {"b", int32(2)}, {"c", int32(3)}}}},
			},
			"SetFieldOverwrite": {
				expr: bson.D{{"$setField", bson.D{
					{"field", "a"},
					{"input", "$obj"},
					{"value", int32(9)},
				}}},
				res: bson.D{{"r", bson.D{{"a", int32(9)}, {"b", int32(2)}}}},
			},
			"UnsetField": {
				expr: bson.D{{"$unsetField", bson.D{{"field", "b"}, {"input", "$obj"}}}},
				res:  bson.D{{"r", bson.D{{"a", int32(1)}}}},
			},
			"BinarySize": {
				expr: bson.D{{"$binarySize", "$str"}},
				res:  bson.D{{"r", int32(5)}},
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

	t.Run("PositiveRand", func(t *testing.T) {
		t.Parallel()

		pipeline := bson.A{
			bson.D{{"$project", bson.D{{"_id", false}, {"r", bson.D{{"$rand", bson.D{}}}}}}},
		}

		cursor, err := collection.Aggregate(ctx, pipeline)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var res []bson.D
		err = cursor.All(ctx, &res)
		require.NoError(t, err)
		require.Len(t, res, 1)

		v, ok := res[0].Map()["r"].(float64)
		require.True(t, ok, "$rand must return a double")
		require.GreaterOrEqual(t, v, 0.0)
		require.Less(t, v, 1.0)
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any // computed expression that must fail
		}{
			"ToIntUnparseable": {
				expr: bson.D{{"$toInt", "$str"}},
			},
			"ToObjectIdBadHex": {
				expr: bson.D{{"$toObjectId", "nothex"}},
			},
			"ConvertUnknownType": {
				expr: bson.D{{"$convert", bson.D{{"input", "$strInt"}, {"to", "widget"}}}},
			},
			"ConvertNoOnError": {
				expr: bson.D{{"$convert", bson.D{{"input", "$str"}, {"to", "int"}}}},
			},
			"SetFieldMissingValue": {
				expr: bson.D{{"$setField", bson.D{{"field", "c"}, {"input", "$obj"}}}},
			},
			"LetMissingIn": {
				expr: bson.D{{"$let", bson.D{{"vars", bson.D{{"a", int32(1)}}}}}},
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
