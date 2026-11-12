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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/FerretDB/FerretDB/integration/setup"
)

// The positional $ operator: the first array element the query matched.
func TestUpdatePositional(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	_, err := collection.InsertMany(ctx, []any{
		bson.D{{"_id", "p1"}, {"members", bson.A{
			bson.D{{"userId", "u1"}, {"admin", true}},
			bson.D{{"userId", "u2"}, {"admin", true}},
		}}},
		bson.D{{"_id", "p2"}, {"members", bson.A{
			bson.D{{"userId", "u2"}, {"admin", true}},
		}}},
	})
	require.NoError(t, err)

	member := func(id string, i int) bson.D {
		var doc bson.D
		require.NoError(t, collection.FindOne(ctx, bson.D{{"_id", id}}).Decode(&doc))

		for _, e := range doc {
			if e.Key == "members" {
				return e.Value.(bson.A)[i].(bson.D)
			}
		}

		t.Fatal("no members")

		return nil
	}

	res, err := collection.UpdateOne(ctx, bson.D{{"_id", "p1"}, {"members.userId", "u2"}},
		bson.D{{"$set", bson.D{{"members.$.admin", false}, {"members.$.note", "x"}}}})
	require.NoError(t, err)
	require.Equal(t, &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, res)
	AssertEqualDocuments(t, bson.D{{"userId", "u1"}, {"admin", true}}, member("p1", 0))
	AssertEqualDocuments(t, bson.D{{"userId", "u2"}, {"admin", false}, {"note", "x"}}, member("p1", 1))

	// Each document's own element: u2 is the second member of p1, the first of p2.
	res, err = collection.UpdateMany(ctx, bson.D{{"members.userId", "u2"}},
		bson.D{{"$set", bson.D{{"members.$.seen", true}}}})
	require.NoError(t, err)
	require.Equal(t, int64(2), res.ModifiedCount)
	AssertEqualDocuments(t, bson.D{{"userId", "u2"}, {"admin", false}, {"note", "x"}, {"seen", true}}, member("p1", 1))
	AssertEqualDocuments(t, bson.D{{"userId", "u2"}, {"admin", true}, {"seen", true}}, member("p2", 0))

	var after bson.D
	err = collection.FindOneAndUpdate(ctx, bson.D{{"_id", "p2"}, {"members", bson.D{{"$elemMatch", bson.D{{"userId", "u2"}}}}}},
		bson.D{{"$unset", bson.D{{"members.$.seen", ""}}}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&after)
	require.NoError(t, err)
	AssertEqualDocuments(t, bson.D{{"userId", "u2"}, {"admin", true}}, member("p2", 0))

	// MongoDB's BadValue when the query names no element, and for an upsert.
	for name, run := range map[string]func() error{
		"no match": func() error {
			_, err := collection.UpdateOne(ctx, bson.D{{"_id", "p1"}}, bson.D{{"$set", bson.D{{"members.$.admin", false}}}})
			return err
		},
		"upsert": func() error {
			_, err := collection.UpdateOne(ctx, bson.D{{"_id", "p3"}, {"members.userId", "u9"}},
				bson.D{{"$set", bson.D{{"members.$.admin", false}}}}, options.Update().SetUpsert(true))
			return err
		},
	} {
		err := run()

		var se mongo.ServerError
		require.True(t, errors.As(err, &se), "%s: %v", name, err)
		require.True(t, se.HasErrorCode(2), "%s: BadValue: %v", name, err)
	}

	n, err := collection.CountDocuments(ctx, bson.D{{"_id", "p3"}})
	require.NoError(t, err)
	require.Zero(t, n, "the refused upsert inserts nothing")
}
