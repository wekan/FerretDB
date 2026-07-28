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
// wrapped in CAST(? AS JSON), which parses the value we bind (sjson's own JSON
// text for a string/ObjectID/date, and the number or boolean itself otherwise).

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

// prepareWhereClause adds WHERE clause with given filters to the query and returns the query and arguments.
func prepareWhereClause(sqlFilters *types.Document) (string, []any, error) {
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

		// don't pushdown $comment, as it's attached to query with select clause
		//
		// all of the other top-level operators such as `$or` do not support pushdown yet
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
						`JSON_CONTAINS(JSON_EXTRACT(%[1]s, ?), CAST(? AS JSON), '$') AND ` +
						// check if value type is equal to filter's
						`JSON_UNQUOTE(JSON_EXTRACT(%[1]s, ?)) = ? )`

					switch v := v.(type) {
					case *types.Document, *types.Array, types.Binary,
						types.NullType, types.Regex, types.Timestamp:
					// type not supported for pushdown

					case float64, bool, int32, int64:
						filters = append(filters, fmt.Sprintf(sql, metadata.DefaultColumn))

						args = append(args, jsonPath(rootKey), v,
							schemaTypePath(rootKey), sjson.GetTypeOfValue(v))

					case string, types.ObjectID, time.Time:
						filters = append(filters, fmt.Sprintf(sql, metadata.DefaultColumn))

						args = append(args, jsonPath(rootKey),
							string(must.NotFail(sjson.MarshalSingleValue(v))),
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
					// whole-collection re-decode every awaitData poll. (Parity with the
					// sqlite/postgresql backends.)
					num, ok := numericBound(v)
					if !ok {
						continue
					}

					var sqlOp string
					switch k {
					case "$gt":
						sqlOp = ">"
					case "$gte":
						sqlOp = ">="
					case "$lt":
						sqlOp = "<"
					case "$lte":
						sqlOp = "<="
					}

					filters = append(filters, fmt.Sprintf(
						`JSON_TYPE(JSON_EXTRACT(%[1]s, ?)) IN ('INTEGER', 'DOUBLE', 'DECIMAL') `+
							`AND JSON_EXTRACT(%[1]s, ?) %[2]s ?`,
						metadata.DefaultColumn, sqlOp,
					))
					args = append(args, jsonPath(rootKey), jsonPath(rootKey), num)

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

// filterIn pushes down {field: {$in: [...]}} as an OR of JSON_CONTAINS arms (one per
// pushable element, same shape as equality) plus, for a `null` element, a
// null-or-missing arm. A SUPERSET; the Go filter re-applies the exact $in. Returns
// ok=false when any element has no safe arm (nested doc/array/binary/regex/Timestamp).
func filterIn(k string, arr *types.Array) (string, []any, bool) {
	var arms []string
	var args []any
	hasNull := false

	for i := 0; i < arr.Len(); i++ {
		e := must.NotFail(arr.Get(i))

		switch e.(type) {
		case types.NullType:
			hasNull = true

		case string, types.ObjectID, time.Time:
			arms = append(arms, fmt.Sprintf(`JSON_CONTAINS(JSON_EXTRACT(%s, ?), CAST(? AS JSON), '$')`, metadata.DefaultColumn))
			args = append(args, jsonPath(k), string(must.NotFail(sjson.MarshalSingleValue(e))))

		case float64, bool, int32, int64:
			arms = append(arms, fmt.Sprintf(`JSON_CONTAINS(JSON_EXTRACT(%s, ?), CAST(? AS JSON), '$')`, metadata.DefaultColumn))
			args = append(args, jsonPath(k), e)

		default:
			return "", nil, false
		}
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

// numericBound returns the numeric argument for a range bound that sjson stores as a
// JSON number — int32/int64/double, a Date (as its Unix-millis) and a BSON Timestamp
// (as its uint64) — or ok=false for a non-number bound (left to the Go filter). A
// Timestamp above signed-64-bit is declined so the pushed value stays an exact
// integer; the Go filter stays authoritative.
func numericBound(v any) (any, bool) {
	switch n := v.(type) {
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return n, true
	case time.Time:
		return n.UnixMilli(), true
	case types.Timestamp:
		if uint64(n) > math.MaxInt64 {
			return nil, false
		}

		return int64(n), true
	default:
		return nil, false
	}
}

// filterEqual returns the proper SQL filter with arguments that filters documents
// where the value under k is equal to v.
func filterEqual(k string, v any) (filter string, args []any) {
	// Select if value under the key is equal to provided value.
	sql := `JSON_CONTAINS(JSON_EXTRACT(%s, ?), CAST(? AS JSON), '$')`

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
			v = types.MaxSafeDouble

		case v < -types.MaxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) < ?`
			v = -types.MaxSafeDouble
		default:
			// don't change the default eq query
		}

		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), v)

	case string, types.ObjectID, time.Time:
		// don't change the default eq query
		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), string(must.NotFail(sjson.MarshalSingleValue(v))))

	case bool, int32:
		// don't change the default eq query
		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), v)

	case int64:
		maxSafeDouble := int64(types.MaxSafeDouble)

		// If value cannot be safe double, fetch all numbers out of the safe range.
		switch {
		case v > maxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) > ?`
			v = maxSafeDouble

		case v < -maxSafeDouble:
			sql = `JSON_EXTRACT(%s, ?) < ?`
			v = -maxSafeDouble
		default:
			// don't change the default eq query
		}

		filter = fmt.Sprintf(sql, metadata.DefaultColumn)
		args = append(args, jsonPath(k), v)

	default:
		panic(fmt.Sprintf("Unexpected type of value: %v", v))
	}

	return
}
