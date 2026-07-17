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

package sqlite

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestPrepareSelectClause(t *testing.T) {
	t.Parallel()
	table := "table"
	comment := "*/ 1; DROP TABLE " + table + " CASCADE -- "

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		capped        bool
		onlyRecordIDs bool

		expectQuery string
	}{
		"CappedRecordID": {
			capped:        true,
			onlyRecordIDs: true,
			expectQuery: fmt.Sprintf(
				`SELECT %s %s FROM %q`,
				"/* * / 1; DROP TABLE "+table+" CASCADE --  */",
				metadata.RecordIDColumn,
				table,
			),
		},
		"Capped": {
			capped: true,
			expectQuery: fmt.Sprintf(
				`SELECT %s %s, %s FROM %q`,
				"/* * / 1; DROP TABLE "+table+" CASCADE --  */",
				metadata.RecordIDColumn,
				metadata.DefaultColumn,
				table,
			),
		},
		"FullRecord": {
			expectQuery: fmt.Sprintf(
				`SELECT %s %s FROM %q`,
				"/* * / 1; DROP TABLE "+table+" CASCADE --  */",
				metadata.DefaultColumn,
				table,
			),
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			query := prepareSelectClause(table, comment, tc.capped, tc.onlyRecordIDs)
			assert.Equal(t, tc.expectQuery, query)
		})
	}
}

func TestPushdownSafeString(t *testing.T) {
	t.Parallel()

	// Safe: Go's encoding/json and SQLite's -> operator serialize these
	// byte-identically, so a parameterized equality comparison is exact.
	for _, s := range []string{
		"",
		"9dbmCNTLuSaPCJbe3", // typical WeKan _id / boardId
		"Hello World 123",
		"unicode äöü 漢字 ✓",
		`quote " and backslash \ escape the same in both`,
	} {
		assert.True(t, pushdownSafeString(s), "%q must be pushdown-safe", s)
	}

	// Unsafe: Go escapes these as \uXXXX while SQLite renders them raw (or the
	// escapes differ), so pushdown would wrongly exclude matching documents.
	for _, s := range []string{
		"a<b",
		"a>b",
		"a&b",
		"line\nbreak",
		"tab\tseparated",
		"del\x7fchar",
		"ls\u2028sep",
		"ps\u2029sep",
	} {
		assert.False(t, pushdownSafeString(s), "%q must NOT be pushdown-safe", s)
	}
}

func TestPrepareWhereClause(t *testing.T) {
	t.Parallel()

	expr := func(field string) string {
		return fmt.Sprintf(`%s->%q`, metadata.DefaultColumn, field)
	}
	arrayArm := func(field string) string {
		return fmt.Sprintf(`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`, expr(field))
	}

	objectID := types.ObjectID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c}

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		filter      *types.Document
		expectWhere string
		expectArgs  []any
	}{
		"NilFilter": {
			filter: nil,
		},
		"EmptyFilter": {
			filter: must.NotFail(types.NewDocument()),
		},
		"IDString": {
			// _id can never be an array, so plain equality — matching the
			// unique _id expression index.
			filter:      must.NotFail(types.NewDocument("_id", "abc")),
			expectWhere: ` WHERE ` + expr("_id") + ` = ?`,
			expectArgs:  []any{`"abc"`},
		},
		"IDObjectID": {
			filter:      must.NotFail(types.NewDocument("_id", objectID)),
			expectWhere: ` WHERE ` + expr("_id") + ` = ?`,
			expectArgs:  []any{`"0102030405060708090a0b0c"`},
		},
		"TopLevelString": {
			// WeKan's hottest query shape: {boardId: X} (#6467, #6468). The
			// array arm keeps Mongo's array-containment equality candidates.
			filter:      must.NotFail(types.NewDocument("boardId", "b1")),
			expectWhere: ` WHERE ` + arrayArm("boardId"),
			expectArgs:  []any{`"b1"`},
		},
		"MultipleFields": {
			filter:      must.NotFail(types.NewDocument("boardId", "b1", "listId", "l1")),
			expectWhere: ` WHERE ` + arrayArm("boardId") + ` AND ` + arrayArm("listId"),
			expectArgs:  []any{`"b1"`, `"l1"`},
		},
		"IDPlusField": {
			// The old code only pushed down a BARE {_id: X}; a compound filter
			// got no pushdown at all.
			filter:      must.NotFail(types.NewDocument("_id", "abc", "boardId", "b1")),
			expectWhere: ` WHERE ` + expr("_id") + ` = ?` + ` AND ` + arrayArm("boardId"),
			expectArgs:  []any{`"abc"`, `"b1"`},
		},
		"MixedPushableAndNot": {
			// non-string values stay in the Go filter; the string still narrows
			filter:      must.NotFail(types.NewDocument("count", int64(5), "boardId", "b1")),
			expectWhere: ` WHERE ` + arrayArm("boardId"),
			expectArgs:  []any{`"b1"`},
		},
		"DottedPathNotPushed": {
			filter: must.NotFail(types.NewDocument("a.b", "x")),
		},
		"OperatorNotPushed": {
			filter: must.NotFail(types.NewDocument("$comment", "x")),
		},
		"NonStringNotPushed": {
			filter: must.NotFail(types.NewDocument("count", int64(5))),
		},
		"UnsafeStringNotPushed": {
			// Go-JSON escapes '<' as \u003c; SQLite's -> renders it raw — a
			// pushed comparison would wrongly exclude the matching document.
			filter: must.NotFail(types.NewDocument("title", "a<b")),
		},

		// --- $in (WeKan's label filter {labelIds: {$in: [...]}}) ---
		"InPushedNonID": {
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("l1", "l2")))))),
			expectWhere: ` WHERE ` + fmt.Sprintf(
				`(%[1]s IN (?, ?) OR (%[1]s >= '[' AND %[1]s < '\'))`, expr("labelIds")),
			expectArgs: []any{`"l1"`, `"l2"`},
		},
		"InPushedID": {
			filter: must.NotFail(types.NewDocument("_id",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a", "b")))))),
			expectWhere: ` WHERE ` + expr("_id") + ` IN (?, ?)`,
			expectArgs:  []any{`"a"`, `"b"`},
		},
		"InEmptyNotPushed": {
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray()))))),
		},
		"InUnsafeElementNotPushed": {
			// a non-string element: dropping it would make IN a SUBSET, so nothing
			// is pushed and the Go filter stays authoritative.
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("l1", int64(5))))))),
		},
		"InUnsafeStringNotPushed": {
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a<b")))))),
		},

		// --- $regex (WeKan's card-title filter {title: {$regex: text, $options: 'i'}}) ---
		"RegexLiteralPushed": {
			filter: must.NotFail(types.NewDocument("title",
				must.NotFail(types.NewDocument("$regex", "foo", "$options", "i")))),
			expectWhere: ` WHERE ` + expr("title") + ` LIKE ?`,
			expectArgs:  []any{`%foo%`},
		},
		"RegexLiteralWithSpacePushed": {
			filter: must.NotFail(types.NewDocument("title",
				must.NotFail(types.NewDocument("$regex", "foo bar")))),
			expectWhere: ` WHERE ` + expr("title") + ` LIKE ?`,
			expectArgs:  []any{`%foo bar%`},
		},
		"RegexMetacharNotPushed": {
			filter: must.NotFail(types.NewDocument("title",
				must.NotFail(types.NewDocument("$regex", "foo.*bar")))),
		},
		"RegexNonASCIINotPushed": {
			filter: must.NotFail(types.NewDocument("title",
				must.NotFail(types.NewDocument("$regex", "café", "$options", "i")))),
		},
		"RegexExtendedOptionNotPushed": {
			filter: must.NotFail(types.NewDocument("title",
				must.NotFail(types.NewDocument("$regex", "foo bar", "$options", "x")))),
		},
		"RangeNotPushed": {
			// $lte on JSON text has the wrong ordering for numbers/dates; stays in Go.
			filter: must.NotFail(types.NewDocument("dueAt",
				must.NotFail(types.NewDocument("$lte", int64(100))))),
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			where, args := prepareWhereClause(tc.filter)

			assert.Equal(t, tc.expectWhere, where)
			assert.Equal(t, tc.expectArgs, args)
		})
	}
}

func TestPrepareOrderByClause(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		sort    *types.Document
		skip    string
		orderBy string
	}{
		"Ascending": {
			sort:    must.NotFail(types.NewDocument("field", int64(1))),
			skip:    "https://github.com/FerretDB/FerretDB/issues/3181",
			orderBy: "",
		},
		"Descending": {
			sort:    must.NotFail(types.NewDocument("field", int64(-1))),
			skip:    "https://github.com/FerretDB/FerretDB/issues/3181",
			orderBy: "",
		},
		"SortNil": {
			orderBy: "",
		},
		"NaturalAscending": {
			sort:    must.NotFail(types.NewDocument("$natural", int64(1))),
			orderBy: ` ORDER BY _ferretdb_record_id`,
		},
		"NaturalDescending": {
			sort:    must.NotFail(types.NewDocument("$natural", int64(-1))),
			orderBy: ` ORDER BY _ferretdb_record_id DESC`,
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			orderBy := prepareOrderByClause(tc.sort)

			assert.Equal(t, tc.orderBy, orderBy)
		})
	}
}
