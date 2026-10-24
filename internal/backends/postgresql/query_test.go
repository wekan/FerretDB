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
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends/postgresql/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// The comment is the one piece of client text written into SQL rather than bound,
// so it is neutralised by sqlguard.SafeComment: `*/` cannot end the block early,
// `/*` cannot open a nested one, and `--` cannot start a line comment if the block
// ever does end early. Hence "* /" and "- -" below.
func TestPrepareSelectClause(t *testing.T) {
	t.Parallel()
	schema := "schema"
	table := "table"
	comment := "*/ 1; DROP SCHEMA " + schema + " CASCADE -- "

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		capped        bool
		onlyRecordIDs bool

		expectQuery string
	}{
		"CappedRecordID": {
			capped:        true,
			onlyRecordIDs: true,
			expectQuery: fmt.Sprintf(
				`SELECT %s %s FROM "%s"."%s"`,
				"/* * / 1; DROP SCHEMA "+schema+" CASCADE - -  */",
				metadata.RecordIDColumn,
				schema,
				table,
			),
		},
		"Capped": {
			capped: true,
			expectQuery: fmt.Sprintf(
				`SELECT %s %s, %s FROM "%s"."%s"`,
				"/* * / 1; DROP SCHEMA "+schema+" CASCADE - -  */",
				metadata.RecordIDColumn,
				metadata.DefaultColumn,
				schema,
				table,
			),
		},
		"FullRecord": {
			expectQuery: fmt.Sprintf(
				`SELECT %s %s FROM "%s"."%s"`,
				"/* * / 1; DROP SCHEMA "+schema+" CASCADE - -  */",
				metadata.DefaultColumn,
				schema,
				table,
			),
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			query := prepareSelectClause(&selectParams{
				Schema:        schema,
				Table:         table,
				Comment:       comment,
				Capped:        tc.capped,
				OnlyRecordIDs: tc.onlyRecordIDs,
			})

			assert.Equal(t, tc.expectQuery, query)
		})
	}
}

func TestPrepareWhereClause(t *testing.T) {
	t.Parallel()
	objectID := types.ObjectID{0x62, 0x56, 0xc5, 0xba, 0x0b, 0xad, 0xc0, 0xff, 0xee, 0xff, 0xff, 0xff}

	// WHERE clauses occurring frequently in tests
	// A top-level equality names its field as a literal, as the index does
	// (filterEqualIndexed), so PostgreSQL can answer it from that index.
	whereEqID := " WHERE _jsonb->'_id' = $1"
	whereEqV := ` WHERE (_jsonb->'v' = $1 OR (_jsonb->'v' >= '[null]'::jsonb AND _jsonb->'v' < '{}'::jsonb AND _jsonb->'v' @> $1))`
	whereContainDotNotation := " WHERE _jsonb#>$1 @> $2"

	whereGt := " WHERE _jsonb->$1 > $2"
	whereNotEq := ` WHERE NOT ( _jsonb ? $1 AND _jsonb->$1 @> $2 AND _jsonb->'$s'->'p'->$1->'t' = `

	for name, tc := range map[string]struct {
		filter   *types.Document
		expected string
		skip     string
		args     []any // if empty, check is disabled
	}{
		"IDObjectID": {
			filter:   must.NotFail(types.NewDocument("_id", objectID)),
			expected: whereEqID,
		},
		"IDString": {
			filter:   must.NotFail(types.NewDocument("_id", "foo")),
			expected: whereEqID,
		},
		"IDBool": {
			filter:   must.NotFail(types.NewDocument("_id", "foo")),
			expected: whereEqID,
		},
		"IDDotNotation": {
			filter:   must.NotFail(types.NewDocument("_id.doc", "foo")),
			expected: whereContainDotNotation,
		},

		"DotNotation": {
			filter:   must.NotFail(types.NewDocument("v.doc", "foo")),
			expected: whereContainDotNotation,
		},
		"DotNotationArrayIndex": {
			filter:   must.NotFail(types.NewDocument("v.arr.0", "foo")),
			expected: whereContainDotNotation,
		},

		// Numeric / date / Timestamp range pushdown, guarded by jsonb_typeof so a
		// non-number value can never crash PostgreSQL's strict ::numeric cast.
		"RangeTimestampGt": {
			// {ts: {$gt: <Timestamp>}} — the OpLog tail shape.
			filter: must.NotFail(types.NewDocument("ts",
				must.NotFail(types.NewDocument("$gt", types.Timestamp(7300000000000000000))))),
			// The key is a LITERAL, matching the capped-collection index expression
			// ((_jsonb->>'ts')::numeric); a parameter key makes generic plans scan.
			expected: ` WHERE jsonb_typeof(_jsonb->'ts') = 'number' AND (_jsonb->>'ts')::numeric > $1`,
			args:     []any{int64(7300000000000000000)},
		},
		"RangeNestedTsKeepsParameters": {
			// Only the top-level ts field is indexed; a nested one keeps the path
			// as a parameter.
			filter: must.NotFail(types.NewDocument("a.ts",
				must.NotFail(types.NewDocument("$gt", int64(5))))),
			expected: ` WHERE jsonb_typeof(_jsonb#>$1) = 'number' AND (_jsonb#>>$2)::numeric > $3`,
			args:     []any{[]string{"a", "ts"}, []string{"a", "ts"}, int64(5)},
		},
		"RangeNumberLte": {
			filter: must.NotFail(types.NewDocument("count",
				must.NotFail(types.NewDocument("$lte", int64(100))))),
			expected: ` WHERE jsonb_typeof(_jsonb->$1) = 'number' AND (_jsonb->>$2)::numeric <= $3`,
			args:     []any{"count", "count", int64(100)},
		},
		"RangeStringBoundNotPushed": {
			// a non-number bound stays in the Go filter (no WHERE).
			filter: must.NotFail(types.NewDocument("v",
				must.NotFail(types.NewDocument("$gt", "abc")))),
			expected: "",
		},

		// $in pushdown: an OR of containment arms, plus a null-or-missing arm.
		"InPushed": {
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a", "b")))))),
			expected: ` WHERE (_jsonb->'labelIds' IN ($1, $2) OR (_jsonb->'labelIds' >= '[null]'::jsonb AND _jsonb->'labelIds' < '{}'::jsonb AND (_jsonb->'labelIds' @> $1 OR _jsonb->'labelIds' @> $2)))`,
			args:     []any{`"a"`, `"b"`},
		},
		"InWithNullPushed": {
			filter: must.NotFail(types.NewDocument("boardId",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("B", types.Null)))))),
			expected: ` WHERE (_jsonb->'boardId' IN ($1) OR (_jsonb->'boardId' >= '[null]'::jsonb AND _jsonb->'boardId' < '{}'::jsonb AND (_jsonb->'boardId' @> $1)) OR (_jsonb->'boardId' IS NULL OR _jsonb->'boardId' = 'null'::jsonb))`,
			args:     []any{`"B"`},
		},
		"InNestedElementNotPushed": {
			// a nested-array element has no safe arm -> the whole $in stays in Go.
			filter: must.NotFail(types.NewDocument("v",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a",
					must.NotFail(types.NewArray("x")))))))),
			expected: "",
		},

		"ImplicitString": {
			filter:   must.NotFail(types.NewDocument("v", "foo")),
			expected: whereEqV,
		},
		"ImplicitEmptyString": {
			filter:   must.NotFail(types.NewDocument("v", "")),
			expected: whereEqV,
		},
		"ImplicitInt32": {
			filter:   must.NotFail(types.NewDocument("v", int32(42))),
			expected: whereEqV,
		},
		"ImplicitInt64": {
			filter:   must.NotFail(types.NewDocument("v", int64(42))),
			expected: whereEqV,
		},
		"ImplicitFloat64": {
			filter:   must.NotFail(types.NewDocument("v", float64(42.13))),
			expected: whereEqV,
		},
		"ImplicitMaxFloat64": {
			filter:   must.NotFail(types.NewDocument("v", math.MaxFloat64)),
			expected: whereGt,
		},
		"ImplicitBool": {
			filter:   must.NotFail(types.NewDocument("v", true)),
			expected: whereEqV,
		},
		"ImplicitDatetime": {
			filter: must.NotFail(types.NewDocument(
				"v", time.Date(2021, 11, 1, 10, 18, 42, 123000000, time.UTC),
			)),
			expected: whereEqV,
		},
		"ImplicitObjectID": {
			filter:   must.NotFail(types.NewDocument("v", objectID)),
			expected: whereEqV,
		},

		"EqString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", "foo")),
			)),
			args:     []any{`"foo"`},
			expected: whereEqV,
		},
		"EqEmptyString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", "")),
			)),
			expected: whereEqV,
		},
		"EqInt32": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", int32(42))),
			)),
			expected: whereEqV,
		},
		"EqInt64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", int64(42))),
			)),
			expected: whereEqV,
		},
		"EqFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", float64(42.13))),
			)),
			expected: whereEqV,
		},
		"EqMaxFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", math.MaxFloat64)),
			)),
			args:     []any{`v`, types.MaxSafeDouble},
			expected: whereGt,
		},
		"EqDoubleBigInt64": {
			filter: must.NotFail(types.NewDocument(
				// TODO https://github.com/FerretDB/FerretDB/issues/3626
				"v", must.NotFail(types.NewDocument("$eq", float64(2<<61))),
			)),
			args:     []any{`v`, types.MaxSafeDouble},
			expected: whereGt,
		},
		"EqBool": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", true)),
			)),
			expected: whereEqV,
		},
		"EqDatetime": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument(
					"$eq", time.Date(2021, 11, 1, 10, 18, 42, 123000000, time.UTC),
				)),
			)),
			expected: whereEqV,
		},
		"EqObjectID": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", objectID)),
			)),
			expected: whereEqV,
		},

		"NeString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", "foo")),
			)),
			expected: whereNotEq + `'"string"' )`,
		},
		"NeEmptyString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", "")),
			)),
			expected: whereNotEq + `'"string"' )`,
		},
		"NeInt32": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", int32(42))),
			)),
			expected: whereNotEq + `'"int"' )`,
		},
		"NeInt64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", int64(42))),
			)),
			expected: whereNotEq + `'"long"' )`,
		},
		"NeFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", float64(42.13))),
			)),
			expected: whereNotEq + `'"double"' )`,
		},
		"NeMaxFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", math.MaxFloat64)),
			)),
			args:     []any{`v`, math.MaxFloat64},
			expected: whereNotEq + `'"double"' )`,
		},
		"NeBool": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", true)),
			)),
			expected: whereNotEq + `'"bool"' )`,
		},
		"NeDatetime": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument(
					"$ne", time.Date(2021, 11, 1, 10, 18, 42, 123000000, time.UTC),
				)),
			)),
			expected: whereNotEq + `'"date"' )`,
		},
		"NeObjectID": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", objectID)),
			)),
			expected: whereNotEq + `'"objectId"' )`,
		},

		"Comment": {
			filter: must.NotFail(types.NewDocument("$comment", "I'm comment")),
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			actual, args, err := prepareWhereClause(new(metadata.Placeholder), tc.filter)
			require.NoError(t, err)

			assert.Equal(t, tc.expected, actual)

			if len(tc.args) == 0 {
				return
			}

			assert.Equal(t, tc.args, args)
		})
	}
}

func TestPrepareOrderByClause(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		sort *types.Document
		skip string

		orderBy string
		args    []any
	}{
		"Ascending": {
			skip:    "https://github.com/FerretDB/FerretDB/issues/3181",
			sort:    must.NotFail(types.NewDocument("field", int64(1))),
			orderBy: ` ORDER BY _jsonb->$1`,
			args:    []any{"field"},
		},
		"Descending": {
			skip:    "https://github.com/FerretDB/FerretDB/issues/3181",
			sort:    must.NotFail(types.NewDocument("field", int64(-1))),
			orderBy: ` ORDER BY _jsonb->$1 DESC`,
			args:    []any{"field"},
		},
		"SortNil": {
			orderBy: "",
			args:    nil,
		},
		"SortDotNotation": {
			skip:    "https://github.com/FerretDB/FerretDB/issues/3181",
			sort:    must.NotFail(types.NewDocument("field.embedded", int64(-1))),
			orderBy: "",
			args:    nil,
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

			orderBy, args := prepareOrderByClause(tc.sort)

			assert.Equal(t, tc.orderBy, orderBy)
			assert.Equal(t, tc.args, args)
		})
	}
}

func TestPrepareWhereClauseAnd(t *testing.T) {
	t.Parallel()

	doc := func(pairs ...any) *types.Document { return must.NotFail(types.NewDocument(pairs...)) }
	arr := func(values ...any) *types.Array { return must.NotFail(types.NewArray(values...)) }

	// The due cards shape: the board ids inside an $and, beside an $or that
	// cannot be pushed down. The $in still reaches the boardId index.
	filter := doc("archived", false, "$and", arr(
		doc("boardId", doc("$in", arr("b1", "b2"))),
		doc("$or", arr(doc("members", "u1"), doc("userId", "u1"))),
	))
	where, args, err := prepareWhereClause(new(metadata.Placeholder), filter)
	require.NoError(t, err)
	assert.Contains(t, where, `(_jsonb->'boardId' IN ($`)
	assert.Contains(t, where, ` AND ((_jsonb->'boardId' IN (`)
	assert.Contains(t, args, `"b1"`)
	assert.Contains(t, args, `"b2"`)

	// The same condition as without the wrapper.
	flat, flatArgs, err := prepareWhereClause(new(metadata.Placeholder), doc("boardId", doc("$in", arr("b1", "b2"))))
	require.NoError(t, err)
	wrapped, wrappedArgs, err := prepareWhereClause(new(metadata.Placeholder),
		doc("$and", arr(doc("boardId", doc("$in", arr("b1", "b2"))))))
	require.NoError(t, err)
	assert.Equal(t, ` WHERE (`+strings.TrimPrefix(flat, ` WHERE `)+`)`, wrapped)
	assert.Equal(t, flatArgs, wrappedArgs)

	// Negative: an $and with nothing to push down adds nothing, and one that is
	// not an array of documents is left to the Go filter.
	for name, f := range map[string]*types.Document{
		"only an $or":     doc("$and", arr(doc("$or", arr(doc("a", "x"))))),
		"not an array":    doc("$and", "x"),
		"empty branches":  doc("$and", arr(doc(), "x")),
		"an empty $and":   doc("$and", arr()),
		"regex in branch": doc("$and", arr(doc("a", types.Regex{Pattern: "x"}))),
	} {
		where, args, err := prepareWhereClause(new(metadata.Placeholder), f)
		require.NoError(t, err, name)
		assert.Empty(t, where, name)
		assert.Empty(t, args, name)
	}
}
