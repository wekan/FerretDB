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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestAggregateExprTrig covers the trigonometric, hyperbolic, angle-conversion
// and $log10 aggregation expression operators added on top of FerretDB v1:
// $sin, $cos, $tan, $asin, $acos, $atan, $atan2, $sinh, $cosh, $tanh, $asinh,
// $acosh, $atanh, $degreesToRadians, $radiansToDegrees and $log10.
func TestAggregateExprTrig(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	doc := bson.D{
		{"_id", "doc1"},
		{"zero", int32(0)},
		{"one", int32(1)},
		{"hundred", int32(100)},
		{"pi", math.Pi},
		{"str", "abc"},
		{"nullField", nil},
	}

	_, err := collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		// exactCases assert an exact double result.
		for name, tc := range map[string]struct {
			expr any
			res  float64
		}{
			"Sin0":               {expr: bson.D{{"$sin", "$zero"}}, res: 0.0},
			"Cos0":               {expr: bson.D{{"$cos", "$zero"}}, res: 1.0},
			"Tan0":               {expr: bson.D{{"$tan", "$zero"}}, res: 0.0},
			"Asin0":              {expr: bson.D{{"$asin", "$zero"}}, res: 0.0},
			"Acos1":              {expr: bson.D{{"$acos", "$one"}}, res: 0.0},
			"Atan0":              {expr: bson.D{{"$atan", "$zero"}}, res: 0.0},
			"Sinh0":              {expr: bson.D{{"$sinh", "$zero"}}, res: 0.0},
			"Cosh0":              {expr: bson.D{{"$cosh", "$zero"}}, res: 1.0},
			"Tanh0":              {expr: bson.D{{"$tanh", "$zero"}}, res: 0.0},
			"Asinh0":             {expr: bson.D{{"$asinh", "$zero"}}, res: 0.0},
			"Acosh1":             {expr: bson.D{{"$acosh", "$one"}}, res: 0.0},
			"Atanh0":             {expr: bson.D{{"$atanh", "$zero"}}, res: 0.0},
			"Log10Hundred":       {expr: bson.D{{"$log10", "$hundred"}}, res: 2.0},
			"RadiansToDegreesPi": {expr: bson.D{{"$radiansToDegrees", "$pi"}}, res: 180.0},
			"DegreesToRadians0":  {expr: bson.D{{"$degreesToRadians", "$zero"}}, res: 0.0},
			"Atan2Zero":          {expr: bson.D{{"$atan2", bson.A{"$zero", "$one"}}}, res: 0.0},
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
				require.Equal(t, []bson.D{{{"r", tc.res}}}, res)
			})
		}

		// deltaCases assert an irrational double result within a tolerance.
		for name, tc := range map[string]struct {
			expr any
			res  float64
		}{
			"Sin1":                {expr: bson.D{{"$sin", "$one"}}, res: math.Sin(1)},
			"Cos1":                {expr: bson.D{{"$cos", "$one"}}, res: math.Cos(1)},
			"DegreesToRadians180": {expr: bson.D{{"$degreesToRadians", int32(180)}}, res: math.Pi},
			"Atan2OneOne":         {expr: bson.D{{"$atan2", bson.A{"$one", "$one"}}}, res: math.Atan2(1, 1)},
			"AsinhHalf":           {expr: bson.D{{"$asinh", 0.5}}, res: math.Asinh(0.5)},
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
				require.Len(t, res, 1)
				require.Len(t, res[0], 1)

				actual, ok := res[0][0].Value.(float64)
				require.True(t, ok, "expected float64 result, got %T", res[0][0].Value)
				assert.InDelta(t, tc.res, actual, 1e-9)
			})
		}

		// nullCases assert null input yields null.
		for name, tc := range map[string]struct {
			expr any
		}{
			"SinNull":   {expr: bson.D{{"$sin", "$nullField"}}},
			"Atan2Null": {expr: bson.D{{"$atan2", bson.A{"$nullField", "$one"}}}},
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
				require.Equal(t, []bson.D{{{"r", nil}}}, res)
			})
		}
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any
		}{
			"SinNonNumeric":    {expr: bson.D{{"$sin", "$str"}}},
			"Log10NonNumeric":  {expr: bson.D{{"$log10", "$str"}}},
			"Atan2NonNumeric":  {expr: bson.D{{"$atan2", bson.A{"$one", "$str"}}}},
			"Atan2TooFewArgs":  {expr: bson.D{{"$atan2", bson.A{"$one"}}}},
			"Atan2TooManyArgs": {expr: bson.D{{"$atan2", bson.A{int32(1), int32(2), int32(3)}}}},
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
