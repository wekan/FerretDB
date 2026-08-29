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
	"strings"
	"testing"
	"time"

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

func TestIDColumnMatchesIndexedExpression(t *testing.T) {
	t.Parallel()

	assert.Equal(t, jsonPathExpr("_id"), metadata.IDColumn)
	assert.NotEqual(t, metadata.DefaultColumn+"->'$._id'", metadata.IDColumn,
		"an equivalent JSON path with different syntax cannot use SQLite's expression index")
}

func TestPushdownSafeString(t *testing.T) {
	t.Parallel()

	// Safe: Go's encoding/json and SQLite's -> operator serialize these
	// byte-identically, so a parameterized equality comparison is exact.
	for _, s := range []string{
		"",
		"9dbmCNTLuSaPCJbe3", // typical application _id
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

func TestPreferredCompoundIndex(t *testing.T) {
	t.Parallel()

	indexes := []metadata.IndexInfo{
		{Name: "boardId_1_archived_1", Key: []metadata.IndexKeyPair{
			{Field: "boardId"}, {Field: "archived"},
		}},
		{Name: "boardId_1_archived_1_type_1", Key: []metadata.IndexKeyPair{
			{Field: "boardId"}, {Field: "archived"}, {Field: "type"},
		}},
		{Name: "type_1", Key: []metadata.IndexKeyPair{{Field: "type"}}},
	}

	t.Run("ExactCompound", func(t *testing.T) {
		filter := must.NotFail(types.NewDocument(
			"boardId", "b1", "archived", false, "type", "cardType-linkedCard",
		))
		assert.Equal(t, "cards_boardId_1_archived_1_type_1", preferredCompoundIndex("cards", indexes, filter))
	})

	t.Run("ShortestMatchingPrefix", func(t *testing.T) {
		filter := must.NotFail(types.NewDocument("boardId", "b1", "archived", false))
		assert.Equal(t, "cards_boardId_1_archived_1", preferredCompoundIndex("cards", indexes, filter))
	})

	t.Run("SingleFieldForced", func(t *testing.T) {
		filter := must.NotFail(types.NewDocument("type", "cardType-linkedCard"))
		assert.Equal(t, "cards_type_1", preferredCompoundIndex("cards", indexes, filter))
	})

	t.Run("FieldsInsideAnd", func(t *testing.T) {
		filter := must.NotFail(types.NewDocument("$and", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("boardId", "b1")),
			must.NotFail(types.NewDocument("archived", false)),
		))))
		assert.Equal(t, "cards_boardId_1_archived_1", preferredCompoundIndex("cards", indexes, filter))
	})
}

func TestPreferredDistinctIndex(t *testing.T) {
	t.Parallel()

	indexes := []metadata.IndexInfo{
		{Name: "boardId_1", Key: []metadata.IndexKeyPair{{Field: "boardId"}}},
		{Name: "swimlaneId_1_sort_1", Key: []metadata.IndexKeyPair{{Field: "swimlaneId"}, {Field: "sort"}}},
		{Name: "hidden_1", Key: []metadata.IndexKeyPair{{Field: "hidden"}}, Hidden: true},
	}
	assert.Equal(t, "cards_swimlaneId_1_sort_1", preferredDistinctIndex("cards", indexes, "swimlaneId"))
	assert.Empty(t, preferredDistinctIndex("cards", indexes, "missing"))
	assert.Empty(t, preferredDistinctIndex("cards", indexes, "hidden"))
}

func TestPrepareWhereClause(t *testing.T) {
	t.Parallel()

	expr := func(field string) string {
		return fmt.Sprintf(`%s->%q`, metadata.DefaultColumn, field)
	}
	arrayArm := func(field string) string {
		return fmt.Sprintf(`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`, expr(field))
	}
	elemMatch := func(field string, conditions ...string) string {
		fieldExpr := expr(field)
		return fmt.Sprintf(
			`(json_type(%[1]s) = 'array' AND EXISTS (`+
				`SELECT 1 FROM json_each(%[1]s) AS element `+
				`WHERE json_type(element.value) = 'object' AND %s))`,
			fieldExpr,
			strings.Join(conditions, " AND "),
		)
	}
	scalarExpr := func(field string) string {
		return fmt.Sprintf(`%s->>%q`, metadata.DefaultColumn, field)
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
		"AndPushesBranches": {
			filter: must.NotFail(types.NewDocument("$and", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("boardId", "b1")),
				must.NotFail(types.NewDocument("archived", false)),
				must.NotFail(types.NewDocument("sort", must.NotFail(types.NewDocument("$gt", int64(0))))),
			)))),
			expectWhere: ` WHERE ((` + arrayArm("boardId") + `) AND (` + arrayArm("archived") +
				`) AND (` + scalarExpr("sort") + ` > ?))`,
			expectArgs: []any{`"b1"`, `false`, int64(0)},
		},
		"AndWithNoPushdownStaysInGo": {
			filter: must.NotFail(types.NewDocument("$and", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("sort", must.NotFail(types.NewDocument("$ne", int64(0))))),
			)))),
		},
		// ── $or ─────────────────────────────────────────────────────────────
		//
		// A selector whose only SELECTIVE terms sit inside an $or - membership
		// ORed over several ways of belonging, beside a non-selective
		// `archived = false` - used to produce a WHERE that narrowed nothing,
		// so every row was decoded and filtered in Go to return a handful.
		"OrPushedDownWhenEveryBranchCan": {
			filter: must.NotFail(types.NewDocument(
				"archived", false,
				"$or", must.NotFail(types.NewArray(
					must.NotFail(types.NewDocument("permission", "public")),
					must.NotFail(types.NewDocument("members.userId", "u1")),
				)),
			)),
			expectWhere: ` WHERE ` + arrayArm("archived") + ` AND (` +
				`(` + arrayArm("permission") + `)` +
				` OR ` +
				`(` + fmt.Sprintf(`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`,
				metadata.DefaultColumn+`->"members"->"userId"`) + `)` +
				`)`,
			expectArgs: []any{`false`, `"public"`, `"u1"`},
		},

		// ALL OR NOTHING. Every other pushdown narrows: a condition that cannot
		// be expressed is dropped and the Go filter removes the extra rows. An
		// OR is the opposite - dropping a branch REMOVES rows that match it, and
		// the Go filter never sees them. So one unpushable branch means the whole
		// $or stays in Go.
		"OrNotPushedDownWhenOneBranchCannotBe": {
			filter: must.NotFail(types.NewDocument("$or", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("permission", "public")),
				// $ne has no pushdown: "not this value" over a JSON column is not
				// a condition this builder can express as a superset.
				must.NotFail(types.NewDocument("title", must.NotFail(types.NewDocument("$ne", "x")))),
			)))),
		},
		"OrNotPushedDownWithNestedOperatorBranch": {
			filter: must.NotFail(types.NewDocument("$or", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("permission", "public")),
				must.NotFail(types.NewDocument("$and", must.NotFail(types.NewArray()))),
			)))),
		},
		"OrNotPushedDownWhenABranchIsEmpty": {
			// An empty branch matches everything, so the $or matches everything:
			// there is nothing to gain and a WHERE would be wrong to narrow.
			filter: must.NotFail(types.NewDocument("$or", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("permission", "public")),
				must.NotFail(types.NewDocument()),
			)))),
		},
		"EmptyOrIsNotPushedDown": {
			filter: must.NotFail(types.NewDocument("$or", must.NotFail(types.NewArray()))),
		},
		"OtherTopLevelOperatorsStillStayInGo": {
			// $nor and the remaining logical operators stay in Go.
			filter: must.NotFail(types.NewDocument("$nor", must.NotFail(types.NewArray(
				must.NotFail(types.NewDocument("permission", "public")),
			)))),
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
			// A common hottest query shape: {field: X} equality. The
			// array arm keeps Mongo's array-containment equality candidates.
			filter:      must.NotFail(types.NewDocument("boardId", "b1")),
			expectWhere: ` WHERE ` + arrayArm("boardId"),
			expectArgs:  []any{`"b1"`},
		},
		"TopLevelBool": {
			filter:      must.NotFail(types.NewDocument("archived", false)),
			expectWhere: ` WHERE ` + arrayArm("archived"),
			expectArgs:  []any{`false`},
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

		// --- document-form $elemMatch ---
		"ElemMatchEqualityPushed": {
			filter: must.NotFail(types.NewDocument("members", must.NotFail(types.NewDocument(
				"$elemMatch", must.NotFail(types.NewDocument("userId", "u1", "isActive", true)),
			)))),
			expectWhere: ` WHERE ` + elemMatch("members",
				`(element.value->"userId" = ? OR (element.value->"userId" >= '[' AND element.value->"userId" < '\'))`,
				`(element.value->"isActive" = ? OR (element.value->"isActive" >= '[' AND element.value->"isActive" < '\'))`,
			),
			expectArgs: []any{`"u1"`, `true`},
		},
		"ElemMatchInPushed": {
			filter: must.NotFail(types.NewDocument("teams", must.NotFail(types.NewDocument(
				"$elemMatch", must.NotFail(types.NewDocument(
					"teamId", must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("t1", "t2")))),
					"isActive", true,
				)),
			)))),
			expectWhere: ` WHERE ` + elemMatch("teams",
				`(element.value->"teamId" IN (?, ?) OR (element.value->"teamId" >= '[' AND element.value->"teamId" < '\'))`,
				`(element.value->"isActive" = ? OR (element.value->"isActive" >= '[' AND element.value->"isActive" < '\'))`,
			),
			expectArgs: []any{`"t1"`, `"t2"`, `true`},
		},
		"ElemMatchUnsupportedInnerConditionStaysInGo": {
			filter: must.NotFail(types.NewDocument("members", must.NotFail(types.NewDocument(
				"$elemMatch", must.NotFail(types.NewDocument("score", must.NotFail(types.NewDocument("$gt", int32(2))))),
			)))),
		},

		// --- $in (a list filter {field: {$in: [...]}}) ---
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
		"EmptyInMatchesNothing": {
			filter: must.NotFail(types.NewDocument("_id",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray()))))),
			expectWhere: ` WHERE 0`,
		},
		"InWithNullPushed": {
			// {boardId: {$in: [id, null]}} — a board's card-scope shape when no
			// subtasks-default board is set. The id pushes as an IN, the null as an
			// IS NULL arm (a null $in element also matches a missing field), plus the
			// array arm — all index-usable, so it no longer full-scans on FerretDB.
			filter: must.NotFail(types.NewDocument("boardId",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("B", types.Null)))))),
			expectWhere: ` WHERE ` + fmt.Sprintf(
				`(%[1]s IN (?) OR %[1]s IS NULL OR (%[1]s >= '[' AND %[1]s < '\'))`, expr("boardId")),
			expectArgs: []any{`"B"`},
		},
		"InOnlyNullPushed": {
			// {field: {$in: [null]}} pushes just the IS NULL + array arm (no args).
			filter: must.NotFail(types.NewDocument("boardId",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray(types.Null)))))),
			expectWhere: ` WHERE ` + fmt.Sprintf(
				`(%[1]s IS NULL OR (%[1]s >= '[' AND %[1]s < '\'))`, expr("boardId")),
		},
		"InIdWithNullPushed": {
			// _id needs no array arm, but the null still gets an IS NULL arm.
			filter: must.NotFail(types.NewDocument("_id",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a", types.Null)))))),
			expectWhere: ` WHERE ` + fmt.Sprintf(`(%[1]s IN (?) OR %[1]s IS NULL)`, expr("_id")),
			expectArgs:  []any{`"a"`},
		},
		"InNumberElementNotPushed": {
			// a number element has no safe superset arm -> the whole $in stays in Go.
			filter: must.NotFail(types.NewDocument("x",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a", int32(5))))))),
		},
		"DottedPathEqualityPushed": {
			// {'meta.cardId': 'C'} — a Meteor-Files attachment lookup. Pushes down as
			// the NESTED expression that matches the meta.cardId expression index, so
			// the attachments collection is no longer full-scanned on every poll.
			filter: must.NotFail(types.NewDocument("meta.cardId", "C")),
			expectWhere: ` WHERE ` + fmt.Sprintf(
				`(%[1]s = ? OR (%[1]s >= '[' AND %[1]s < '\'))`,
				metadata.DefaultColumn+`->"meta"->"cardId"`),
			expectArgs: []any{`"C"`},
		},
		"DottedPathInPushed": {
			filter: must.NotFail(types.NewDocument("meta.cardId",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a", "b")))))),
			expectWhere: ` WHERE ` + fmt.Sprintf(
				`(%[1]s IN (?, ?) OR (%[1]s >= '[' AND %[1]s < '\'))`,
				metadata.DefaultColumn+`->"meta"->"cardId"`),
			expectArgs: []any{`"a"`, `"b"`},
		},
		"DottedPathRangeNotPushed": {
			// a range on a dotted path would need a scalar ->> on the raw key, which
			// is not a valid nested path, so it stays in the Go filter (no WHERE).
			filter: must.NotFail(types.NewDocument("meta.n",
				must.NotFail(types.NewDocument("$gt", int64(5))))),
		},
		"InEmptyMatchesNothing": {
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray()))))),
			expectWhere: ` WHERE 0`,
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

		// --- $regex (a substring filter {field: {$regex: text, $options: 'i'}}) ---
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
		// --- numeric/date range filters, pushed via ->> ---
		"RangeLtePushed": {
			filter: must.NotFail(types.NewDocument("count",
				must.NotFail(types.NewDocument("$lte", int64(100))))),
			expectWhere: ` WHERE ` + scalarExpr("count") + ` <= ?`,
			expectArgs:  []any{int64(100)},
		},
		"RangeDatePushed": {
			// a date bound is compared as its Unix-millis (how sjson stores dates).
			filter: must.NotFail(types.NewDocument("dueAt",
				must.NotFail(types.NewDocument("$lte", time.UnixMilli(1700))))),
			expectWhere: ` WHERE ` + scalarExpr("dueAt") + ` <= ?`,
			expectArgs:  []any{int64(1700)},
		},
		"RangeBetweenPushed": {
			// week filter shape {$gte: A, $lte: B}: both arms ANDed.
			filter: must.NotFail(types.NewDocument("dueAt",
				must.NotFail(types.NewDocument("$gte", int64(10), "$lte", int64(20))))),
			expectWhere: ` WHERE (` + scalarExpr("dueAt") + ` >= ? AND ` + scalarExpr("dueAt") + ` <= ?)`,
			expectArgs:  []any{int64(10), int64(20)},
		},
		"RangeStringBoundNotPushed": {
			// string ranges have collation/serialization subtleties; stay in Go.
			filter: must.NotFail(types.NewDocument("title",
				must.NotFail(types.NewDocument("$gt", "abc")))),
		},
		"RangeTimestampPushed": {
			// the capped-collection tail shape {ts: {$gt: <Timestamp>}}: a BSON
			// Timestamp is stored as its uint64 (a JSON number), so it pushes down
			// as a numeric ->> comparison — so an idle tail no longer decodes the
			// whole collection in Go on every awaitData poll.
			filter: must.NotFail(types.NewDocument("ts",
				must.NotFail(types.NewDocument("$gt", types.Timestamp(7300000000000000000))))),
			expectWhere: ` WHERE ` + scalarExpr("ts") + ` > ?`,
			expectArgs:  []any{int64(7300000000000000000)},
		},
		"RangeTimestampOverflowNotPushed": {
			// a Timestamp that would not fit a signed 64-bit int is left to Go, so
			// the SQL arg stays an exact integer (never a wrong/subset comparison).
			filter: must.NotFail(types.NewDocument("ts",
				must.NotFail(types.NewDocument("$gt", types.Timestamp(1<<63))))),
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
