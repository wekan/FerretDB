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

package mysql

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FerretDB/FerretDB/internal/backends/mysql/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestMultikeyWhere(t *testing.T) {
	t.Parallel()

	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
	arr := func(values ...any) *types.Array { return must.NotFail(types.NewArray(values...)) }

	meta := &metadata.Collection{TableName: "boards_1234abcd", Indexes: []metadata.IndexInfo{
		{Name: "_id_", Key: []metadata.IndexKeyPair{{Field: "_id"}}},
		{Name: "members.userId_1", Key: []metadata.IndexKeyPair{{Field: "members.userId"}}},
		{Name: "archived_1_type_1", Key: []metadata.IndexKeyPair{{Field: "archived"}, {Field: "type"}}},
	}}
	mk := metadata.MultikeyTableName(meta.TableName, "members", "userId")
	ready := func(name string) bool { return name == mk }
	member := func(user any) *types.Document {
		return doc("members", doc("$elemMatch", doc("userId", user, "isActive", true)))
	}
	id := metadata.QuoteIdent(metadata.SafeColumnName("_id"))
	side := id + " IN (SELECT id FROM `db`.`" + mk + "` WHERE v IN (?))"

	// A membership test: the side table.
	cond, args := multikeyWhere("db", meta, ready, member("u1"))
	assert.Equal(t, side, cond)
	assert.Equal(t, []any{`"u1"`}, args)

	cond, args = multikeyWhere("db", meta, ready, doc("members", doc("$elemMatch", doc("userId", doc("$in", arr("u1", "u2"))))))
	assert.Equal(t, strings.Replace(side, "(?)", "(?, ?)", 1), cond)
	assert.Equal(t, []any{`"u1"`, `"u2"`}, args)

	// The All Boards shape: a membership OR starred _ids.
	cond, args = multikeyWhere("db", meta, ready, doc("archived", false, "$or", arr(
		member("u1"), doc("_id", doc("$in", arr("b1", "b2")), "permission", "public"),
	)))
	assert.Equal(t, "("+side+" OR "+id+" IN (?, ?))", cond)
	assert.Equal(t, []any{`"u1"`, `"b1"`, `"b2"`}, args)

	// Negative: nothing narrows what may match outside it.
	for name, filter := range map[string]*types.Document{
		"a branch with no indexed condition": doc("$or", arr(member("u1"), doc("permission", "public"))),
		"no side table for that array":       doc("teams", doc("$elemMatch", doc("teamId", "t1"))),
		"a field the index does not lead":    doc("members", doc("$elemMatch", doc("isActive", true))),
		"a number is not in the table":       member(int32(1)),
		"a string the text is not certain":   member(`a"b`),
		"a string too long for the table":    member(strings.Repeat("x", 260)),
		"empty $in":                          doc("members", doc("$elemMatch", doc("userId", doc("$in", arr())))),
		"not $elemMatch":                     doc("members", doc("$size", int32(1))),
	} {
		cond, args := multikeyWhere("db", meta, ready, filter)
		assert.Empty(t, cond, name)
		assert.Empty(t, args, name)
	}

	// Negative: a side table that is not complete - triggers refused, say - is
	// never used, and neither is a collection without metadata.
	cond, _ = multikeyWhere("db", meta, func(string) bool { return false }, member("u1"))
	assert.Empty(t, cond)
	cond, _ = multikeyWhere("db", nil, ready, member("u1"))
	assert.Empty(t, cond)
}

func TestMultikeySQL(t *testing.T) {
	t.Parallel()

	// The side table's name fits MySQL's 64 characters whatever the table's.
	name := metadata.MultikeyTableName(strings.Repeat("t", 64), "members", "userId")
	assert.LessOrEqual(t, len(name+"_ai"), 64)
	assert.NotEqual(t, name, metadata.MultikeyTableName(strings.Repeat("t", 64), "members", "teamId"))

	// Only plain names get a side table: they are written into JSON paths.
	for field, ok := range map[string]bool{
		"members.userId":      true,
		"members.0":           false,
		"members.user\"Id":    false,
		"a.b.c":               false,
		"members":             false,
		"members.user-id_2":   true,
		"$members.userId":     false,
		"members.userId.more": false,
	} {
		_, _, got := metadata.MultikeyPath(metadata.IndexInfo{Key: []metadata.IndexKeyPair{{Field: field}}})
		assert.Equal(t, ok, got, field)
	}
}
