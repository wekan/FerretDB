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
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/FerretDB/FerretDB/integration/setup"
)

func TestUpdatePullDocumentCondition(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)
	doc := bson.D{
		{"_id", "pull-document-condition"},
		{"customFields", bson.A{
			bson.D{{"_id", "kept"}, {"value", true}},
			bson.D{{"_id", "removed"}, {"value", false}},
		}},
	}

	_, err := collection.InsertOne(ctx, doc)
	require.NoError(t, err)

	res, err := collection.UpdateOne(ctx, bson.D{{"_id", "pull-document-condition"}},
		bson.D{{"$pull", bson.D{{"customFields", bson.D{{"_id", "removed"}}}}}})
	require.NoError(t, err)
	require.Equal(t, &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, res)

	res, err = collection.UpdateOne(ctx, bson.D{{"_id", "pull-document-condition"}},
		bson.D{{"$pull", bson.D{{"customFields", bson.D{{"_id", "missing"}}}}}})
	require.NoError(t, err)
	require.Equal(t, &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 0}, res)

	var actual bson.D
	err = collection.FindOne(ctx, bson.D{{"_id", "pull-document-condition"}}).Decode(&actual)
	require.NoError(t, err)
	AssertEqualDocuments(t, bson.D{
		{"_id", "pull-document-condition"},
		{"customFields", bson.A{bson.D{{"_id", "kept"}, {"value", true}}}},
	}, actual)
}
