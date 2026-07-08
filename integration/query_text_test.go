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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// findTextIDs runs the given filter and returns the sorted set of matched string _id values.
func findTextIDs(t *testing.T, ctx context.Context, collection *mongo.Collection, filter bson.D) []string {
	t.Helper()

	cursor, err := collection.Find(ctx, filter)
	require.NoError(t, err)

	var docs []bson.D
	require.NoError(t, cursor.All(ctx, &docs))

	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		for _, e := range doc {
			if e.Key == "_id" {
				ids = append(ids, e.Value.(string))
			}
		}
	}

	sort.Strings(ids)

	return ids
}

// TestQueryText covers creating a text index, listing it back, and running $text queries
// against FerretDB's partial (self-contained, no relevance scoring) $text implementation.
func TestQueryText(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	// Create a compound text index on subject and body.
	indexName, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{"subject", "text"}, {"body", "text"}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, indexName)

	// listIndexes must report the text index with its text key(s).
	cursor, err := collection.Indexes().List(ctx)
	require.NoError(t, err)

	var indexes []bson.D
	require.NoError(t, cursor.All(ctx, &indexes))

	var foundCompound bool

	for _, idx := range indexes {
		m := idx.Map()

		if m["name"] == indexName {
			foundCompound = true

			key, ok := m["key"].(bson.D)
			require.True(t, ok, "text index key should be a document")

			km := key.Map()
			assert.Equal(t, "text", km["subject"], "subject must be a text key")
			assert.Equal(t, "text", km["body"], "body must be a text key")

			// weights should be echoed back, defaulting to 1 per field.
			weights, ok := m["weights"].(bson.D)
			require.True(t, ok, "text index must report weights")
			wm := weights.Map()
			assert.EqualValues(t, 1, wm["subject"])
			assert.EqualValues(t, 1, wm["body"])

			// text index option defaults are reported.
			assert.Equal(t, "english", m["default_language"])
			assert.EqualValues(t, 3, m["textIndexVersion"])
		}
	}

	assert.True(t, foundCompound, "compound text index must be listed")

	// A separate collection verifies text index options (weights, default_language)
	// are accepted and round-tripped through listIndexes.
	weightedColl := collection.Database().Collection(collection.Name() + "_weighted")

	weightedName, err := weightedColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{"title", "text"}},
		Options: options.Index().
			SetName("title_weighted").
			SetWeights(bson.D{{"title", 5}}).
			SetDefaultLanguage("english"),
	})
	require.NoError(t, err)
	require.Equal(t, "title_weighted", weightedName)

	wCursor, err := weightedColl.Indexes().List(ctx)
	require.NoError(t, err)

	var wIndexes []bson.D
	require.NoError(t, wCursor.All(ctx, &wIndexes))

	var foundWeighted bool

	for _, idx := range wIndexes {
		m := idx.Map()

		if m["name"] == "title_weighted" {
			foundWeighted = true

			weights, ok := m["weights"].(bson.D)
			require.True(t, ok, "weighted text index must report weights")
			assert.EqualValues(t, 5, weights.Map()["title"])
			assert.Equal(t, "english", m["default_language"])
		}
	}

	assert.True(t, foundWeighted, "weighted text index must be listed")

	// Insert test documents.
	docs := []any{
		bson.D{{"_id", "a"}, {"subject", "Coffee brewing"}, {"body", "How to make espresso"}},
		bson.D{{"_id", "b"}, {"subject", "Tea time"}, {"body", "Green tea and coffee alternatives"}},
		bson.D{{"_id", "c"}, {"subject", "Morning routine"}, {"body", "Wake up and stretch"}},
		bson.D{{"_id", "d"}, {"subject", "COFFEE facts"}, {"body", "Beans and roasting"}},
		bson.D{{"_id", "e"}, {"subject", "Nested"}, {"body", "plain"}, {"tags", bson.A{"barista", "milk"}}},
	}
	_, err = collection.InsertMany(ctx, docs)
	require.NoError(t, err)

	// Single term (case-insensitive word match across string fields).
	assert.Equal(t, []string{"a", "b", "d"},
		findTextIDs(t, ctx, collection, bson.D{{"$text", bson.D{{"$search", "coffee"}}}}))

	// Multi-term OR: matches documents containing any of the terms.
	assert.Equal(t, []string{"a", "b", "d", "e"},
		findTextIDs(t, ctx, collection, bson.D{{"$text", bson.D{{"$search", "coffee barista"}}}}))

	// Term matching recurses into arrays of strings.
	assert.Equal(t, []string{"e"},
		findTextIDs(t, ctx, collection, bson.D{{"$text", bson.D{{"$search", "milk"}}}}))

	// Case-sensitive matching: only the document with uppercase COFFEE.
	assert.Equal(t, []string{"d"},
		findTextIDs(t, ctx, collection, bson.D{
			{"$text", bson.D{{"$search", "COFFEE"}, {"$caseSensitive", true}}},
		}))

	// Quoted phrase: contiguous substring match.
	assert.Equal(t, []string{"a"},
		findTextIDs(t, ctx, collection, bson.D{
			{"$text", bson.D{{"$search", `"coffee brewing"`}}},
		}))

	// Phrase that does not occur contiguously matches nothing.
	assert.Empty(t,
		findTextIDs(t, ctx, collection, bson.D{
			{"$text", bson.D{{"$search", `"coffee routine"`}}},
		}))

	// Negation: exclude documents containing the negated term while requiring the positive.
	assert.Equal(t, []string{"a", "d"},
		findTextIDs(t, ctx, collection, bson.D{
			{"$text", bson.D{{"$search", "coffee -tea"}}},
		}))

	// $language and $diacriticSensitive are accepted and ignored.
	assert.Equal(t, []string{"a", "b", "d"},
		findTextIDs(t, ctx, collection, bson.D{
			{"$text", bson.D{{"$search", "coffee"}, {"$language", "english"}, {"$diacriticSensitive", false}}},
		}))
}

// TestQueryTextErrors covers invalid $text queries.
func TestQueryTextErrors(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	_, err := collection.InsertOne(ctx, bson.D{{"_id", "a"}, {"subject", "coffee"}})
	require.NoError(t, err)

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		filter bson.D
	}{
		"NonStringSearch": {
			filter: bson.D{{"$text", bson.D{{"$search", int32(123)}}}},
		},
		"MissingSearch": {
			filter: bson.D{{"$text", bson.D{}}},
		},
		"NonBoolCaseSensitive": {
			filter: bson.D{{"$text", bson.D{{"$search", "coffee"}, {"$caseSensitive", "yes"}}}},
		},
		"NotADocument": {
			filter: bson.D{{"$text", "coffee"}},
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cursor, err := collection.Find(ctx, tc.filter)
			if err == nil {
				// Some drivers defer the error until the cursor is iterated.
				err = cursor.All(ctx, &[]bson.D{})
			}

			require.Error(t, err)

			var cmdErr mongo.CommandError
			require.ErrorAs(t, err, &cmdErr)
		})
	}
}
