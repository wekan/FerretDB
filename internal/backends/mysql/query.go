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
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/FerretDB/FerretDB/internal/backends/mysql/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/sqlguard"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// selectParams contains params that specify how prepareSelectClause function will
// build the SELECT SQL query.
type selectParams struct {
	Schema  string
	Table   string
	Comment string

	Capped        bool
	OnlyRecordIDs bool
}

// prepareSelectClause returns SELECT clause for default column of provided schema and table name.
//
//	For capped collection with onlyRecordIDs, it returns select clause for recordID column.
//
// For capped collection, it returns select clause for recordID column and default column.
func prepareSelectClause(params *selectParams) string {
	if params == nil {
		params = new(selectParams)
	}

	if params.Comment != "" {
		params.Comment = strings.ReplaceAll(params.Comment, "/*", "/ *")
		// A comment is the ONE piece of client text written into SQL instead of
		// bound, so it is made harmless first: sqlguard.SafeComment neutralises
		// anything that could end the block, open a nested one, or start a line
		// comment after it, and bounds the length.
		params.Comment = `/* ` + sqlguard.SafeComment(params.Comment) + ` */`
	}

	if params.Capped && params.OnlyRecordIDs {
		return fmt.Sprintf(
			`SELECT %s %s FROM %s.%s`,
			params.Comment,
			metadata.RecordIDColumn,
			metadata.QuoteIdent(params.Schema), metadata.QuoteIdent(params.Table),
		)
	}

	if params.Capped {
		return fmt.Sprintf(
			`SELECT %s %s, %s FROM %s.%s`,
			params.Comment,
			metadata.RecordIDColumn,
			metadata.DefaultColumn,
			metadata.QuoteIdent(params.Schema), metadata.QuoteIdent(params.Table),
		)
	}

	return fmt.Sprintf(
		`SELECT %s %s FROM %s.%s`,
		params.Comment,
		metadata.DefaultColumn,
		metadata.QuoteIdent(params.Schema), metadata.QuoteIdent(params.Table),
	)
}

func prepareOrderByClause(sort *types.Document) (string, []any) {
	if sort.Len() != 1 {
		return "", nil
	}

	v := must.NotFail(sort.Get("$natural"))
	var order string

	switch v.(int64) {
	case 1:
	// Ascending order
	case -1:
		order = " DESC"
	default:
		panic("not reachable")
	}

	return fmt.Sprintf(" ORDER BY %s%s", metadata.RecordIDColumn, order), nil
}

// Note on JSON_CONTAINS: its second argument must be a JSON DOCUMENT, and a bound
// parameter arrives as a string or a number, not as JSON - MySQL answered
// "Error 3146 (22032): Invalid data type for JSON data in argument 2 to function
// json_contains" for every equality, $ne and $in. So the candidate is always
// wrapped in JSON_EXTRACT(?, '$'), which parses the value we bind (sjson's own
// JSON text for every scalar).
//
// JSON_EXTRACT of the whole document rather than CAST(? AS JSON): **MariaDB has
// no JSON type** - JSON is an alias for LONGTEXT there - so a cast to it is a
// syntax error, and MariaDB answered "Error 1064" for every filtered query.
// JSON_EXTRACT(?, '$') parses the string on MySQL and returns it unchanged on
// MariaDB, so ONE statement serves both engines.

// jsonValue returns the value as the JSON TEXT sjson stores for it, to be bound
// and parsed back with JSON_EXTRACT(?, '$').
//
// Every scalar goes through it, not only strings: MySQL has no boolean, so a Go
// `true` is sent as 1 and `CAST(1 AS JSON)` is the JSON number 1, which does NOT
// match the stored JSON `true` - a `{archived: false}` query would have pushed a
// filter that matches nothing and silently returned no rows. Marshalling first
// means the candidate is byte-for-byte what the document holds.
func jsonValue(v any) string {
	return string(must.NotFail(sjson.MarshalSingleValue(v)))
}

// jsonPath returns the MySQL JSON path of a top-level field, as a value to BIND.
//
// `col->$.?` is not MySQL: the `->` operator wants a literal path string, and a
// placeholder there is a syntax error - every pushed-down filter came back as
// "Error 1064 (42000): You have an error in your SQL syntax", so the whole
// backend answered nothing but errors as soon as a query had a filter. The path
// goes through JSON_EXTRACT(col, ?) instead, which takes it as an argument.
//
// The member is quoted, so a field name containing a dot, a space or a quote is
// one member and not a path expression: `$."a.b"` reads the field literally
// named `a.b`, which is what a MongoDB top-level key is. A quote or a backslash
// inside the name is escaped, so a name can never end the quoted member early.
func jsonPath(field string) string {
	return `$."` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(field) + `"`
}

// schemaTypePath returns the path of the sjson SCHEMA entry for a field - the
// `t` of `$s.p.<field>` - which is how a pushed filter checks the stored type.
func schemaTypePath(field string) string {
	return `$."$s"."p"."` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(field) + `"."t"`
}

// elemMatchFields returns the stored JSON of FIELD and value, one pair per field of an
// $elemMatch {FIELD: value, ...} whose value is a plain scalar - the shape of
// every membership test. A field with an operator or another kind of value is
// left out: the condition only has to be a superset. ok=false when none is left.
func elemMatchFields(v any) ([][2]string, bool) {
	doc, ok := v.(*types.Document)
	if !ok || doc.Len() == 0 {
		return nil, false
	}

	var res [][2]string

	for _, field := range doc.Keys() {
		if field == "" || strings.HasPrefix(field, "$") || strings.ContainsRune(field, '.') {
			continue
		}

		switch value := must.NotFail(doc.Get(field)).(type) {
		case string, types.ObjectID, bool, int32:
		case int64:
			if value > int64(types.MaxSafeDouble) || value < -int64(types.MaxSafeDouble) {
				continue
			}
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) || value > types.MaxSafeDouble || value < -types.MaxSafeDouble {
				continue
			}
		default:
			continue
		}

		res = append(res, [2]string{
			string(must.NotFail(sjson.MarshalSingleValue(field))),
			string(must.NotFail(sjson.MarshalSingleValue(must.NotFail(doc.Get(field))))),
		})
	}

	return res, len(res) > 0
}

// withoutDottedKeys returns the branch without its dotted keys. The handler
// removes those from the top level of a filter, because a dotted path does not
// cross an array of documents in SQL (`items.k` against items: [{k: "b"}]); a
// branch of $and or $or reached here with them, and an $or of `items.k` lost
// the matches. Without them an $and branch only widens, and an $or branch left
// with nothing makes the whole $or stay in Go.
func withoutDottedKeys(doc *types.Document) *types.Document {
	dotted := false
	for _, k := range doc.Keys() {
		dotted = dotted || strings.ContainsRune(k, '.')
	}

	if !dotted {
		return doc
	}

	res := doc.DeepCopy()
	for _, k := range doc.Keys() {
		if strings.ContainsRune(k, '.') {
			res.Remove(k)
		}
	}

	return res
}

// logicalWhere is the pushdown of a top-level $and or $or, or "" when there is
// nothing to push down: an $or only when every branch pushes something.
func logicalWhere(op string, v any) (string, []any, error) {
	branches, ok := v.(*types.Array)
	if !ok || branches.Len() == 0 {
		return "", nil, nil
	}

	var conds []string
	var args []any

	for i := 0; i < branches.Len(); i++ {
		branch, ok := must.NotFail(branches.Get(i)).(*types.Document)
		if ok {
			branch = withoutDottedKeys(branch)
		}

		var where string
		var branchArgs []any

		if ok && branch.Len() > 0 {
			var err error
			if where, branchArgs, err = prepareWhereClause(branch); err != nil {
				return "", nil, lazyerrors.Error(err)
			}
		}

		if where == "" {
			if op == "$or" {
				return "", nil, nil
			}

			continue
		}

		conds = append(conds, "("+strings.TrimPrefix(where, " WHERE ")+")")
		args = append(args, branchArgs...)
	}

	if len(conds) == 0 {
		return "", nil, nil
	}

	joiner := " AND "
	if op == "$or" {
		joiner = " OR "
	}

	return "(" + strings.Join(conds, joiner) + ")", args, nil
}

// prepareWhereClause adds WHERE clause with given filters to the query and returns the query and arguments.
func prepareWhereClause(sqlFilters *types.Document) (string, []any, error) {
	return prepareWhereClauseWith(sqlFilters, whereOptions{})
}

// whereOptions are what prepareWhereClauseWith needs to know about the server.
type whereOptions struct {
	// textJSON: the server keeps JSON as text (MariaDB, where JSON is LONGTEXT),
	// so every JSON function call parses the whole document again. MySQL keeps
	// it binary. Measured on due cards, 61,507 cards: pushing the $and, $or and
	// $elemMatch conditions took MySQL from 1,957 to 904 ms, and MariaDB from
	// 1,651 to 3,406 - five JSON calls a row cost it more than decoding the
	// document once in Go. MariaDB keeps what an index answers (indexed
	// narrowing, the multikey side tables) and the top-level comparisons.
	textJSON bool
}

// prepareWhereClauseWith is prepareWhereClause for a server of the given kind.
func prepareWhereClauseWith(sqlFilters *types.Document, opts whereOptions) (string, []any, error) {
	var filters []string
	var args []any

	iter := sqlFilters.Iterator()
	defer iter.Close()

	// iterate through root document
	for {
		rootKey, rootVal, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			return "", nil, lazyerrors.Error(err)
		}

		// A top-level $and is pushed down branch by branch, and an $or when every
		// branch pushes something - see the PostgreSQL backend's prepareWhereClause.
		if rootKey == "$and" || rootKey == "$or" {
			if opts.textJSON {
				continue
			}

			cond, condArgs, err := logicalWhere(rootKey, rootVal)
			if err != nil {
				return "", nil, lazyerrors.Error(err)
			}

			if cond != "" {
				filters = append(filters, cond)
				args = append(args, condArgs...)
			}

			continue
		}

		// don't pushdown $comment, as it's attached to query with select clause
		if strings.HasPrefix(rootKey, "$") {
			continue
		}

		path, err := types.NewPathFromString(rootKey)

		var pe *types.PathError

		switch {
		case err == nil:
			// Handle dot notation
			if path.Len() > 1 {
				continue
			}
		case errors.As(err, &pe):
			// ignore empty key error, otherwise return error
			if pe.Code() != types.ErrPathElementEmpty {
				return "", nil, lazyerrors.Error(err)
			}
		default:
			panic("Invalid error type: PathError expected")
		}

		switch v := rootVal.(type) {
		case *types.Document:
			iter := v.Iterator()
			defer iter.Close()

			// iterate through subdocument, as it may contain operators
			for {
				k, v, err := iter.Next()
				if err != nil {
					if errors.Is(err, iterator.ErrIteratorDone) {
						break
					}

					return "", nil, lazyerrors.Error(err)
				}

				switch k {
				case "$eq":
					if f, a := filterEqual(rootKey, v); f != "" {
						filters = append(filters, f)
						args = append(args, a...)
					}

				case "$elemMatch":
					// {members: {$elemMatch: {userId: u, ...}}}: JSON_CONTAINS of
					// {"userId": u} - an object is contained in an array when it is in
					// one of its elements, and u in a FIELD that is an array holding it.
					// Each field on its own, so a superset. Without it, an $or of a
					// membership and the open permissions read every board.
					if fields, ok := elemMatchFields(v); ok && !opts.textJSON {
						for _, f := range fields {
							filters = append(filters, fmt.Sprintf(`JSON_CONTAINS(JSON_EXTRACT(%s, ?), JSON_EXTRACT(?, '$'))`, metadata.DefaultColumn))
							args = append(args, jsonPath(rootKey), `{`+f[0]+`:`+f[1]+`}`)
						}
					}

				case "$in":
					// {field: {$in: [...]}} -> OR of JSON_CONTAINS arms (one per pushable
					// element) + a null-or-missing arm; a SUPERSET, the Go filter
					// re-applies the exact $in. Parity with the other backends. Any
					// element with no safe arm (nested doc/array/binary/regex/Timestamp)
					// leaves the whole $in to the Go filter.
					if arr, ok := v.(*types.Array); ok {
						if f, a, ok := filterIn(rootKey, arr); ok {
							filters = append(filters, f)
							args = append(args, a...)
						}
					}

				case "$ne":
					// Every path and every value is BOUND: the type name is the last
					// piece of this that used to be formatted into the statement.
					sql := `NOT ( ` +
						// check if the value under the key is equal to filter value
						`JSON_CONTAINS(JSON_EXTRACT(%[1]s, ?), JSON_EXTRACT(?, '$'), '$') AND ` +
						// check if value type is equal to filter's
						`JSON_UNQUOTE(JSON_EXTRACT(%[1]s, ?)) = ? )`

					switch v := v.(type) {
					case *types.Document, *types.Array, types.Binary,
						types.NullType, types.Regex, types.Timestamp:
					// type not supported for pushdown

					case float64, bool, int32, int64, string, types.ObjectID, time.Time:
						filters = append(filters, fmt.Sprintf(sql, metadata.DefaultColumn))

						args = append(args, jsonPath(rootKey), jsonValue(v),
							schemaTypePath(rootKey), sjson.GetTypeOfValue(v))

					default:
						panic(fmt.Sprintf("Unexpected type of value: %v", v))
					}

				case "$gt", "$gte", "$lt", "$lte":
					// Push down a numeric / date / BSON-Timestamp range bound. sjson
					// stores int/double/Date(UnixMilli)/Timestamp(uint64) all as JSON
					// numbers. GUARD with JSON_TYPE(...) IN number types first, so a
					// non-number value cannot mis-compare and the pushed filter stays a
					// SUPERSET (only number-typed docs are pre-filtered; the in-Go filter
					// re-applies exact, type-bracketed comparison). Mainly this makes an
					// idle OpLog tail's {ts:{$gt}} an indexed range scan instead of a
					// whole-collection re-decode every awaitData poll.
					var sqlOp, relaxed string
					switch k {
					case "$gt":
						sqlOp, relaxed = ">", ">="
					case "$gte":
						sqlOp, relaxed = ">=", ">="
					case "$lt":
						sqlOp, relaxed = "<", "<="
					case "$lte":
						sqlOp, relaxed = "<=", "<="
					}

					// Dates and Timestamps are compared in DECIMAL form, which holds
					// their stored integers (UnixMilli, uint64) exactly. They were not
					// pushed down before because a live MySQL 9.7 answered a date range
					// with no rows: MySQL types such values as 'UNSIGNED INTEGER', which
					// the number guard did not list (see jsonNumberTypes).
					if temporal, ok := temporalBound(v); ok {
						filters = append(filters, decimalRange(rootKey, relaxed))
						args = append(args, decimalRangeArgs(rootKey, temporal)...)

						continue
					}

					num, ok := numericBound(v)
					if !ok {
						continue
					}

					filters = append(filters, fmt.Sprintf(
						`JSON_TYPE(JSON_EXTRACT(%[1]s, ?)) IN `+jsonNumberTypes+` `+
							`AND JSON_EXTRACT(%[1]s, ?) %[2]s ?`,
						metadata.DefaultColumn, sqlOp,
					))
					args = append(args, jsonPath(rootKey), jsonPath(rootKey), num)

					// The capped-collection "ts" index is on the DECIMAL form (see
					// decimalRange); neither MySQL nor MariaDB uses it for the comparison
					// above, so the indexable form is ANDed alongside it.
					if rootKey == "ts" {
						filters = append(filters, decimalRange(rootKey, relaxed))
						args = append(args, decimalRangeArgs(rootKey, num)...)
					}

				default:
					// other operators ($regex, $exists, …) stay in the Go filter.
					continue
				}
			}

		case *types.Array, types.Binary, types.NullType, types.Regex, types.Timestamp:
			// type not supported for pushdown

		case float64, string, types.ObjectID, bool, time.Time, int32, int64:
			if f, a := filterEqual(rootKey, v); f != "" {
				filters = append(filters, f)
				args = append(args, a...)
			}

		default:
			panic(fmt.Sprintf("Unexpected type of value: %v", v))
		}
	}

	var filter string
	if len(filters) > 0 {
		filter = ` WHERE ` + strings.Join(filters, " AND ")
	}

	return filter, args, nil
}

// maxJSONInValues is the longest $in list compared with JSON functions.
const maxJSONInValues = 32

// filterIn pushes down {field: {$in: [...]}} as JSON_CONTAINS for one value, one
// JSON_OVERLAPS for a few, or a plain IN of the unquoted value for many strings,
// plus, for a `null` element, a null-or-missing arm. A SUPERSET; the Go filter
// re-applies the exact $in. Returns ok=false when any element has no safe arm
// (nested doc/array/binary/regex/Timestamp), or for a long list of non-strings.
func filterIn(k string, arr *types.Array) (string, []any, bool) {
	var arms []string
	var args []any
	var values []string
	var plain []any // the values as plain strings, when every value is one
	allStrings := true
	hasNull := false

	for i := 0; i < arr.Len(); i++ {
		e := must.NotFail(arr.Get(i))

		switch e := e.(type) {
		case types.NullType:
			hasNull = true

		case string:
			values = append(values, jsonValue(e))
			plain = append(plain, e)

		case types.ObjectID, time.Time, float64, bool, int32, int64:
			values = append(values, jsonValue(e))
			allStrings = false

		default:
			return "", nil, false
		}
	}

	// A long list of strings is a plain IN of the unquoted value - MySQL sorts the
	// list once and binary-searches it for each row - with every array a
	// candidate. JSON_OVERLAPS compares each row with every value: the due cards
	// query's 2,596 board ids over 20,000 cards made 52 million comparisons.
	if allStrings && len(plain) > maxJSONInValues {
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(plain)), ", ")
		arms = append(arms, fmt.Sprintf(
			`(JSON_UNQUOTE(JSON_EXTRACT(%[1]s, ?)) IN (%[2]s) OR JSON_TYPE(JSON_EXTRACT(%[1]s, ?)) = 'ARRAY')`,
			metadata.DefaultColumn, placeholders))
		args = append(args, jsonPath(k))
		args = append(args, plain...)
		args = append(args, jsonPath(k))
		values = nil
	} else if len(values) > maxJSONInValues {
		// A long list of anything else is left to the Go filter: one JSON
		// comparison per value per row costs more than it saves.
		return "", nil, false
	}

	// One value is a JSON_CONTAINS; more are ONE JSON_OVERLAPS of the field and
	// the list - true when the field is one of the values or an array holding
	// one. The OR of a JSON_CONTAINS per value made every candidate row evaluate
	// all of them: the due cards query's 2,596 board ids, for each of 17,000
	// cards, ran for minutes.
	switch len(values) {
	case 0:
	case 1:
		arms = append(arms, fmt.Sprintf(`JSON_CONTAINS(JSON_EXTRACT(%s, ?), JSON_EXTRACT(?, '$'), '$')`, metadata.DefaultColumn))
		args = append(args, jsonPath(k), values[0])
	default:
		arms = append(arms, fmt.Sprintf(`JSON_OVERLAPS(JSON_EXTRACT(%s, ?), JSON_EXTRACT(?, '$'))`, metadata.DefaultColumn))
		args = append(args, jsonPath(k), "["+strings.Join(values, ",")+"]")
	}

	if hasNull {
		arms = append(arms, fmt.Sprintf(
			`(JSON_EXTRACT(%[1]s, ?) IS NULL OR JSON_TYPE(JSON_EXTRACT(%[1]s, ?)) = 'NULL')`,
			metadata.DefaultColumn))
		args = append(args, jsonPath(k), jsonPath(k))
	}

	if len(arms) == 0 {
		return "", nil, false
	}

	return "(" + strings.Join(arms, " OR ") + ")", args, true
}

// numericBound returns the numeric argument for a range bound that sjson stores as
// a JSON number — int32/int64/double — or ok=false for anything else, which is left
// to the Go filter.
//
// A DATE and a BSON TIMESTAMP are stored as JSON numbers too, and they used to be
// pushed down as their Unix-millis / uint64. A live run against MySQL 9.7 showed
// `{when: {$gte: <date>}}` returning NO rows where every other backend returned
// two: the pushed comparison excluded documents the filter matches, and a pushdown
// that is too NARROW is silently wrong - the in-Go filter never sees the rows the
// SQL did not return. Numbers behave (the float range case answers identically),
// so only the two temporal types are declined here, until a live EXPLAIN shows
// which expression MySQL needs for them. Correctness first; this costs one
// whole-collection re-decode for a date range on the experimental backend.
// jsonNumberTypes is every JSON_TYPE a stored number can have. MySQL 9.7
// reports some positive integers as 'UNSIGNED INTEGER' (1577934245000 is one,
// 2147483648 is not), so a guard without it silently dropped those documents
// from every pushed-down range - wrong answers, not just a slower query.
// MariaDB reports 'INTEGER' for the same values.
const jsonNumberTypes = `('INTEGER', 'UNSIGNED INTEGER', 'DOUBLE', 'DECIMAL')`

// temporalBound returns the stored JSON number of a Date (UnixMilli) or BSON
// Timestamp (uint64) range bound. Timestamps above MaxInt64 are not pushed.
func temporalBound(v any) (int64, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UnixMilli(), true
	case types.Timestamp:
		if uint64(t) > math.MaxInt64 {
			return 0, false
		}

		return int64(t), true
	default:
		return 0, false
	}
}

// decimalRange is a SUPERSET range filter on the DECIMAL(65,10) value of a
// top-level field, guarded by JSON_TYPE. relaxed must be ">=" or "<=":
// DECIMAL(65,10) rounds past ten decimals, and rounding never reverses order,
// so a non-strict comparison keeps every matching document; the Go filter
// re-applies the exact one.
//
// For the "ts" key the path is a LITERAL, so the expression is exactly the one
// metadata.collectionCreate indexes for capped collections: a functional key
// part on MySQL, an indexed STORED column on MariaDB. Verified with EXPLAIN on
// MySQL 9.7 and MariaDB 12.3: this form is a range scan on that index, and
// MySQL needs the bound CAST to DECIMAL too - with an integer or DOUBLE bound
// it scans the whole table.
func decimalRange(rootKey, relaxed string) string {
	path := "?"
	if rootKey == "ts" {
		path = "'$.ts'"
	}

	return fmt.Sprintf(
		`JSON_TYPE(JSON_EXTRACT(%[1]s, %[2]s)) IN `+jsonNumberTypes+` `+
			`AND CAST(JSON_UNQUOTE(JSON_EXTRACT(%[1]s, %[2]s)) AS DECIMAL(65,10)) %[3]s CAST(? AS DECIMAL(65,10))`,
		metadata.DefaultColumn, path, relaxed,
	)
}

// decimalRangeArgs returns the arguments decimalRange's placeholders take.
func decimalRangeArgs(rootKey string, bound any) []any {
	if rootKey == "ts" {
		return []any{bound}
	}

	return []any{jsonPath(rootKey), jsonPath(rootKey), bound}
}

func numericBound(v any) (any, bool) {
	switch n := v.(type) {
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return n, true
	default:
		return nil, false
	}
}

// filterEqual returns the proper SQL filter with arguments that filters documents
// where the value under k is equal to v.
func filterEqual(k string, v any) (filter string, args []any) {
	// Select if value under the key is equal to provided value.
	sql := `JSON_CONTAINS(JSON_EXTRACT(%s, ?), JSON_EXTRACT(?, '$'), '$')`

	switch v := v.(type) {
	case *types.Document, *types.Array, types.Binary,
		types.NullType, types.Regex, types.Timestamp:
		// type not supported for pushdown

	case float64:
		// If value is not safe double, fetch all numbers out of safe range.
		// TODO https://github.com/FerretDB/FerretDB/issues/3626
		switch {
		case v > types.MaxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) > ?`
			filter = fmt.Sprintf(sql, metadata.DefaultColumn)
			args = append(args, jsonPath(k), types.MaxSafeDouble)

			return

		case v < -types.MaxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) < ?`
			filter = fmt.Sprintf(sql, metadata.DefaultColumn)
			args = append(args, jsonPath(k), -types.MaxSafeDouble)

			return
		}

		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), jsonValue(v))

	case string, types.ObjectID, time.Time, bool, int32:
		// don't change the default eq query
		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), jsonValue(v))

	case int64:
		maxSafeDouble := int64(types.MaxSafeDouble)

		// If value cannot be safe double, fetch all numbers out of the safe range.
		switch {
		case v > maxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) > ?`
			filter = fmt.Sprintf(sql, metadata.DefaultColumn)
			args = append(args, jsonPath(k), maxSafeDouble)

			return

		case v < -maxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) < ?`
			filter = fmt.Sprintf(sql, metadata.DefaultColumn)
			args = append(args, jsonPath(k), -maxSafeDouble)

			return
		}

		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), jsonValue(v))

	default:
		panic(fmt.Sprintf("Unexpected type of value: %v", v))
	}

	return
}

// indexedNarrowing returns a condition that lets MySQL and MariaDB answer an
// equality or $in on an indexed top-level field from that field's index, to be
// ANDed with prepareWhereClause's.
//
// The pushed-down equality is JSON_CONTAINS(JSON_EXTRACT(doc, ?), ...), which no
// index can serve: a document by _id was found by reading the whole table (3.4 s
// on MySQL and 18.7 s on MariaDB for one of 600,000), and so was every update,
// which reads its document first. An index's key is a generated column holding
// JSON_EXTRACT(doc, '$.field') as text, so `column = '"value"'` is an index
// lookup. It narrows to a SUPERSET of the matches, and JSON_CONTAINS still
// decides: MongoDB's {field: value} also matches an array that contains value,
// so every array (its text starts with '[') stays a candidate; a column of an
// older table that is case-insensitive only widens the candidates.
//
// Only values whose stored text is certain are used: an ObjectID, and a string
// of plain printable ASCII with nothing JSON or the encoder escapes. A number is
// not - MySQL renders 5.0 where the client sent 5 - and neither is any other
// string; they keep the previous, unindexed condition.
func indexedNarrowing(filter *types.Document, indexed map[string]struct{}) (string, []any) {
	if filter == nil || len(indexed) == 0 {
		return "", nil
	}

	var conds []string
	var args []any

	for _, key := range filter.Keys() {
		// Every branch of a top-level $and must hold, so what each narrows to is
		// ANDed too: {$and: [{boardId: {$in: [...]}}, ...]} uses the index.
		if key == "$and" {
			if branches, ok := must.NotFail(filter.Get(key)).(*types.Array); ok {
				for i := 0; i < branches.Len(); i++ {
					if branch, ok := must.NotFail(branches.Get(i)).(*types.Document); ok {
						if cond, condArgs := indexedNarrowing(branch, indexed); cond != "" {
							conds = append(conds, "("+cond+")")
							args = append(args, condArgs...)
						}
					}
				}
			}

			continue
		}

		if _, ok := indexed[key]; !ok || key == "" || strings.HasPrefix(key, "$") || strings.ContainsRune(key, '.') {
			continue
		}

		values, ok := narrowableValues(must.NotFail(filter.Get(key)))
		if !ok {
			continue
		}

		col := metadata.QuoteIdent(metadata.SafeColumnName(key))
		placeholders := make([]string, len(values))

		for i, v := range values {
			placeholders[i] = "?"
			args = append(args, jsonValue(v))
		}

		arm := fmt.Sprintf("%s = ?", col)
		if len(values) > 1 {
			arm = fmt.Sprintf("%s IN (%s)", col, strings.Join(placeholders, ", "))
		}

		if key == "_id" {
			conds = append(conds, arm) // _id is never an array
		} else {
			conds = append(conds, fmt.Sprintf("(%s OR %s LIKE '[%%')", arm, col))
		}
	}

	return strings.Join(conds, " AND "), args
}

// narrowableValues returns the values of {field: v}, {field: {$eq: v}} or
// {field: {$in: [...]}} when every one of them has a certain stored text.
func narrowableValues(v any) ([]any, bool) {
	switch v := v.(type) {
	case string, types.ObjectID:
		return []any{v}, certainText(v)

	case *types.Document:
		if v.Len() != 1 {
			return nil, false
		}

		switch v.Keys()[0] {
		case "$eq":
			e := must.NotFail(v.Get("$eq"))
			return []any{e}, certainText(e)

		case "$in":
			arr, ok := must.NotFail(v.Get("$in")).(*types.Array)
			if !ok || arr.Len() == 0 {
				return nil, false
			}

			values := make([]any, arr.Len())
			for i := range values {
				values[i] = must.NotFail(arr.Get(i))
				if !certainText(values[i]) {
					return nil, false
				}
			}

			return values, true
		}
	}

	return nil, false
}

// certainText reports a value whose JSON text MySQL and MariaDB render exactly
// as jsonValue does.
func certainText(v any) bool {
	switch v := v.(type) {
	case types.ObjectID:
		return true
	case string:
		for i := 0; i < len(v); i++ {
			c := v[i]
			if c < 0x20 || c > 0x7e || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
				return false
			}
		}

		return true
	default:
		return false
	}
}

// indexedFields returns the top-level fields that have a generated index column.
func indexedFields(indexes metadata.Indexes) map[string]struct{} {
	res := map[string]struct{}{}

	for _, index := range indexes {
		for _, key := range index.Key {
			if !strings.ContainsRune(key.Field, '.') {
				res[key.Field] = struct{}{}
			}
		}
	}

	return res
}
