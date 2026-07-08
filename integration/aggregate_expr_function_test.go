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

// TestAggregateExprFunction covers the $function aggregation expression
// operator, which runs a user-supplied JavaScript function against the
// evaluated argument expressions using the embedded goja engine.
func TestAggregateExprFunction(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	doc := bson.D{
		{"_id", "doc1"},
		{"x", int32(3)},
		{"y", int32(4)},
		{"name", "ferret"},
	}

	_, err := collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			expr any    // computed expression for field "r"
			res  bson.D // expected single result document
		}{
			"Add": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function(a, b) { return a + b; }"},
					{"args", bson.A{"$x", "$y"}},
					{"lang", "js"},
				}}},
				res: bson.D{{"r", int32(7)}},
			},
			"Multiply": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function(a, b) { return a * b; }"},
					{"args", bson.A{"$x", "$y"}},
					{"lang", "js"},
				}}},
				res: bson.D{{"r", int32(12)}},
			},
			"StringConcat": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function(s) { return s + '!'; }"},
					{"args", bson.A{"$name"}},
					{"lang", "js"},
				}}},
				res: bson.D{{"r", "ferret!"}},
			},
			"NoArgs": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function() { return 42; }"},
					{"args", bson.A{}},
					{"lang", "js"},
				}}},
				res: bson.D{{"r", int32(42)}},
			},
			"BoolResult": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function(a, b) { return a < b; }"},
					{"args", bson.A{"$x", "$y"}},
					{"lang", "js"},
				}}},
				res: bson.D{{"r", true}},
			},
		} {
			name, tc := name, tc
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
			"MissingBody": {
				expr: bson.D{{"$function", bson.D{
					{"args", bson.A{"$x"}},
					{"lang", "js"},
				}}},
			},
			"WrongLang": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function() { return 1; }"},
					{"args", bson.A{}},
					{"lang", "python"},
				}}},
			},
			"BodyThrows": {
				expr: bson.D{{"$function", bson.D{
					{"body", "function() { throw new Error('boom'); }"},
					{"args", bson.A{}},
					{"lang", "js"},
				}}},
			},
			"BodyNotFunction": {
				expr: bson.D{{"$function", bson.D{
					{"body", "123"},
					{"args", bson.A{}},
					{"lang", "js"},
				}}},
			},
		} {
			name, tc := name, tc
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
