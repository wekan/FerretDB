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
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FerretDB/FerretDB/internal/backends/postgresql/metadata"
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
// For capped collection with onlyRecordIDs, it returns select clause for recordID column.
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
			`SELECT %s %s FROM %s`,
			params.Comment,
			metadata.RecordIDColumn,
			pgx.Identifier{params.Schema, params.Table}.Sanitize(),
		)
	}

	if params.Capped {
		return fmt.Sprintf(
			`SELECT %s %s, %s FROM %s`,
			params.Comment,
			metadata.RecordIDColumn,
			metadata.DefaultColumn,
			pgx.Identifier{params.Schema, params.Table}.Sanitize(),
		)
	}

	return fmt.Sprintf(
		`SELECT %s %s FROM %s`,
		params.Comment,
		metadata.DefaultColumn,
		pgx.Identifier{params.Schema, params.Table}.Sanitize(),
	)
}

// prepareWhereClause adds WHERE clause with given filters to the query and returns the query and arguments.
func prepareWhereClause(p *metadata.Placeholder, sqlFilters *types.Document) (string, []any, error) {
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

		keyOperator := "->" // keyOperator is the operator that is used to access the field. (->/#>)

		// key can be either a string '"v"' or PostgreSQL path '{v,foo}'.
		// We use path type only for dot notation due to simplicity of SQL queries, and the fact
		// that path doesn't handle empty keys.
		var key any = rootKey

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
			if path.Len() > 1 {
				keyOperator = "#>"
				key = path.Slice() // '{v,foo}'
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
					if f, a := filterEqual(p, key, v, keyOperator); f != "" {
						filters = append(filters, f)
						args = append(args, a...)
					}

				case "$in":
					// Push down {field: {$in: [...]}} as an OR of containment arms (one
					// per pushable element) plus, for a `null` element, a
					// null-or-missing arm — a SUPERSET; the Go filter re-applies the
					// exact $in. Parity with the sqlite backend. If any element has no
					// safe arm (a nested doc/array/binary/regex/Timestamp) the whole
					// $in stays in the Go filter (pushing a subset would be wrong).
					if arr, ok := v.(*types.Array); ok {
						if f, a, ok := filterIn(p, key, keyOperator, arr); ok {
							filters = append(filters, f)
							args = append(args, a...)
						}
					}

				case "$ne":
					sql := `NOT ( ` +
						// does document contain the key,
						// it is necessary, as NOT won't work correctly if the key does not exist.
						`%[1]s ? %[2]s AND ` +
						// does the value under the key is equal to filter value
						`%[1]s->%[2]s @> %[3]s AND ` +
						// does the value type is equal to the filter's one
						`%[1]s->'$s'->'p'->%[2]s->'t' = '"%[4]s"' )`

					switch v := v.(type) {
					case *types.Document, *types.Array, types.Binary,
						types.NullType, types.Regex, types.Timestamp:
						// type not supported for pushdown

					case float64, bool, int32, int64:
						filters = append(filters, fmt.Sprintf(
							sql,
							metadata.DefaultColumn,
							p.Next(),
							p.Next(),
							sjson.GetTypeOfValue(v),
						))

						// merge with the case below?
						// TODO https://github.com/FerretDB/FerretDB/issues/3626
						args = append(args, rootKey, v)

					case string, types.ObjectID, time.Time:
						filters = append(filters, fmt.Sprintf(
							sql,
							metadata.DefaultColumn,
							p.Next(),
							p.Next(),
							sjson.GetTypeOfValue(v),
						))

						// merge with the case above?
						// TODO https://github.com/FerretDB/FerretDB/issues/3626
						args = append(args, rootKey, string(must.NotFail(sjson.MarshalSingleValue(v))))

					default:
						panic(fmt.Sprintf("Unexpected type of value: %v", v))
					}

				case "$gt", "$gte", "$lt", "$lte":
					// Push down a numeric / date / BSON-Timestamp range bound. sjson
					// stores int/double/Date(UnixMilli)/Timestamp(uint64) all as JSON
					// numbers, so compare the ->>-extracted text cast to numeric. GUARD
					// with jsonb_typeof(...) = 'number' first: PostgreSQL's ::numeric
					// cast THROWS on a non-numeric value (unlike SQLite), so without the
					// guard a range over a mixed-type field would error the whole query;
					// the guard also keeps this a SUPERSET (only number-typed docs are
					// pre-filtered — the in-Go filter re-applies exact, type-bracketed
					// comparison). Mainly this makes an idle OpLog tail's {ts:{$gt}}
					// resume as an indexed range scan instead of re-decoding the whole
					// capped collection every awaitData poll.
					num, ok := numericBoundPG(v)
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

					// keyOperator is "->" or "#>"; the text-extraction form is "->>"/"#>>".
					filters = append(filters, fmt.Sprintf(
						`jsonb_typeof(%[1]s%[2]s%[3]s) = 'number' AND (%[1]s%[4]s%[5]s)::numeric %[6]s %[7]s`,
						metadata.DefaultColumn, keyOperator, p.Next(), keyOperator+">", p.Next(), sqlOp, p.Next(),
					))
					args = append(args, key, key, num)

				default:
					// other operators ($regex, $exists, …) stay in the Go filter.
					continue
				}
			}

		case *types.Array, types.Binary, types.NullType, types.Regex, types.Timestamp:
			// type not supported for pushdown

		case float64, string, types.ObjectID, bool, time.Time, int32, int64:
			if f, a := filterEqual(p, key, v, keyOperator); f != "" {
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

// prepareOrderByClause returns ORDER BY clause with arguments for given sort document.
//
// The provided sort document should be already validated.
// Provided document should only contain a single value.
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

// filterIn pushes down {field: {$in: [...]}} as an OR-union of JSONB containment arms
// (index-friendly, same shape as equality) — one per pushable element — plus, for a
// `null` element, a `(x IS NULL OR x = 'null'::jsonb)` arm that matches a missing OR
// JSON-null field (what a `null` $in element matches). It returns ok=false when any
// element has no safe arm (a nested doc/array/binary/regex/Timestamp), since dropping
// one would make IN a SUBSET, not a superset. The Go filter re-applies the exact $in.
func filterIn(p *metadata.Placeholder, key any, keyOperator string, arr *types.Array) (string, []any, bool) {
	var arms []string
	var args []any
	hasNull := false

	for i := 0; i < arr.Len(); i++ {
		e := must.NotFail(arr.Get(i))

		switch ev := e.(type) {
		case types.NullType:
			hasNull = true

		case string, types.ObjectID, float64, bool, int32, int64, time.Time:
			// One containment arm, exactly like filterEqual's default (the @> form).
			arms = append(arms, fmt.Sprintf(
				`%[1]s%[2]s%[3]s @> %[4]s`, metadata.DefaultColumn, keyOperator, p.Next(), p.Next()))

			switch ev.(type) {
			case string, types.ObjectID, time.Time:
				args = append(args, key, string(must.NotFail(sjson.MarshalSingleValue(e))))
			default:
				args = append(args, key, e)
			}

		default:
			// nested doc/array/binary/regex/Timestamp: no safe arm -> leave to Go.
			return "", nil, false
		}
	}

	if hasNull {
		arms = append(arms, fmt.Sprintf(
			`(%[1]s%[2]s%[3]s IS NULL OR %[1]s%[2]s%[4]s = 'null'::jsonb)`,
			metadata.DefaultColumn, keyOperator, p.Next(), p.Next()))
		args = append(args, key, key)
	}

	if len(arms) == 0 {
		return "", nil, false
	}

	return "(" + strings.Join(arms, " OR ") + ")", args, true
}

// numericBoundPG returns the numeric argument for a range bound that sjson stores as
// a JSON number — int32/int64/double, a Date (as its Unix-millis) and a BSON
// Timestamp (as its uint64) — so a `(_jsonb->>'field')::numeric` comparison can push
// it down, or ok=false for a non-number bound (left to the Go filter). A Timestamp
// that would not fit a signed 64-bit integer is declined so the pushed value stays an
// exact integer (never a wrong/subset comparison); the Go filter stays authoritative.
func numericBoundPG(v any) (any, bool) {
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
func filterEqual(p *metadata.Placeholder, k any, v any, operator string) (filter string, args []any) {
	// Select if value under the key is equal to provided value.
	sql := `%[1]s%[2]s%[3]s @> %[4]s`

	switch v := v.(type) {
	case *types.Document, *types.Array, types.Binary,
		types.NullType, types.Regex, types.Timestamp:
		// type not supported for pushdown

	case float64:
		// If value is not safe double, fetch all numbers out of safe range.
		// TODO https://github.com/FerretDB/FerretDB/issues/3626
		switch {
		case v > types.MaxSafeDouble:
			sql = `%[1]s%[2]s%[3]s > %[4]s`
			v = types.MaxSafeDouble

		case v < -types.MaxSafeDouble:
			sql = `%[1]s%[2]s%[3]s < %[4]s`
			v = -types.MaxSafeDouble
		default:
			// don't change the default eq query
		}

		filter = fmt.Sprintf(sql, metadata.DefaultColumn, operator, p.Next(), p.Next())
		args = append(args, k, v)

	case string, types.ObjectID, time.Time:
		// merge with the case below?
		// TODO https://github.com/FerretDB/FerretDB/issues/3626

		// don't change the default eq query
		filter = fmt.Sprintf(sql, metadata.DefaultColumn, operator, p.Next(), p.Next())
		args = append(args, k, string(must.NotFail(sjson.MarshalSingleValue(v))))

	case bool, int32:
		// merge with the case above?
		// TODO https://github.com/FerretDB/FerretDB/issues/3626

		// don't change the default eq query
		filter = fmt.Sprintf(sql, metadata.DefaultColumn, operator, p.Next(), p.Next())
		args = append(args, k, v)

	case int64:
		// TODO https://github.com/FerretDB/FerretDB/issues/3626
		maxSafeDouble := int64(types.MaxSafeDouble)

		// If value cannot be safe double, fetch all numbers out of the safe range.
		switch {
		case v > maxSafeDouble:
			sql = `%[1]s%[2]s%[3]s > %[4]s`
			v = maxSafeDouble

		case v < -maxSafeDouble:
			sql = `%[1]s%[2]s%[3]s < %[4]s`
			v = -maxSafeDouble
		default:
			// don't change the default eq query
		}

		filter = fmt.Sprintf(sql, metadata.DefaultColumn, operator, p.Next(), p.Next())
		args = append(args, k, v)

	default:
		panic(fmt.Sprintf("Unexpected type of value: %v", v))
	}

	return
}
