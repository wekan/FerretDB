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

// TestAggregateFacet covers the $facet aggregation stage.
func TestAggregateFacet(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	docs := []any{
		bson.D{{"_id", "doc1"}, {"category", "a"}, {"name", "alice"}, {"price", int32(10)}},
		bson.D{{"_id", "doc2"}, {"category", "a"}, {"name", "bob"}, {"price", int32(30)}},
		bson.D{{"_id", "doc3"}, {"category", "b"}, {"name", "carol"}, {"price", int32(20)}},
		bson.D{{"_id", "doc4"}, {"category", "b"}, {"name", "dave"}, {"price", int32(40)}},
	}

	_, err := collection.InsertMany(ctx, docs)
	require.NoError(t, err)

	t.Run("TwoSubPipelines", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$facet", bson.D{
				{"counts", bson.A{
					bson.D{{"$sortByCount", "$category"}},
				}},
				{"top", bson.A{
					bson.D{{"$sort", bson.D{{"price", int32(-1)}}}},
					bson.D{{"$limit", int32(2)}},
					bson.D{{"$project", bson.D{{"_id", int32(0)}, {"name", int32(1)}}}},
				}},
			}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 1)

		m := res[0].Map()

		// counts: both categories have 2 documents; ties broken by _id ascending.
		assert.Equal(t, bson.A{
			bson.D{{"_id", "a"}, {"count", int32(2)}},
			bson.D{{"_id", "b"}, {"count", int32(2)}},
		}, m["counts"])

		// top: two highest prices are dave(40) and bob(30).
		assert.Equal(t, bson.A{
			bson.D{{"name", "dave"}},
			bson.D{{"name", "bob"}},
		}, m["top"])
	})

	t.Run("MatchGroupSubPipeline", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$facet", bson.D{
				{"byCategoryA", bson.A{
					bson.D{{"$match", bson.D{{"category", "a"}}}},
					bson.D{{"$group", bson.D{
						{"_id", "$category"},
						{"total", bson.D{{"$sum", "$price"}}},
					}}},
				}},
			}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 1)

		m := res[0].Map()

		// category "a" documents have prices 10 and 30, summing to 40.
		assert.Equal(t, bson.A{
			bson.D{{"_id", "a"}, {"total", int32(40)}},
		}, m["byCategoryA"])
	})

	t.Run("NonObjectError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$facet", "not-an-object"}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("FieldNotArrayError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$facet", bson.D{
				{"bad", bson.D{{"$sort", bson.D{{"price", int32(1)}}}}},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("DisallowedStageError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$facet", bson.D{
				{"bad", bson.A{
					bson.D{{"$out", "somewhere"}},
				}},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("NestedFacetError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$facet", bson.D{
				{"bad", bson.A{
					bson.D{{"$facet", bson.D{
						{"inner", bson.A{}},
					}}},
				}},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})
}
