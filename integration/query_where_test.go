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
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestQueryWhere covers the $where query operator, which evaluates a JavaScript
// expression or function against each document (with `this` bound to the
// document) using the embedded goja engine.
func TestQueryWhere(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	docs := []any{
		bson.D{{"_id", "one"}, {"a", int32(1)}},
		bson.D{{"_id", "two"}, {"a", int32(2)}},
		bson.D{{"_id", "six"}, {"a", int32(6)}},
		bson.D{{"_id", "ten"}, {"a", int32(10)}},
	}

	_, err := collection.InsertMany(ctx, docs)
	require.NoError(t, err)

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			filter any      // $where filter value
			ids    []string // expected _ids, sorted
		}{
			"ExpressionGreater": {
				filter: "this.a > 5",
				ids:    []string{"six", "ten"},
			},
			"ExpressionEquals": {
				filter: "this.a == 2",
				ids:    []string{"two"},
			},
			"FunctionEven": {
				filter: "function() { return this.a % 2 == 0; }",
				ids:    []string{"six", "ten", "two"},
			},
			"FunctionCombined": {
				filter: "function() { return this.a >= 2 && this.a <= 6; }",
				ids:    []string{"six", "two"},
			},
		} {
			name, tc := name, tc
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				cursor, err := collection.Find(ctx, bson.D{{"$where", tc.filter}})
				require.NoError(t, err)
				defer cursor.Close(ctx)

				var res []bson.D
				err = cursor.All(ctx, &res)
				require.NoError(t, err)

				ids := make([]string, 0, len(res))
				for _, d := range res {
					ids = append(ids, d.Map()["_id"].(string))
				}

				sort.Strings(ids)
				require.Equal(t, tc.ids, ids)
			})
		}
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			filter any // invalid $where filter value
		}{
			"SyntaxError": {
				filter: "this.a >",
			},
			"RuntimeError": {
				filter: "function() { throw new Error('boom'); }",
			},
			"NonString": {
				filter: int32(1),
			},
		} {
			name, tc := name, tc
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				cursor, err := collection.Find(ctx, bson.D{{"$where", tc.filter}})
				if err == nil {
					// the error may surface while draining the cursor
					err = cursor.All(ctx, &[]bson.D{})
					cursor.Close(ctx)
				}

				require.Error(t, err)
			})
		}
	})
}
