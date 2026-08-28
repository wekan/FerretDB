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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// listIndexByName returns the listIndexes document for the index with the given name.
func listIndexByName(t *testing.T, ctx context.Context, collection *mongo.Collection, name string) bson.M {
	t.Helper()

	cursor, err := collection.Indexes().List(ctx)
	require.NoError(t, err)

	var indexes []bson.D
	require.NoError(t, cursor.All(ctx, &indexes))

	for _, idx := range indexes {
		m := idx.Map()
		if m["name"] == name {
			return m
		}
	}

	return nil
}

// TestCreateIndexesOptions verifies that FerretDB accepts, stores and reports the hidden,
// collation, partialFilterExpression and 2dsphere index options through listIndexes, while
// still rejecting options that remain unimplemented and malformed option values.
//
// Note: these options are accepted and round-tripped only. FerretDB does not hide indexes
// from the planner, does not apply locale-aware collation, does not restrict partial indexes
// and does not support geospatial queries.
func TestCreateIndexesOptions(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	// createIndex is a helper that creates a single index via the raw createIndexes command
	// so tests fully control the option document and can assert exact errors.
	createIndex := func(spec bson.D) error {
		command := bson.D{
			{"createIndexes", collection.Name()},
			{"indexes", bson.A{spec}},
		}

		var res bson.D

		return collection.Database().RunCommand(ctx, command).Decode(&res)
	}

	t.Run("Hidden", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, createIndex(bson.D{
			{"key", bson.D{{"h", 1}}},
			{"name", "h_hidden"},
			{"hidden", true},
		}))

		m := listIndexByName(t, ctx, collection, "h_hidden")
		require.NotNil(t, m, "hidden index must be listed")
		assert.Equal(t, true, m["hidden"], "listIndexes must report hidden: true")
	})

	t.Run("HiddenFalseNotReported", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, createIndex(bson.D{
			{"key", bson.D{{"hf", 1}}},
			{"name", "hf_visible"},
			{"hidden", false},
		}))

		m := listIndexByName(t, ctx, collection, "hf_visible")
		require.NotNil(t, m)
		_, ok := m["hidden"]
		assert.False(t, ok, "hidden: false must not be reported")
	})

	t.Run("Collation", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, createIndex(bson.D{
			{"key", bson.D{{"c", 1}}},
			{"name", "c_collation"},
			{"collation", bson.D{{"locale", "en"}, {"strength", int32(2)}}},
		}))

		m := listIndexByName(t, ctx, collection, "c_collation")
		require.NotNil(t, m, "collation index must be listed")

		collation, ok := m["collation"].(bson.D)
		require.True(t, ok, "listIndexes must report the collation document")
		cm := collation.Map()
		assert.Equal(t, "en", cm["locale"])
		assert.EqualValues(t, 2, cm["strength"])
	})

	t.Run("PartialFilterExpression", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, createIndex(bson.D{
			{"key", bson.D{{"p", 1}}},
			{"name", "p_partial"},
			{"partialFilterExpression", bson.D{{"rating", bson.D{{"$gt", int32(5)}}}}},
		}))

		m := listIndexByName(t, ctx, collection, "p_partial")
		require.NotNil(t, m, "partial index must be listed")

		pfe, ok := m["partialFilterExpression"].(bson.D)
		require.True(t, ok, "listIndexes must report partialFilterExpression")

		rating, ok := pfe.Map()["rating"].(bson.D)
		require.True(t, ok, "partialFilterExpression must round-trip the nested document")
		assert.EqualValues(t, 5, rating.Map()["$gt"])
	})

	t.Run("Sphere2D", func(t *testing.T) {
		t.Parallel()

		_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{"loc", "2dsphere"}},
		})
		require.NoError(t, err)

		m := listIndexByName(t, ctx, collection, "loc_2dsphere")
		require.NotNil(t, m, "2dsphere index must be listed")

		key, ok := m["key"].(bson.D)
		require.True(t, ok)
		assert.Equal(t, "2dsphere", key.Map()["loc"], "2dsphere key must round-trip")
		assert.EqualValues(t, 3, m["2dsphereIndexVersion"], "2dsphereIndexVersion must be reported")
	})

	t.Run("Sphere2DWithVersion", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, createIndex(bson.D{
			{"key", bson.D{{"geo", "2dsphere"}}},
			{"name", "geo_2dsphere"},
			{"2dsphereIndexVersion", int32(2)},
		}))

		m := listIndexByName(t, ctx, collection, "geo_2dsphere")
		require.NotNil(t, m)
		assert.EqualValues(t, 2, m["2dsphereIndexVersion"], "explicit 2dsphereIndexVersion must round-trip")
	})

	t.Run("LegacyNamespace", func(t *testing.T) {
		t.Parallel()

		// MongoDB 3.x writes `ns` into dumped index metadata. Modern
		// mongorestore may forward it, so accept and discard it like MongoDB.
		require.NoError(t, createIndex(bson.D{
			{"key", bson.D{{"legacy", 1}}},
			{"name", "legacy_1"},
			{"ns", collection.Database().Name() + "." + collection.Name()},
		}))

		m := listIndexByName(t, ctx, collection, "legacy_1")
		require.NotNil(t, m, "legacy index must be created")
		_, ok := m["ns"]
		assert.False(t, ok, "deprecated ns must not become a stored index option")
	})

	// Negative: an option that remains unimplemented must still be rejected.
	t.Run("UnsupportedStorageEngine", func(t *testing.T) {
		t.Parallel()

		err := createIndex(bson.D{
			{"key", bson.D{{"se", 1}}},
			{"name", "se_idx"},
			{"storageEngine", bson.D{{"wiredTiger", bson.D{}}}},
		})
		require.Error(t, err, "storageEngine must still be rejected")

		var ce mongo.CommandError
		require.True(t, errors.As(err, &ce))
		assert.EqualValues(t, 238, ce.Code, "unimplemented option must return NotImplemented (238)")
	})

	t.Run("UnsupportedWildcardProjection", func(t *testing.T) {
		t.Parallel()

		err := createIndex(bson.D{
			{"key", bson.D{{"$**", 1}}},
			{"name", "wp_idx"},
			{"wildcardProjection", bson.D{{"a", int32(1)}}},
		})
		require.Error(t, err, "wildcardProjection must still be rejected")
	})

	// Negative: a malformed option value must be rejected with a type error.
	t.Run("MalformedHidden", func(t *testing.T) {
		t.Parallel()

		err := createIndex(bson.D{
			{"key", bson.D{{"mh", 1}}},
			{"name", "mh_idx"},
			{"hidden", "yes"},
		})
		require.Error(t, err, "non-boolean hidden must be rejected")

		var ce mongo.CommandError
		require.True(t, errors.As(err, &ce))
		assert.EqualValues(t, 14, ce.Code, "malformed hidden must return TypeMismatch (14)")
	})

	t.Run("MalformedCollation", func(t *testing.T) {
		t.Parallel()

		err := createIndex(bson.D{
			{"key", bson.D{{"mc", 1}}},
			{"name", "mc_idx"},
			{"collation", "en"},
		})
		require.Error(t, err, "non-document collation must be rejected")

		var ce mongo.CommandError
		require.True(t, errors.As(err, &ce))
		assert.EqualValues(t, 14, ce.Code, "malformed collation must return TypeMismatch (14)")
	})

	t.Run("UnknownOption", func(t *testing.T) {
		t.Parallel()

		err := createIndex(bson.D{
			{"key", bson.D{{"unknown", 1}}},
			{"name", "unknown_idx"},
			{"notALegacyOption", true},
		})
		require.Error(t, err, "unrelated unknown options must still be rejected")

		var ce mongo.CommandError
		require.True(t, errors.As(err, &ce))
		assert.EqualValues(t, 2, ce.Code, "unknown option must return BadValue (2)")
	})
}
