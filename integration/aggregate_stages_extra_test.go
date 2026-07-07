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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// TestAggregateStagesExtra covers the aggregation stages added on top of
// FerretDB v1.24.2: $replaceRoot, $replaceWith, $sortByCount and $sample.
func TestAggregateStagesExtra(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	docs := []any{
		bson.D{
			{"_id", "doc1"},
			{"category", "a"},
			{"sub", bson.D{{"x", int32(1)}, {"y", int32(2)}}},
			{"name", "alice"},
		},
		bson.D{
			{"_id", "doc2"},
			{"category", "a"},
			{"sub", bson.D{{"x", int32(3)}, {"y", int32(4)}}},
			{"name", "bob"},
		},
		bson.D{
			{"_id", "doc3"},
			{"category", "b"},
			{"sub", bson.D{{"x", int32(5)}, {"y", int32(6)}}},
			{"name", "carol"},
		},
	}

	_, err := collection.InsertMany(ctx, docs)
	require.NoError(t, err)

	t.Run("ReplaceRoot", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$match", bson.D{{"_id", "doc1"}}}},
			bson.D{{"$replaceRoot", bson.D{{"newRoot", "$sub"}}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 1)
		assert.Equal(t, bson.D{{"x", int32(1)}, {"y", int32(2)}}, res[0])
	})

	t.Run("ReplaceRootComputed", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$match", bson.D{{"_id", "doc1"}}}},
			bson.D{{"$replaceRoot", bson.D{{"newRoot", bson.D{{"total", bson.D{{"$add", bson.A{"$sub.x", "$sub.y"}}}}}}}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 1)
		assert.Equal(t, bson.D{{"total", int32(3)}}, res[0])
	})

	t.Run("ReplaceWith", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$match", bson.D{{"_id", "doc2"}}}},
			bson.D{{"$replaceWith", "$sub"}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 1)
		assert.Equal(t, bson.D{{"x", int32(3)}, {"y", int32(4)}}, res[0])
	})

	t.Run("SortByCount", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$sortByCount", "$category"}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		// category "a" appears twice, "b" once; sorted by count descending.
		require.Len(t, res, 2)
		assert.Equal(t, bson.D{{"_id", "a"}, {"count", int32(2)}}, res[0])
		assert.Equal(t, bson.D{{"_id", "b"}, {"count", int32(1)}}, res[1])
	})

	t.Run("Sample", func(t *testing.T) {
		t.Parallel()

		const size = 2

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$sample", bson.D{{"size", int32(size)}}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, size)

		known := map[string]struct{}{"doc1": {}, "doc2": {}, "doc3": {}}
		seen := map[string]struct{}{}

		for _, doc := range res {
			m := doc.Map()
			id, ok := m["_id"].(string)
			require.True(t, ok, "sampled document must have a string _id")

			_, isKnown := known[id]
			assert.True(t, isKnown, "sampled document %q must be from the input set", id)

			_, dup := seen[id]
			assert.False(t, dup, "sampled document %q must not be returned twice", id)
			seen[id] = struct{}{}
		}
	})

	t.Run("SampleAll", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$sample", bson.D{{"size", int32(10)}}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		// size >= number of documents returns all of them.
		require.Len(t, res, 3)
	})

	t.Run("ReplaceRootNonDocumentError", func(t *testing.T) {
		t.Parallel()

		// "$name" evaluates to a string, not a document.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$replaceRoot", bson.D{{"newRoot", "$name"}}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("SampleNegativeSizeError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$sample", bson.D{{"size", int32(-1)}}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("SampleNonIntegerSizeError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$sample", bson.D{{"size", 1.5}}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("SortByCountInvalidExpressionError", func(t *testing.T) {
		t.Parallel()

		// an object that is not a valid expression operator.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$sortByCount", bson.D{{"category", int32(1)}}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})
}
