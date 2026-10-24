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
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"

	"github.com/FerretDB/FerretDB/internal/backends/mysql/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// prefixedKeyColumnsForTest reaches the registry's helper.
var prefixedKeyColumnsForTest = metadata.PrefixedKeyColumns

// An update or delete by _id compares the _id index's generated column with the
// id's JSON text as a plain string, then exactly. MariaDB 12.3 compares two equal
// JSON strings (JSON_EXTRACT(doc, '$._id') = JSON_EXTRACT(?, '$')) as different,
// so every update and delete by a string _id matched nothing.
func TestIDEqualsUsesTheIndexedColumn(t *testing.T) {
	t.Parallel()

	col := metadata.QuoteIdent(metadata.SafeColumnName("_id"))

	cond := idEquals()
	assert.True(t, strings.HasPrefix(cond, col+" = ?"), "the indexed column first: %s", cond)
	assert.Contains(t, cond, "CAST("+col+" AS BINARY) = CAST(? AS BINARY)", "and an exact, case-sensitive match")
	assert.NotContains(t, cond, "JSON_EXTRACT", "negative: no JSON-to-JSON comparison")

	q, args := deleteByIDs("db", "t", []any{`"a"`, `"b"`})
	assert.Contains(t, q, col+" IN (?, ?) AND CAST("+col+" AS BINARY) IN (CAST(? AS BINARY), CAST(? AS BINARY))")
	assert.Equal(t, []any{`"a"`, `"b"`, `"a"`, `"b"`}, args)
	assert.NotContains(t, q, "JSON_EXTRACT")

	// A table made before the column had this name falls back, on error 1054 only.
	assert.True(t, unknownColumn(&mysql.MySQLError{Number: 1054}))
	assert.False(t, unknownColumn(&mysql.MySQLError{Number: 1062}), "negative: a duplicate key is not a missing column")
	assert.False(t, unknownColumn(errors.New("x")))
	assert.False(t, unknownColumn(nil))
}

// An equality or $in on an indexed field gets a condition its index can serve,
// beside the exact JSON_CONTAINS - a superset: arrays stay candidates.
func TestIndexedNarrowing(t *testing.T) {
	t.Parallel()

	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
	arr := func(values ...any) *types.Array { return must.NotFail(types.NewArray(values...)) }
	indexed := map[string]struct{}{"_id": {}, "boardId": {}, "title": {}}
	col := func(f string) string { return metadata.QuoteIdent(metadata.SafeColumnName(f)) }

	cond, args := indexedNarrowing(doc("boardId", "b1", "archived", false), indexed)
	assert.Equal(t, "("+col("boardId")+" = ? OR "+col("boardId")+" LIKE '[%')", cond)
	assert.Equal(t, []any{`"b1"`}, args)

	cond, args = indexedNarrowing(doc("_id", doc("$in", arr("a", "b"))), indexed)
	assert.Equal(t, col("_id")+" IN (?, ?)", cond, "_id is never an array")
	assert.Equal(t, []any{`"a"`, `"b"`}, args)

	cond, _ = indexedNarrowing(doc("boardId", doc("$eq", "b1")), indexed)
	assert.Contains(t, cond, col("boardId")+" = ?")

	// Negative: what the column cannot compare exactly, or no index, is not narrowed.
	for name, filter := range map[string]*types.Document{
		"not indexed":        doc("listId", "l1"),
		"number":             doc("title", int32(5)),
		"escaped by encoder": doc("title", "a<b"),
		"quote":              doc("title", `a"b`),
		"non-ASCII":          doc("title", "Hyvää"),
		"$in with a number":  doc("boardId", doc("$in", arr("b1", int32(2)))),
		"empty $in":          doc("boardId", doc("$in", arr())),
		"other operator":     doc("boardId", doc("$ne", "b1")),
		"two operators":      doc("boardId", doc("$eq", "b1", "$ne", "b2")),
		"dotted":             doc("a.b", "x"),
	} {
		cond, args := indexedNarrowing(filter, map[string]struct{}{"_id": {}, "boardId": {}, "title": {}, "a.b": {}})
		assert.Empty(t, cond, name)
		assert.Empty(t, args, name)
	}
}

func TestPrefixedKeyColumns(t *testing.T) {
	t.Parallel()

	three := []string{"`a`", "`b` DESC", "`c`"}
	assert.Equal(t, three, prefixedKeyColumnsForTest(three), "three columns fit in 3,072 bytes")

	six := []string{"`a`", "`b`", "`c`", "`d` DESC", "`e`", "`f`"}
	assert.Equal(t, []string{"`a`(128)", "`b`(128)", "`c`(128)", "`d`(128) DESC", "`e`(128)", "`f`(128)"},
		prefixedKeyColumnsForTest(six))
}

func TestIndexJSONPath(t *testing.T) {
	t.Parallel()

	for field, expected := range map[string]string{
		"title":                                `$."title"`,
		"services.accounts-lockout.unlockTime": `$."services"."accounts-lockout"."unlockTime"`,
		"profile.emailBuffer.0":                `$."profile"."emailBuffer"."0"`,
		`a"b\c`:                               `$."a\"b\\c"`,
	} {
		assert.Equal(t, expected, metadata.IndexJSONPath(field), field)
	}
}

func TestIndexedNarrowingAnd(t *testing.T) {
	t.Parallel()

	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
	arr := func(values ...any) *types.Array { return must.NotFail(types.NewArray(values...)) }
	indexed := map[string]struct{}{"_id": {}, "boardId": {}}
	col := metadata.QuoteIdent(metadata.SafeColumnName("boardId"))

	// The due cards shape: the board ids inside an $and.
	cond, args := indexedNarrowing(doc("archived", false, "$and", arr(
		doc("boardId", doc("$in", arr("b1", "b2"))),
		doc("$or", arr(doc("members", "u1"))),
	)), indexed)
	assert.Equal(t, "(("+col+" IN (?, ?) OR "+col+" LIKE '[%'))", cond)
	assert.Equal(t, []any{`"b1"`, `"b2"`}, args)

	// Negative: nothing indexed in the branches, or no branches, adds nothing.
	for name, f := range map[string]*types.Document{
		"not indexed":  doc("$and", arr(doc("title", "x"))),
		"not an array": doc("$and", "x"),
		"only an $or":  doc("$and", arr(doc("$or", arr(doc("boardId", "b1"))))),
	} {
		cond, args := indexedNarrowing(f, indexed)
		assert.Empty(t, cond, name)
		assert.Empty(t, args, name)
	}
}
