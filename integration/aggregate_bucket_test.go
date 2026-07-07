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

// TestAggregateBucket covers the $bucket and $bucketAuto aggregation stages.
func TestAggregateBucket(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	docs := []any{
		bson.D{{"_id", "d0"}, {"value", int32(0)}, {"price", int32(10)}},
		bson.D{{"_id", "d1"}, {"value", int32(1)}, {"price", int32(20)}},
		bson.D{{"_id", "d2"}, {"value", int32(2)}, {"price", int32(30)}},
		bson.D{{"_id", "d3"}, {"value", int32(3)}, {"price", int32(40)}},
		bson.D{{"_id", "d4"}, {"value", int32(4)}, {"price", int32(50)}},
		bson.D{{"_id", "d5"}, {"value", int32(5)}, {"price", int32(60)}},
		bson.D{{"_id", "d6"}, {"value", int32(6)}, {"price", int32(70)}},
		bson.D{{"_id", "d7"}, {"value", int32(7)}, {"price", int32(80)}},
		bson.D{{"_id", "d8"}, {"value", int32(8)}, {"price", int32(90)}},
		bson.D{{"_id", "d9"}, {"value", int32(9)}, {"price", int32(100)}},
	}

	_, err := collection.InsertMany(ctx, docs)
	require.NoError(t, err)

	t.Run("BucketDefaultOutput", func(t *testing.T) {
		t.Parallel()

		// buckets [0,3), [3,6), [6,10); default output counts documents.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucket", bson.D{
				{"groupBy", "$value"},
				{"boundaries", bson.A{int32(0), int32(3), int32(6), int32(10)}},
			}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 3)
		assert.Equal(t, bson.D{{"_id", int32(0)}, {"count", int32(3)}}, res[0])
		assert.Equal(t, bson.D{{"_id", int32(3)}, {"count", int32(3)}}, res[1])
		assert.Equal(t, bson.D{{"_id", int32(6)}, {"count", int32(4)}}, res[2])
	})

	t.Run("BucketDefaultBucket", func(t *testing.T) {
		t.Parallel()

		// values 6..9 fall outside the boundaries and go to the "Other" bucket.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucket", bson.D{
				{"groupBy", "$value"},
				{"boundaries", bson.A{int32(0), int32(3), int32(6)}},
				{"default", "Other"},
			}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 3)
		assert.Equal(t, bson.D{{"_id", int32(0)}, {"count", int32(3)}}, res[0])
		assert.Equal(t, bson.D{{"_id", int32(3)}, {"count", int32(3)}}, res[1])
		// the default bucket is always last.
		assert.Equal(t, bson.D{{"_id", "Other"}, {"count", int32(4)}}, res[2])
	})

	t.Run("BucketExplicitOutput", func(t *testing.T) {
		t.Parallel()

		// explicit output summing the "price" field per bucket.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucket", bson.D{
				{"groupBy", "$value"},
				{"boundaries", bson.A{int32(0), int32(5), int32(10)}},
				{"output", bson.D{
					{"total", bson.D{{"$sum", "$price"}}},
					{"count", bson.D{{"$sum", int32(1)}}},
				}},
			}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 2)
		// prices 10+20+30+40+50 = 150; 60+70+80+90+100 = 400.
		assert.Equal(t, bson.D{{"_id", int32(0)}, {"total", int32(150)}, {"count", int32(5)}}, res[0])
		assert.Equal(t, bson.D{{"_id", int32(5)}, {"total", int32(400)}, {"count", int32(5)}}, res[1])
	})

	t.Run("BucketAuto", func(t *testing.T) {
		t.Parallel()

		// 10 distinct values split into 4 buckets: sizes 3,3,2,2.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucketAuto", bson.D{
				{"groupBy", "$value"},
				{"buckets", int32(4)},
			}}},
		})
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		require.Len(t, res, 4)

		assert.Equal(t, bson.D{{"_id", bson.D{{"min", int32(0)}, {"max", int32(3)}}}, {"count", int32(3)}}, res[0])
		assert.Equal(t, bson.D{{"_id", bson.D{{"min", int32(3)}, {"max", int32(6)}}}, {"count", int32(3)}}, res[1])
		assert.Equal(t, bson.D{{"_id", bson.D{{"min", int32(6)}, {"max", int32(8)}}}, {"count", int32(2)}}, res[2])
		assert.Equal(t, bson.D{{"_id", bson.D{{"min", int32(8)}, {"max", int32(9)}}}, {"count", int32(2)}}, res[3])
	})

	t.Run("BucketBoundariesTooFewError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucket", bson.D{
				{"groupBy", "$value"},
				{"boundaries", bson.A{int32(0)}},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("BucketBoundariesNotAscendingError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucket", bson.D{
				{"groupBy", "$value"},
				{"boundaries", bson.A{int32(0), int32(5), int32(3)}},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("BucketNoMatchingBucketError", func(t *testing.T) {
		t.Parallel()

		// value 9 falls outside [0,5) and no default is specified.
		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucket", bson.D{
				{"groupBy", "$value"},
				{"boundaries", bson.A{int32(0), int32(5)}},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})

	t.Run("BucketAutoNonPositiveBucketsError", func(t *testing.T) {
		t.Parallel()

		cursor, err := collection.Aggregate(ctx, bson.A{
			bson.D{{"$bucketAuto", bson.D{
				{"groupBy", "$value"},
				{"buckets", int32(0)},
			}}},
		})

		if err == nil {
			err = cursor.All(ctx, &[]bson.D{})
			cursor.Close(ctx)
		}

		require.Error(t, err)
	})
}
