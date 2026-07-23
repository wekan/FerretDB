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

package hana

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func prepareSelectClause(schema, table string) string {
	return fmt.Sprintf("SELECT * FROM %q.%q", schema, table)
}

func jsonToHanaQueryString(jsonStr string) string {
	hanaString := string(strToHanaJSON([]byte(jsonStr)))
	return strings.ReplaceAll(hanaString, "\"", "'")
}

// numericBound returns the numeric argument for a range bound that sjson stores as a
// JSON number — int32/int64/double, a Date (as its Unix-millis) and a BSON Timestamp
// (as its uint64) — or ok=false for a non-number bound (left to the Go filter). A
// Timestamp above signed-64-bit is declined so the pushed value stays an exact integer.
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

func makeFilter(table, key, op string, value any) string {
	var valStr string
	hanaKey := jsonToHanaQueryString(key)

	switch v := value.(type) {
	case *types.Document, *types.Array, types.Binary,
		types.NullType, types.Regex, types.Timestamp:
	// type not supported for pushdown
	case float64:
		// If value is not safe double, fetch all numbers out of safe range.
		// TODO https://github.com/FerretDB/FerretDB/issues/3626
		switch {
		case v > types.MaxSafeDouble:
			value = types.MaxSafeDouble

		case v < -types.MaxSafeDouble:
			value = -types.MaxSafeDouble
		default:
			// don't change the default value
		}
		valStr = fmt.Sprintf("%f", value)
	case bool:
		valStr = fmt.Sprintf("TO_JSON_BOOLEAN(%t)", value)
	case int32, int64:
		valStr = fmt.Sprintf("%d", value)
	case nil:
		valStr = "NULL"
	case string, types.ObjectID, time.Time:
		marshaledValue := string(must.NotFail(sjson.MarshalSingleValue(v)))
		valStr = jsonToHanaQueryString(marshaledValue)
	default:
		panic(fmt.Sprintf("Unexpected type of value: %v", v))
	}

	res := fmt.Sprintf("%q %s %s", hanaKey, op, valStr)

	// If table name matches key we need to prefix with "table"."key"
	if key == table {
		res = fmt.Sprintf("%q.%s", table, res)
	}

	return res
}

func prepareWhereClause(table string, filter *types.Document) (string, error) {
	var filters []string

	iter := filter.Iterator()
	defer iter.Close()

	// iterate through root document
	for {
		rootKey, rootVal, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			return "", lazyerrors.Error(err)
		}

		// don't pushdown $comment, as it's attached to query with select clause
		//
		// all of the other top-level operators such as `$or` do not support pushdown yet
		if strings.HasPrefix(rootKey, "$") {
			continue
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

					return "", lazyerrors.Error(err)
				}

				switch k {
				case "$eq":
					if f := makeFilter(table, rootKey, "=", v); f != "" {
						filters = append(filters, f)
					}

				case "$ne":
					if f := makeFilter(table, rootKey, "<>", v); f != "" {
						filters = append(filters, f)
					}
				case "$gt", "$gte", "$lt", "$lte":
					// Best-effort numeric/date/Timestamp range pushdown (parity with the
					// other backends). sjson stores int/double/Date(UnixMilli)/
					// Timestamp(uint64) as JSON numbers; convert the bound to that
					// numeric and let makeFilter render the comparison against the
					// DocStore field. Non-number bounds stay in the Go filter, which
					// re-applies the exact, type-bracketed comparison regardless.
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

					if num, ok := numericBound(v); ok {
						if f := makeFilter(table, rootKey, sqlOp, num); f != "" {
							filters = append(filters, f)
						}
					}
				default:
					// other operators ($regex, $exists, …) stay in the Go filter.
					continue
				}
			}

		case *types.Array, types.Binary, types.NullType, types.Regex, types.Timestamp:
			// type not supported for pushdown

		case float64, string, types.ObjectID, bool, time.Time, int32, int64:
			if f := makeFilter(table, rootKey, "=", v); f != "" {
				filters = append(filters, f)
			}

		default:
			panic(fmt.Sprintf("Unexpected type of value: %v", v))
		}
	}

	whereClause := ""
	if len(filters) > 0 {
		whereClause = " WHERE " + strings.Join(filters, " AND ")
	}

	return whereClause, nil
}

func prepareOrderByClause(sort *types.Document) (string, error) {
	if sort.Len() != 1 {
		return "", nil
	}

	v := must.NotFail(sort.Get("$natural"))
	var order string

	switch v.(int64) {
	case 1:
		order = "ASC"
	case -1:
		order = "DESC"
	default:
		panic("not reachable")
	}
	orderByClause := fmt.Sprintf(" ORDER BY \"_id\" %s", order)

	return orderByClause, nil
}
