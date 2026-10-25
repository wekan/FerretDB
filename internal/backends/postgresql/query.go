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

		// A top-level $and is pushed down branch by branch, as the SQLite
		// backend does: every branch must hold, so ANDing what each branch pushes
		// down is still a superset, and an ordinary client wrapper such as
		// {$and: [{boardId: {$in: [...]}}, {$or: [...]}]} uses the boardId index
		// instead of reading the whole table (90 s for a due cards query among
		// 615,070 cards). A branch with nothing to push down only widens it.
		//
		// A top-level $or is pushed down when EVERY branch pushes something: the
		// OR of supersets is a superset. One branch with nothing could match any
		// row, and then the whole $or stays in Go. The due cards query's
		// {$or: [{members: u}, {assignees: u}, ...]} made the database return every
		// card of the user's boards for Go to throw nearly all of them away.
		if rootKey == "$and" || rootKey == "$or" {
			cond, condArgs, err := logicalWhere(p, rootKey, rootVal)
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

				case "$elemMatch":
					// {members: {$elemMatch: {userId: u, ...}}}: the array contains an
					// element with each field - `@> '[{"userId": u}]'`, or that field an
					// array holding u. Each field on its own, so a superset; the field a
					// literal, so the array's GIN index applies. All Boards and due
					// cards' visible boards, in an $or with the open permissions, read
					// every board without it.
					if keyOperator != "->" {
						continue
					}

					if fields, ok := elemMatchFields(v); ok {
						expr := metadata.DefaultColumn + `->` + quoteLiteral(rootKey)
						for _, f := range fields {
							filters = append(filters, fmt.Sprintf(`(%[1]s @> %[2]s OR %[1]s @> %[3]s)`, expr, p.Next(), p.Next()))
							args = append(args, `[{`+f[0]+`:`+f[1]+`}]`, `[{`+f[0]+`:[`+f[1]+`]}]`)
						}
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

					// The top-level "ts" key is written as a LITERAL: the capped-collection
					// index is on ((_jsonb->>'ts')::numeric), and PostgreSQL can match an
					// index expression only when the key is a constant. With the key as a
					// parameter, a cached generic plan is a sequential scan (verified with
					// EXPLAIN on PostgreSQL 18 and plan_cache_mode = force_generic_plan);
					// only custom plans, which substitute the value, could use the index.
					if rootKey == "ts" && keyOperator == "->" {
						filters = append(filters, fmt.Sprintf(
							`jsonb_typeof(%[1]s->'ts') = 'number' AND (%[1]s->>'ts')::numeric %[2]s %[3]s`,
							metadata.DefaultColumn, sqlOp, p.Next(),
						))
						args = append(args, num)

						continue
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

// logicalWhere is the pushdown of a top-level $and or $or (prepareWhereClause),
// or "" when there is nothing to push down. An $or that cannot be pushed down
// gives its placeholders back, so the numbering stays dense.
func logicalWhere(p *metadata.Placeholder, op string, v any) (string, []any, error) {
	branches, ok := v.(*types.Array)
	if !ok || branches.Len() == 0 {
		return "", nil, nil
	}

	saved := *p

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
			if where, branchArgs, err = prepareWhereClause(p, branch); err != nil {
				return "", nil, lazyerrors.Error(err)
			}
		}

		if where == "" {
			if op == "$or" {
				*p = saved
				return "", nil, nil
			}

			continue
		}

		conds = append(conds, `(`+strings.TrimPrefix(where, ` WHERE `)+`)`)
		args = append(args, branchArgs...)
	}

	if len(conds) == 0 {
		return "", nil, nil
	}

	joiner := " AND "
	if op == "$or" {
		joiner = " OR "
	}

	return `(` + strings.Join(conds, joiner) + `)`, args, nil
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
// filterInIndexed is filterIn for a top-level field and scalar values, written
// so that PostgreSQL can answer it from the field's index - the same rewrite as
// filterEqualIndexed: the field a literal, `IN (...)` for the values, and a
// non-empty array that contains one of them for MongoDB's array matching. It
// also needs one parameter per value instead of two, which matters for a long
// list (PostgreSQL takes at most 65,535).
func filterInIndexed(p *metadata.Placeholder, field string, arr *types.Array) (string, []any, bool) {
	expr := metadata.DefaultColumn + `->` + quoteLiteral(field)

	var placeholders []string
	var args []any
	hasNull := false

	for i := 0; i < arr.Len(); i++ {
		switch e := must.NotFail(arr.Get(i)).(type) {
		case types.NullType:
			hasNull = true
		case string, types.ObjectID, time.Time:
			placeholders = append(placeholders, p.Next())
			args = append(args, string(must.NotFail(sjson.MarshalSingleValue(e))))
		case bool, int32:
			placeholders = append(placeholders, p.Next())
			args = append(args, e)
		case float64:
			if e > types.MaxSafeDouble || e < -types.MaxSafeDouble {
				return "", nil, false
			}

			placeholders = append(placeholders, p.Next())
			args = append(args, e)
		case int64:
			if e > int64(types.MaxSafeDouble) || e < -int64(types.MaxSafeDouble) {
				return "", nil, false
			}

			placeholders = append(placeholders, p.Next())
			args = append(args, e)
		default:
			return "", nil, false
		}
	}

	var arms []string

	if len(placeholders) > 0 {
		arms = append(arms, fmt.Sprintf(`%s IN (%s)`, expr, strings.Join(placeholders, ", ")))

		if field != "_id" {
			contains := make([]string, len(placeholders))
			for i, ph := range placeholders {
				contains[i] = fmt.Sprintf(`%s @> %s`, expr, ph)
			}

			arms = append(arms, fmt.Sprintf(`(%[1]s >= '[null]'::jsonb AND %[1]s < '{}'::jsonb AND (%[2]s))`,
				expr, strings.Join(contains, " OR ")))
		}
	}

	if hasNull {
		arms = append(arms, fmt.Sprintf(`(%[1]s IS NULL OR %[1]s = 'null'::jsonb)`, expr))
	}

	if len(arms) == 0 {
		return "", nil, false
	}

	return "(" + strings.Join(arms, " OR ") + ")", args, true
}

func filterIn(p *metadata.Placeholder, key any, keyOperator string, arr *types.Array) (string, []any, bool) {
	if field, ok := key.(string); ok && keyOperator == "->" {
		return filterInIndexed(p, field, arr)
	}

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
// filterEqualIndexed is filterEqual for a top-level field and a scalar value,
// written so that PostgreSQL can answer it from the field's index.
//
// filterEqual's `_jsonb->$1 @> $2` passes the field NAME as a parameter, and an
// expression index on `_jsonb->'boardId'` cannot match an expression whose key
// is only known at run time - nor can a btree index serve @>. So every pushed
// down equality, an _id lookup included, read the whole table.
//
// Here the field is a literal, as in the index, and MongoDB's "equal, or an
// array that contains it" is `= $n`, or a non-empty array that @> $n: two index
// ranges PostgreSQL can combine. In jsonb's order non-empty arrays sort from
// '[null]' (the shortest, with the smallest element) to before '{}' - and not
// from '[]', which for historical reasons sorts below null and would make the
// range everything but objects. _id is never an array, so it is a plain
// equality.
func filterEqualIndexed(p *metadata.Placeholder, field string, v any) (string, []any, bool) {
	var arg any

	switch v := v.(type) {
	case string, types.ObjectID, time.Time:
		arg = string(must.NotFail(sjson.MarshalSingleValue(v)))
	case bool, int32:
		arg = v
	case float64:
		if v > types.MaxSafeDouble || v < -types.MaxSafeDouble {
			return "", nil, false
		}

		arg = v
	case int64:
		if v > int64(types.MaxSafeDouble) || v < -int64(types.MaxSafeDouble) {
			return "", nil, false
		}

		arg = v
	default:
		return "", nil, false
	}

	expr := metadata.DefaultColumn + `->` + quoteLiteral(field)
	ph := p.Next()

	if field == "_id" {
		return fmt.Sprintf(`%s = %s`, expr, ph), []any{arg}, true
	}

	return fmt.Sprintf(`(%[1]s = %[2]s OR (%[1]s >= '[null]'::jsonb AND %[1]s < '{}'::jsonb AND %[1]s @> %[2]s))`, expr, ph),
		[]any{arg}, true
}

// quoteLiteral quotes a field name as a string literal, the same way the
// registry quotes it in an index expression - so the two expressions match.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func filterEqual(p *metadata.Placeholder, k any, v any, operator string) (filter string, args []any) {
	if field, ok := k.(string); ok && operator == "->" {
		if f, a, ok := filterEqualIndexed(p, field, v); ok {
			return f, a
		}
	}

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
