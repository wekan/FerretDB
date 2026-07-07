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

// TestAggregateExprArithmetic covers the arithmetic aggregation expression
// operators added on top of FerretDB v1.24.2: $add, $subtract, $multiply,
// $divide, $mod, $abs, $ceil, $floor, $trunc, $round, $pow, $sqrt, $exp, $ln,
// $log, $max, $min and $avg.
func TestAggregateExprArithmetic(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	doc := bson.D{
		{"_id", "doc1"},
		{"a", int32(6)},
		{"b", int32(3)},
		{"c", 2.5},
		{"d", int64(10)},
		{"neg", int32(-7)},
		{"arr", bson.A{int32(3), int32(1), int32(2)}},
		{"nums", bson.A{int32(1), int32(2), int32(3), int32(4)}},
		{"str", "abc"},
		{"zero", int32(0)},
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
			"AddInts": {
				expr: bson.D{{"$add", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", int32(9)}},
			},
			"AddIntDouble": {
				expr: bson.D{{"$add", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", 8.5}},
			},
			"AddIntLong": {
				expr: bson.D{{"$add", bson.A{"$a", "$d"}}},
				res:  bson.D{{"r", int64(16)}},
			},
			"SubtractInts": {
				expr: bson.D{{"$subtract", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", int32(3)}},
			},
			"SubtractIntDouble": {
				expr: bson.D{{"$subtract", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", 3.5}},
			},
			"MultiplyInts": {
				expr: bson.D{{"$multiply", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", int32(18)}},
			},
			"MultiplyIntDouble": {
				expr: bson.D{{"$multiply", bson.A{"$a", "$c"}}},
				res:  bson.D{{"r", 15.0}},
			},
			"Divide": {
				expr: bson.D{{"$divide", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", 2.0}},
			},
			"Mod": {
				expr: bson.D{{"$mod", bson.A{"$a", "$b"}}},
				res:  bson.D{{"r", int32(0)}},
			},
			"ModNonZero": {
				expr: bson.D{{"$mod", bson.A{"$a", int32(4)}}},
				res:  bson.D{{"r", int32(2)}},
			},
			"AbsInt": {
				expr: bson.D{{"$abs", "$neg"}},
				res:  bson.D{{"r", int32(7)}},
			},
			"AbsDouble": {
				expr: bson.D{{"$abs", -2.5}},
				res:  bson.D{{"r", 2.5}},
			},
			"CeilInt": {
				expr: bson.D{{"$ceil", "$a"}},
				res:  bson.D{{"r", int32(6)}},
			},
			"CeilDouble": {
				expr: bson.D{{"$ceil", "$c"}},
				res:  bson.D{{"r", 3.0}},
			},
			"Floor": {
				expr: bson.D{{"$floor", "$c"}},
				res:  bson.D{{"r", 2.0}},
			},
			"Trunc": {
				expr: bson.D{{"$trunc", 2.9}},
				res:  bson.D{{"r", 2.0}},
			},
			"Round": {
				expr: bson.D{{"$round", 2.567}},
				res:  bson.D{{"r", 3.0}},
			},
			"RoundPlace": {
				expr: bson.D{{"$round", bson.A{2.567, int32(1)}}},
				res:  bson.D{{"r", 2.6}},
			},
			"PowInts": {
				expr: bson.D{{"$pow", bson.A{int32(2), int32(3)}}},
				res:  bson.D{{"r", int32(8)}},
			},
			"PowDouble": {
				expr: bson.D{{"$pow", bson.A{2.0, int32(3)}}},
				res:  bson.D{{"r", 8.0}},
			},
			"Sqrt": {
				expr: bson.D{{"$sqrt", int32(25)}},
				res:  bson.D{{"r", 5.0}},
			},
			"Exp": {
				expr: bson.D{{"$exp", int32(0)}},
				res:  bson.D{{"r", 1.0}},
			},
			"Ln": {
				expr: bson.D{{"$ln", 1.0}},
				res:  bson.D{{"r", 0.0}},
			},
			"Log": {
				expr: bson.D{{"$log", bson.A{int32(8), int32(2)}}},
				res:  bson.D{{"r", 3.0}},
			},
			"MaxArray": {
				expr: bson.D{{"$max", "$arr"}},
				res:  bson.D{{"r", int32(3)}},
			},
			"MinArray": {
				expr: bson.D{{"$min", "$arr"}},
				res:  bson.D{{"r", int32(1)}},
			},
			"AvgArray": {
				expr: bson.D{{"$avg", "$nums"}},
				res:  bson.D{{"r", 2.5}},
			},
			"MaxArgs": {
				expr: bson.D{{"$max", bson.A{"$a", "$b", "$c"}}},
				res:  bson.D{{"r", int32(6)}},
			},
			"AvgArgs": {
				expr: bson.D{{"$avg", bson.A{int32(2), int32(4)}}},
				res:  bson.D{{"r", 3.0}},
			},
			"MaxEmpty": {
				expr: bson.D{{"$max", bson.A{bson.A{}}}},
				res:  bson.D{{"r", nil}},
			},
			"AddNull": {
				expr: bson.D{{"$add", bson.A{"$a", "$nullField"}}},
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
			"DivideByZero": {
				expr: bson.D{{"$divide", bson.A{"$a", "$zero"}}},
			},
			"ModByZero": {
				expr: bson.D{{"$mod", bson.A{"$a", "$zero"}}},
			},
			"AbsNonNumeric": {
				expr: bson.D{{"$abs", "$str"}},
			},
			"AddNonNumeric": {
				expr: bson.D{{"$add", bson.A{"$a", "$str"}}},
			},
			"SubtractWrongArgsLen": {
				expr: bson.D{{"$subtract", bson.A{"$a"}}},
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
