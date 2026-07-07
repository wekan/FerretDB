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

// TestAggregateExprBool covers the comparison, boolean and conditional
// aggregation expression operators added on top of FerretDB v1.24.2:
// $cmp, $gt, $gte, $lt, $lte, $and, $not, $cond, $switch and $allElementsTrue.
func TestAggregateExprBool(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	doc := bson.D{
		{"_id", "doc1"},
		{"a", int32(5)},
		{"b", int32(5)},
		{"c", int32(7)},
		{"arr", bson.A{int32(1), int32(2), int32(3)}},
		{"flags", bson.A{true, true, true}},
		{"mixed", bson.A{true, false, true}},
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
			"CmpLess": {
				expr: bson.D{{"$cmp", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", int32(-1)}},
			},
			"CmpEqual": {
				expr: bson.D{{"$cmp", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", int32(0)}},
			},
			"CmpGreater": {
				expr: bson.D{{"$cmp", bson.A{"$c", "$a"}}},
				res:  bson.D{{"r", int32(1)}},
			},
			"GtTrue": {
				expr: bson.D{{"$gt", bson.A{"$c", "$a"}}},
				res:  bson.D{{"r", true}},
			},
			"GtFalse": {
				expr: bson.D{{"$gt", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", false}},
			},
			"GteTrueEqual": {
				expr: bson.D{{"$gte", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", true}},
			},
			"GteFalse": {
				expr: bson.D{{"$gte", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", false}},
			},
			"LtTrue": {
				expr: bson.D{{"$lt", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", true}},
			},
			"LtFalse": {
				expr: bson.D{{"$lt", bson.A{"$c", "$a"}}},
				res:  bson.D{{"r", false}},
			},
			"LteTrueEqual": {
				expr: bson.D{{"$lte", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", true}},
			},
			"LteFalse": {
				expr: bson.D{{"$lte", bson.A{"$c", "$a"}}},
				res:  bson.D{{"r", false}},
			},
			"AndTrue": {
				expr: bson.D{{"$and", bson.A{true, "$a", bson.D{{"$gt", bson.A{"$c", "$a"}}}}}},
				res:  bson.D{{"r", true}},
			},
			"AndFalse": {
				expr: bson.D{{"$and", bson.A{true, "$nullField", "$a"}}},
				res:  bson.D{{"r", false}},
			},
			"AndZeroFalse": {
				expr: bson.D{{"$and", bson.A{true, int32(0)}}},
				res:  bson.D{{"r", false}},
			},
			"NotTrue": {
				expr: bson.D{{"$not", bson.A{false}}},
				res:  bson.D{{"r", true}},
			},
			"NotFalse": {
				expr: bson.D{{"$not", bson.A{"$a"}}},
				res:  bson.D{{"r", false}},
			},
			"NotSingleArg": {
				expr: bson.D{{"$not", "$nullField"}},
				res:  bson.D{{"r", true}},
			},
			"CondObjectThen": {
				expr: bson.D{{"$cond", bson.D{
					{"if", bson.D{{"$gt", bson.A{"$c", "$a"}}}},
					{"then", "bigger"},
					{"else", "smaller"},
				}}},
				res: bson.D{{"r", "bigger"}},
			},
			"CondObjectElse": {
				expr: bson.D{{"$cond", bson.D{
					{"if", bson.D{{"$lt", bson.A{"$c", "$a"}}}},
					{"then", "bigger"},
					{"else", "smaller"},
				}}},
				res: bson.D{{"r", "smaller"}},
			},
			"CondArrayThen": {
				expr: bson.D{{"$cond", bson.A{"$a", int32(1), int32(2)}}},
				res:  bson.D{{"r", int32(1)}},
			},
			"CondArrayElse": {
				expr: bson.D{{"$cond", bson.A{"$nullField", int32(1), int32(2)}}},
				res:  bson.D{{"r", int32(2)}},
			},
			"SwitchMatch": {
				expr: bson.D{{"$switch", bson.D{
					{"branches", bson.A{
						bson.D{{"case", bson.D{{"$lt", bson.A{"$c", "$a"}}}}, {"then", "low"}},
						bson.D{{"case", bson.D{{"$gt", bson.A{"$c", "$a"}}}}, {"then", "high"}},
					}},
					{"default", "none"},
				}}},
				res: bson.D{{"r", "high"}},
			},
			"SwitchDefault": {
				expr: bson.D{{"$switch", bson.D{
					{"branches", bson.A{
						bson.D{{"case", false}, {"then", "low"}},
						bson.D{{"case", "$nullField"}, {"then", "high"}},
					}},
					{"default", "none"},
				}}},
				res: bson.D{{"r", "none"}},
			},
			"AllElementsTrueTrue": {
				expr: bson.D{{"$allElementsTrue", bson.A{"$flags"}}},
				res:  bson.D{{"r", true}},
			},
			"AllElementsTrueFalse": {
				expr: bson.D{{"$allElementsTrue", bson.A{"$mixed"}}},
				res:  bson.D{{"r", false}},
			},
			"AllElementsTrueEmpty": {
				expr: bson.D{{"$allElementsTrue", bson.A{bson.A{}}}},
				res:  bson.D{{"r", true}},
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

	t.Run("PositiveAddFields", func(t *testing.T) {
		t.Parallel()

		pipeline := bson.A{
			bson.D{{"$addFields", bson.D{{"r", bson.D{{"$and", bson.A{"$a", "$b"}}}}}}},
			bson.D{{"$project", bson.D{{"_id", false}, {"r", true}}}},
		}

		cursor, err := collection.Aggregate(ctx, pipeline)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var res []bson.D
		err = cursor.All(ctx, &res)
		require.NoError(t, err)
		require.Equal(t, []bson.D{{{"r", true}}}, res)
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any // computed expression that must fail
		}{
			"CmpWrongArgsLen": {
				expr: bson.D{{"$cmp", bson.A{"$a"}}},
			},
			"GtWrongArgsLen": {
				expr: bson.D{{"$gt", bson.A{"$a", "$b", "$c"}}},
			},
			"NotWrongArgsLen": {
				expr: bson.D{{"$not", bson.A{"$a", "$b"}}},
			},
			"SwitchNoMatchNoDefault": {
				expr: bson.D{{"$switch", bson.D{
					{"branches", bson.A{
						bson.D{{"case", false}, {"then", "low"}},
						bson.D{{"case", "$nullField"}, {"then", "high"}},
					}},
				}}},
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
