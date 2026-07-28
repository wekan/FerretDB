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
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends/mysql/metadata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// The comment is the one piece of client text written into SQL rather than bound,
// so it is neutralised by sqlguard.SafeComment: `*/` cannot end the block early,
// `/*` cannot open a nested one, and `--` cannot start a line comment if the block
// ever does end early. That is why the expectations below carry "* /" and "- -".
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
				"SELECT %s %s FROM `%s`.`%s`",
				"/* * / 1; DROP SCHEMA "+schema+" CASCADE - -  */",
				metadata.RecordIDColumn,
				schema,
				table,
			),
		},
		"Capped": {
			capped: true,
			expectQuery: fmt.Sprintf(
				"SELECT %s %s, %s FROM `%s`.`%s`",
				"/* * / 1; DROP SCHEMA "+schema+" CASCADE - -  */",
				metadata.RecordIDColumn,
				metadata.DefaultColumn,
				schema,
				table,
			),
		},
		"FullRecord": {
			expectQuery: fmt.Sprintf(
				"SELECT %s %s FROM `%s`.`%s`",
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
	// The path is BOUND, through JSON_EXTRACT: `col->$.?` is a MySQL syntax error
	// (the `->` operator wants a literal path), which is what made every filtered
	// query answer "Error 1064 (42000)".
	whereContain := " WHERE JSON_CONTAINS(JSON_EXTRACT(_ferretdb_sjson, ?), JSON_EXTRACT(?, '$'), '$')"
	whereGt := " WHERE JSON_EXTRACT(_ferretdb_sjson, ?) > ?"
	whereNotEq := ` WHERE NOT ( JSON_CONTAINS(JSON_EXTRACT(_ferretdb_sjson, ?), JSON_EXTRACT(?, '$'), '$') AND ` +
		`JSON_UNQUOTE(JSON_EXTRACT(_ferretdb_sjson, ?)) = ? )`

	for name, tc := range map[string]struct {
		filter   *types.Document
		expected string
		skip     string
		args     []any // if empty, check is disabled
	}{
		"IDObjectID": {
			filter:   must.NotFail(types.NewDocument("_id", objectID)),
			expected: whereContain,
		},
		"IDString": {
			filter:   must.NotFail(types.NewDocument("_id", "foo")),
			expected: whereContain,
		},
		"IDBool": {
			filter:   must.NotFail(types.NewDocument("_id", "foo")),
			expected: whereContain,
		},
		"IDDotNotation": {
			filter: must.NotFail(types.NewDocument("_id.doc", "foo")),
		},

		"DotNotation": {
			filter: must.NotFail(types.NewDocument("v.doc", "foo")),
		},
		"DotNotationArrayIndex": {
			filter: must.NotFail(types.NewDocument("v.arr.0", "foo")),
		},

		// Numeric / date / Timestamp range pushdown, guarded by JSON_TYPE so a
		// non-number value can never mis-compare.
		"RangeTimestampGt": {
			// {ts: {$gt: <Timestamp>}} — the OpLog tail shape.
			filter: must.NotFail(types.NewDocument("ts",
				must.NotFail(types.NewDocument("$gt", types.Timestamp(7300000000000000000))))),
			expected: ` WHERE JSON_TYPE(JSON_EXTRACT(_ferretdb_sjson, ?)) IN ('INTEGER', 'DOUBLE', 'DECIMAL') ` +
				`AND JSON_EXTRACT(_ferretdb_sjson, ?) > ?`,
			args: []any{`$."ts"`, `$."ts"`, int64(7300000000000000000)},
		},
		"RangeNumberLte": {
			filter: must.NotFail(types.NewDocument("count",
				must.NotFail(types.NewDocument("$lte", int64(100))))),
			expected: ` WHERE JSON_TYPE(JSON_EXTRACT(_ferretdb_sjson, ?)) IN ('INTEGER', 'DOUBLE', 'DECIMAL') ` +
				`AND JSON_EXTRACT(_ferretdb_sjson, ?) <= ?`,
			args: []any{`$."count"`, `$."count"`, int64(100)},
		},
		"RangeStringBoundNotPushed": {
			// a non-number bound stays in the Go filter (no WHERE).
			filter: must.NotFail(types.NewDocument("v",
				must.NotFail(types.NewDocument("$gt", "abc")))),
		},

		// $in pushdown: an OR of JSON_CONTAINS arms, plus a null-or-missing arm.
		"InPushed": {
			filter: must.NotFail(types.NewDocument("labelIds",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("a", "b")))))),
			expected: ` WHERE (JSON_CONTAINS(JSON_EXTRACT(_ferretdb_sjson, ?), JSON_EXTRACT(?, '$'), '$') ` +
				`OR JSON_CONTAINS(JSON_EXTRACT(_ferretdb_sjson, ?), JSON_EXTRACT(?, '$'), '$'))`,
			args: []any{`$."labelIds"`, `"a"`, `$."labelIds"`, `"b"`},
		},
		"InWithNullPushed": {
			filter: must.NotFail(types.NewDocument("boardId",
				must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("B", types.Null)))))),
			expected: ` WHERE (JSON_CONTAINS(JSON_EXTRACT(_ferretdb_sjson, ?), JSON_EXTRACT(?, '$'), '$') ` +
				`OR (JSON_EXTRACT(_ferretdb_sjson, ?) IS NULL ` +
				`OR JSON_TYPE(JSON_EXTRACT(_ferretdb_sjson, ?)) = 'NULL'))`,
			args: []any{`$."boardId"`, `"B"`, `$."boardId"`, `$."boardId"`},
		},

		"ImplicitString": {
			filter:   must.NotFail(types.NewDocument("v", "foo")),
			expected: whereContain,
		},
		"ImplicitEmptyString": {
			filter:   must.NotFail(types.NewDocument("v", "")),
			expected: whereContain,
		},
		"ImplicitInt32": {
			filter:   must.NotFail(types.NewDocument("v", int32(42))),
			expected: whereContain,
		},
		"ImplicitInt64": {
			filter:   must.NotFail(types.NewDocument("v", int64(42))),
			expected: whereContain,
		},
		"ImplicitFloat64": {
			filter:   must.NotFail(types.NewDocument("v", float64(42.13))),
			expected: whereContain,
		},
		"ImplicitMaxFloat64": {
			filter:   must.NotFail(types.NewDocument("v", math.MaxFloat64)),
			expected: whereGt,
		},
		"ImplicitBool": {
			filter:   must.NotFail(types.NewDocument("v", true)),
			expected: whereContain,
		},
		"ImplicitDatetime": {
			filter: must.NotFail(types.NewDocument(
				"v", time.Date(2021, 11, 1, 10, 18, 42, 123000000, time.UTC),
			)),
			expected: whereContain,
		},
		"ImplicitObjectID": {
			filter:   must.NotFail(types.NewDocument("v", objectID)),
			expected: whereContain,
		},

		"EqString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", "foo")),
			)),
			args:     []any{`$."v"`, `"foo"`},
			expected: whereContain,
		},
		"EqEmptyString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", "")),
			)),
			expected: whereContain,
		},
		"EqInt32": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", int32(42))),
			)),
			expected: whereContain,
		},
		"EqInt64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", int64(42))),
			)),
			expected: whereContain,
		},
		"EqFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", float64(42.13))),
			)),
			expected: whereContain,
		},
		"EqMaxFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", math.MaxFloat64)),
			)),
			args:     []any{`$."v"`, types.MaxSafeDouble},
			expected: whereGt,
		},
		"EqDoubleBigInt64": {
			filter: must.NotFail(types.NewDocument(
				// TODO https://github.com/FerretDB/FerretDB/issues/3626
				"v", must.NotFail(types.NewDocument("$eq", float64(2<<61))),
			)),
			args:     []any{`$."v"`, types.MaxSafeDouble},
			expected: whereGt,
		},
		"EqBool": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", true)),
			)),
			expected: whereContain,
		},
		"EqDatetime": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument(
					"$eq", time.Date(2021, 11, 1, 10, 18, 42, 123000000, time.UTC),
				)),
			)),
			expected: whereContain,
		},
		"EqObjectID": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", objectID)),
			)),
			expected: whereContain,
		},

		"NeString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", "foo")),
			)),
			expected: whereNotEq,
		},
		"NeEmptyString": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", "")),
			)),
			expected: whereNotEq,
		},
		"NeInt32": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", int32(42))),
			)),
			expected: whereNotEq,
		},
		"NeInt64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", int64(42))),
			)),
			expected: whereNotEq,
		},
		"NeFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", float64(42.13))),
			)),
			expected: whereNotEq,
		},
		"NeMaxFloat64": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", math.MaxFloat64)),
			)),
			// $ne binds four values: the field path, the value AS ITS sjson TEXT (the
			// candidate is JSON_EXTRACT(?, '$'), so it must be what the document holds -
			// a Go bool would otherwise be sent as 1 and never match `true`), the
			// path of the stored TYPE, and the type name.
			args: []any{`$."v"`, "1.7976931348623157e+308", `$."$s"."p"."v"."t"`, "double"},
			expected: whereNotEq,
		},
		"NeBool": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", true)),
			)),
			expected: whereNotEq,
		},
		"NeDatetime": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument(
					"$ne", time.Date(2021, 11, 1, 10, 18, 42, 123000000, time.UTC),
				)),
			)),
			expected: whereNotEq,
		},
		"NeObjectID": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$ne", objectID)),
			)),
			expected: whereNotEq,
		},

		// MySQL has no boolean: a Go `true` bound directly is 1, and CAST(1 AS JSON)
		// does not match the stored JSON `true` - a {archived: false} query would
		// have pushed a filter that matches nothing. The candidate is the value's
		// own sjson text.
		"EqBoolIsJSONTrue": {
			filter: must.NotFail(types.NewDocument(
				"v", must.NotFail(types.NewDocument("$eq", true)),
			)),
			args:     []any{`$."v"`, "true"},
			expected: whereContain,
		},
		"ImplicitBoolFalseIsJSONFalse": {
			filter:   must.NotFail(types.NewDocument("archived", false)),
			args:     []any{`$."archived"`, "false"},
			expected: whereContain,
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

			actual, args, err := prepareWhereClause(tc.filter)
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
