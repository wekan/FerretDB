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

package postgresql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FerretDB/FerretDB/internal/backends/postgresql/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestMultikeyWhere(t *testing.T) {
	t.Parallel()

	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
	arr := func(values ...any) *types.Array { return must.NotFail(types.NewArray(values...)) }

	meta := &metadata.Collection{Indexes: []metadata.IndexInfo{
		{Name: "_id_", Key: []metadata.IndexKeyPair{{Field: "_id"}}},
		{Name: "members.userId_1", Key: []metadata.IndexKeyPair{{Field: "members.userId"}}},
		{Name: "archived_1_type_1", Key: []metadata.IndexKeyPair{{Field: "archived"}, {Field: "type"}}},
	}}
	member := func(user string) *types.Document {
		return doc("members", doc("$elemMatch", doc("userId", user, "isActive", true)))
	}
	contains := `(_jsonb->'members' @> $1 OR _jsonb->'members' @> $2)`

	// A membership test: containment, for a scalar userId and one in an array.
	cond, args := multikeyWhere(new(metadata.Placeholder), meta, member("u1"))
	assert.Equal(t, contains, cond)
	assert.Equal(t, []any{`[{"userId":"u1"}]`, `[{"userId":["u1"]}]`}, args)

	// The All Boards shape: a membership OR starred _ids.
	cond, args = multikeyWhere(new(metadata.Placeholder), meta, doc("archived", false, "$or", arr(
		member("u1"), doc("_id", doc("$in", arr("b1", "b2")), "permission", "public"),
	)))
	assert.Equal(t, `(`+contains+` OR (_jsonb->'_id' IN ($3, $4)))`, cond)
	assert.Equal(t, []any{`[{"userId":"u1"}]`, `[{"userId":["u1"]}]`, `"b1"`, `"b2"`}, args)

	// Negative: nothing narrows what may match outside it.
	for name, filter := range map[string]*types.Document{
		"a branch with no indexed condition": doc("$or", arr(member("u1"), doc("permission", "public"))),
		"no GIN index for that array":        doc("teams", doc("$elemMatch", doc("teamId", "t1"))),
		"a field the index does not lead":    doc("members", doc("$elemMatch", doc("isActive", true))),
		"a number cannot be compared":        doc("members", doc("$elemMatch", doc("userId", int32(1)))),
		"empty $in":                          doc("members", doc("$elemMatch", doc("userId", doc("$in", arr())))),
		"not $elemMatch":                     doc("members", doc("$size", int32(1))),
	} {
		cond, args := multikeyWhere(new(metadata.Placeholder), meta, filter)
		assert.Empty(t, cond, name)
		assert.Empty(t, args, name)
	}

	// Negative: a capped collection, or no metadata, is never narrowed.
	assert.Empty(t, first(multikeyWhere(new(metadata.Placeholder), nil, member("u1"))))
}

func first(s string, _ []any) string { return s }

func TestMultikeyIndexes(t *testing.T) {
	t.Parallel()

	arrays := metadata.MultikeyArrays([]metadata.IndexInfo{
		{Key: []metadata.IndexKeyPair{{Field: "members.userId"}}},
		{Key: []metadata.IndexKeyPair{{Field: "members.isActive"}, {Field: "x"}}},
		{Key: []metadata.IndexKeyPair{{Field: "x"}, {Field: "teams.teamId"}}},
		{Key: []metadata.IndexKeyPair{{Field: "a.b.c"}}},
		{Key: []metadata.IndexKeyPair{{Field: `a"b.c`}}},
	})
	assert.Equal(t, map[string][]string{"members": {"userId", "isActive"}}, arrays,
		"only a leading ARRAY.FIELD of plain names")
	assert.LessOrEqual(t, len(metadata.MultikeyIndexName(string(make([]byte, 100)), "members")), 63)
}
