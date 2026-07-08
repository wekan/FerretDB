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

// TestAggregateSetWindowFields covers the $setWindowFields aggregation stage and its
// window operators added on top of FerretDB v1: $rank, $denseRank, $documentNumber,
// $shift, and the window accumulators $sum, $avg, $min, $max, $count, $push, $first,
// $last, $stdDevPop and $stdDevSamp.
func TestAggregateSetWindowFields(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	// Two partitions (category a and b). qty is unique within each partition so that
	// document-order-sensitive operators are deterministic.
	docs := []any{
		bson.D{{"_id", "a1"}, {"category", "a"}, {"qty", int32(10)}},
		bson.D{{"_id", "a2"}, {"category", "a"}, {"qty", int32(20)}},
		bson.D{{"_id", "a3"}, {"category", "a"}, {"qty", int32(30)}},
		bson.D{{"_id", "b1"}, {"category", "b"}, {"qty", int32(5)}},
		bson.D{{"_id", "b2"}, {"category", "b"}, {"qty", int32(15)}},
	}

	_, err := collection.InsertMany(ctx, docs)
	require.NoError(t, err)

	// resultMap runs the pipeline and returns a map of _id -> value of the given field.
	resultMap := func(t *testing.T, pipeline bson.A, field string) map[string]any {
		t.Helper()

		cursor, err := collection.Aggregate(ctx, pipeline)
		require.NoError(t, err)

		defer cursor.Close(ctx)

		var res []bson.D
		require.NoError(t, cursor.All(ctx, &res))

		out := make(map[string]any, len(res))

		for _, doc := range res {
			m := doc.Map()

			id, ok := m["_id"].(string)
			require.True(t, ok, "unexpected _id type in %v", doc)

			out[id] = m[field]
		}

		return out
	}

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()

		t.Run("SumUnboundedPartition", func(t *testing.T) {
			t.Parallel()

			// Default window covers the whole partition.
			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"total", bson.D{{"$sum", "$qty"}}}}},
			}}}}

			got := resultMap(t, pipeline, "total")
			assert.Equal(t, map[string]any{
				"a1": int32(60), "a2": int32(60), "a3": int32(60),
				"b1": int32(20), "b2": int32(20),
			}, got)
		})

		t.Run("AvgUnboundedPartition", func(t *testing.T) {
			t.Parallel()

			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"a", bson.D{{"$avg", "$qty"}}}}},
			}}}}

			got := resultMap(t, pipeline, "a")
			assert.Equal(t, map[string]any{
				"a1": 20.0, "a2": 20.0, "a3": 20.0,
				"b1": 10.0, "b2": 10.0,
			}, got)
		})

		t.Run("RunningSum", func(t *testing.T) {
			t.Parallel()

			// unbounded..current window yields a cumulative sum within the partition.
			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"running", bson.D{
					{"$sum", "$qty"},
					{"window", bson.D{{"documents", bson.A{"unbounded", "current"}}}},
				}}}},
			}}}}

			got := resultMap(t, pipeline, "running")
			assert.Equal(t, map[string]any{
				"a1": int32(10), "a2": int32(30), "a3": int32(60),
				"b1": int32(5), "b2": int32(20),
			}, got)
		})

		t.Run("MovingSumBounded", func(t *testing.T) {
			t.Parallel()

			// window [-1, 0] sums the current and previous document.
			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"moving", bson.D{
					{"$sum", "$qty"},
					{"window", bson.D{{"documents", bson.A{int32(-1), int32(0)}}}},
				}}}},
			}}}}

			got := resultMap(t, pipeline, "moving")
			assert.Equal(t, map[string]any{
				"a1": int32(10), "a2": int32(30), "a3": int32(50),
				"b1": int32(5), "b2": int32(20),
			}, got)
		})

		t.Run("Rank", func(t *testing.T) {
			t.Parallel()

			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"r", bson.D{{"$rank", bson.D{}}}}}},
			}}}}

			got := resultMap(t, pipeline, "r")
			assert.Equal(t, map[string]any{
				"a1": int32(1), "a2": int32(2), "a3": int32(3),
				"b1": int32(1), "b2": int32(2),
			}, got)
		})

		t.Run("DocumentNumber", func(t *testing.T) {
			t.Parallel()

			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"n", bson.D{{"$documentNumber", bson.D{}}}}}},
			}}}}

			got := resultMap(t, pipeline, "n")
			assert.Equal(t, map[string]any{
				"a1": int32(1), "a2": int32(2), "a3": int32(3),
				"b1": int32(1), "b2": int32(2),
			}, got)
		})

		t.Run("ShiftWithDefault", func(t *testing.T) {
			t.Parallel()

			// Previous document's qty, default -1 when out of range.
			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"prev", bson.D{{"$shift", bson.D{
					{"output", "$qty"},
					{"by", int32(-1)},
					{"default", int32(-1)},
				}}}}}},
			}}}}

			got := resultMap(t, pipeline, "prev")
			assert.Equal(t, map[string]any{
				"a1": int32(-1), "a2": int32(10), "a3": int32(20),
				"b1": int32(-1), "b2": int32(5),
			}, got)
		})

		t.Run("ShiftNoDefault", func(t *testing.T) {
			t.Parallel()

			// Next document's qty, null when out of range.
			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{{"next", bson.D{{"$shift", bson.D{
					{"output", "$qty"},
					{"by", int32(1)},
				}}}}}},
			}}}}

			got := resultMap(t, pipeline, "next")
			assert.Equal(t, map[string]any{
				"a1": int32(20), "a2": int32(30), "a3": nil,
				"b1": int32(15), "b2": nil,
			}, got)
		})

		t.Run("MinMaxPush", func(t *testing.T) {
			t.Parallel()

			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$category"},
				{"sortBy", bson.D{{"qty", int32(1)}}},
				{"output", bson.D{
					{"lo", bson.D{{"$min", "$qty"}}},
					{"hi", bson.D{{"$max", "$qty"}}},
					{"all", bson.D{{"$push", "$qty"}}},
				}},
			}}}}

			gotMin := resultMap(t, pipeline, "lo")
			assert.Equal(t, map[string]any{
				"a1": int32(10), "a2": int32(10), "a3": int32(10),
				"b1": int32(5), "b2": int32(5),
			}, gotMin)

			gotMax := resultMap(t, pipeline, "hi")
			assert.Equal(t, map[string]any{
				"a1": int32(30), "a2": int32(30), "a3": int32(30),
				"b1": int32(15), "b2": int32(15),
			}, gotMax)

			gotPush := resultMap(t, pipeline, "all")
			assert.Equal(t, bson.A{int32(10), int32(20), int32(30)}, gotPush["a1"])
			assert.Equal(t, bson.A{int32(5), int32(15)}, gotPush["b1"])
		})

		t.Run("DenseRankWithTies", func(t *testing.T) {
			t.Parallel()

			ctxT, coll := setup.Setup(t)

			tieDocs := []any{
				bson.D{{"_id", "t1"}, {"g", "x"}, {"score", int32(10)}},
				bson.D{{"_id", "t2"}, {"g", "x"}, {"score", int32(10)}},
				bson.D{{"_id", "t3"}, {"g", "x"}, {"score", int32(20)}},
			}
			_, err := coll.InsertMany(ctxT, tieDocs)
			require.NoError(t, err)

			pipeline := bson.A{bson.D{{"$setWindowFields", bson.D{
				{"partitionBy", "$g"},
				{"sortBy", bson.D{{"score", int32(1)}}},
				{"output", bson.D{
					{"rank", bson.D{{"$rank", bson.D{}}}},
					{"dense", bson.D{{"$denseRank", bson.D{}}}},
				}},
			}}}}

			cursor, err := coll.Aggregate(ctxT, pipeline)
			require.NoError(t, err)

			defer cursor.Close(ctxT)

			var res []bson.D
			require.NoError(t, cursor.All(ctxT, &res))

			rank := make(map[string]any)
			dense := make(map[string]any)

			for _, d := range res {
				m := d.Map()
				id := m["_id"].(string)
				rank[id] = m["rank"]
				dense[id] = m["dense"]
			}

			// Tied documents share rank 1; the next rank skips to 3 for $rank but is 2
			// for $denseRank.
			assert.Equal(t, map[string]any{"t1": int32(1), "t2": int32(1), "t3": int32(3)}, rank)
			assert.Equal(t, map[string]any{"t1": int32(1), "t2": int32(1), "t3": int32(2)}, dense)
		})
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			swf any // value of the $setWindowFields stage
		}{
			"RankWithoutSortBy": {
				swf: bson.D{
					{"partitionBy", "$category"},
					{"output", bson.D{{"r", bson.D{{"$rank", bson.D{}}}}}},
				},
			},
			"ShiftWithoutSortBy": {
				swf: bson.D{
					{"partitionBy", "$category"},
					{"output", bson.D{{"s", bson.D{{"$shift", bson.D{
						{"output", "$qty"}, {"by", int32(1)},
					}}}}}},
				},
			},
			"UnknownWindowOperator": {
				swf: bson.D{
					{"sortBy", bson.D{{"qty", int32(1)}}},
					{"output", bson.D{{"x", bson.D{{"$notARealOp", "$qty"}}}}},
				},
			},
			"DeferredWindowOperator": {
				swf: bson.D{
					{"sortBy", bson.D{{"qty", int32(1)}}},
					{"output", bson.D{{"x", bson.D{{"$expMovingAvg", bson.D{{"input", "$qty"}, {"N", int32(2)}}}}}}},
				},
			},
			"OutputNotDocument": {
				swf: bson.D{
					{"sortBy", bson.D{{"qty", int32(1)}}},
					{"output", "notADocument"},
				},
			},
			"OutputFieldNotDocument": {
				swf: bson.D{
					{"sortBy", bson.D{{"qty", int32(1)}}},
					{"output", bson.D{{"x", "notADocument"}}},
				},
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				pipeline := bson.A{bson.D{{"$setWindowFields", tc.swf}}}

				cursor, err := collection.Aggregate(ctx, pipeline)
				if err == nil {
					err = cursor.All(ctx, &[]bson.D{})
					cursor.Close(ctx)
				}

				require.Error(t, err)
			})
		}
	})
}
