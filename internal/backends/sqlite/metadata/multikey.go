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

package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strings"

	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// Multikey side tables.
//
// A SQLite expression index cannot index the elements of an array: an index
// on {"members.userId": 1} indexes _ferretdb_sjson->"members"->"userId", which
// is NULL for every document whose members is an array - that is, for every
// document. So a filter on an array of documents ({members: {$elemMatch:
// {userId: X}}}, the shape of every membership test) could only narrow by
// other fields, and SQLite parsed the array of almost every row to answer it.
//
// For every index whose leading key is ARRAY.FIELD, the collection now has a
// side table of (element value, document _id): one row per value FIELD has in
// an element of ARRAY - the element's FIELD itself, or each element of it when
// it is an array. Triggers keep it current on every insert, update and delete,
// and it is backfilled when the index is created or, for an index created by an
// older version, once when the database is opened (index format 3).
//
// The value is stored as the JSON text the -> operator returns, the form the
// query pushdown already compares against, so equality is byte-exact.
//
// query.go uses it only to narrow the candidates (_id IN (SELECT id ...)) of a
// filter whose every match must have such an element. The Go filter still
// applies the whole filter, so a candidate too many costs time, never a wrong
// answer - and a candidate cannot be missing, because every value of FIELD in
// every element is in the table.

// plainName is a field name the side table SQL may contain as it is. The names
// are written into trigger bodies, so anything else - a quote, a backslash -
// gets no side table and keeps the ordinary pushdown.
var plainName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// MultikeyPath returns ARRAY and FIELD of an index whose leading key is the
// two-segment dotted path ARRAY.FIELD, the only shape a side table is kept for.
func MultikeyPath(index IndexInfo) (string, string, bool) {
	if len(index.Key) == 0 || index.TextOptions != nil {
		return "", "", false
	}

	key := index.Key[0]
	if key.Text || key.Sphere2D {
		return "", "", false
	}

	array, field, ok := strings.Cut(key.Field, ".")
	if !ok || !plainName.MatchString(array) || !plainName.MatchString(field) {
		return "", "", false
	}

	return array, field, true
}

// MultikeyTableName returns the side table of ARRAY.FIELD for the given
// collection table. The path is hashed, so client field names never become
// SQLite identifiers.
func MultikeyTableName(table, array, field string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(array + "." + field))

	return fmt.Sprintf("%s_mk_%x", table, h.Sum64())
}

// multikeyPaths returns the distinct ARRAY.FIELD paths the indexes need tables for.
func multikeyPaths(indexes []IndexInfo) [][2]string {
	var res [][2]string

	for _, index := range indexes {
		array, field, ok := MultikeyPath(index)
		if !ok {
			continue
		}

		p := [2]string{array, field}
		if !slices.Contains(res, p) {
			res = append(res, p)
		}
	}

	return res
}

// LabelLiteral quotes a JSON object label for the -> operator as a string
// literal. The usual ->"label" relies on SQLite reading an unknown "identifier"
// as a string - but beside json_each, whose columns are key, value, type, atom,
// id, parent, fullkey and path, a field with one of those names is that
// column, and the expression reads the wrong thing (or is ambiguous). A
// literal is never a column, and -> returns the same value for both.
func LabelLiteral(label string) string {
	return `'` + strings.ReplaceAll(label, `'`, `''`) + `'`
}

// multikeyValuesSQL selects the (v, id) rows of one document row, aliased row.
func multikeyValuesSQL(row, array, field string) string {
	arr := fmt.Sprintf(`%s.%s->%s`, row, DefaultColumn, LabelLiteral(array))
	id := fmt.Sprintf(`%s.%s->%s`, row, DefaultColumn, LabelLiteral("_id"))
	// json_each hands a scalar element back as a plain SQL value, which -> cannot
	// parse; its type column says what the element is without parsing it, so
	// FIELD is read only from object elements. json_each(NULL) has no rows.
	val := fmt.Sprintf(`(CASE WHEN e.type = 'object' THEN e.value->%s END)`, LabelLiteral(field))

	return fmt.Sprintf(
		`SELECT %[3]s, %[2]s FROM json_each(%[1]s) AS e `+
			`WHERE json_type(%[1]s) = 'array' AND e.type = 'object' `+
			`AND %[3]s IS NOT NULL AND json_type(%[3]s) != 'array' `+
			`UNION ALL `+
			`SELECT (%[3]s)->x.key, %[2]s FROM json_each(%[1]s) AS e, `+
			`json_each(CASE WHEN json_type(%[3]s) = 'array' THEN %[3]s END) AS x `+
			`WHERE json_type(%[1]s) = 'array' AND e.type = 'object'`,
		arr, id, val,
	)
}

// multikeyCreateSQL returns the statements that create, maintain and fill the
// side table of ARRAY.FIELD. Each is safe to run again.
func multikeyCreateSQL(table, array, field string) []string {
	mk := MultikeyTableName(table, array, field)
	id := func(row string) string { return fmt.Sprintf(`%s.%s->%s`, row, DefaultColumn, LabelLiteral("_id")) }

	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (v TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (v, id)) WITHOUT ROWID`, mk),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %q ON %q (id)`, mk+"_id", mk),
		// One statement per trigger body - the SQL guard accepts a trigger's `;`
		// only then (internal/util/sqlguard). An update is two triggers: SQLite
		// runs BEFORE triggers before AFTER ones, so the old values are gone
		// before the new ones are added, even when _id itself changed.
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %q AFTER INSERT ON %q BEGIN INSERT OR IGNORE INTO %q (v, id) %s; END`,
			mk+"_ai", table, mk, multikeyValuesSQL("NEW", array, field)),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %q BEFORE UPDATE OF %s ON %q BEGIN DELETE FROM %q WHERE id = %s; END`,
			mk+"_bu", DefaultColumn, table, mk, id("OLD")),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %q AFTER UPDATE OF %s ON %q BEGIN INSERT OR IGNORE INTO %q (v, id) %s; END`,
			mk+"_au", DefaultColumn, table, mk, multikeyValuesSQL("NEW", array, field)),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %q AFTER DELETE ON %q BEGIN DELETE FROM %q WHERE id = %s; END`,
			mk+"_ad", table, mk, id("OLD")),
		// The backfill: every existing document. INSERT OR IGNORE, so running it
		// again over a filled table changes nothing.
		fmt.Sprintf(`INSERT OR IGNORE INTO %q (v, id) %s`, mk,
			strings.Replace(multikeyValuesSQL("doc", array, field), `FROM json_each(`, fmt.Sprintf(`FROM %q AS doc, json_each(`, table), 2)),
	}
}

// multikeyDropSQL returns the statements that remove the side table of ARRAY.FIELD.
func multikeyDropSQL(table, array, field string) []string {
	mk := MultikeyTableName(table, array, field)

	return []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %q`, mk+"_ai"),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %q`, mk+"_bu"),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %q`, mk+"_au"),
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %q`, mk+"_ad"),
		fmt.Sprintf(`DROP TABLE IF EXISTS %q`, mk),
	}
}

// execer is what both *fsql.DB and *fsql.Tx offer.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var (
	_ execer = (*fsql.DB)(nil)
	_ execer = (*fsql.Tx)(nil)
)

// ensureMultikey creates and fills the side tables the indexes need.
func ensureMultikey(ctx context.Context, db execer, table string, indexes []IndexInfo) error {
	for _, p := range multikeyPaths(indexes) {
		for _, q := range multikeyCreateSQL(table, p[0], p[1]) {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return lazyerrors.Error(err)
			}
		}
	}

	return nil
}

// dropUnusedMultikey removes the side tables of paths that no remaining index has.
func dropUnusedMultikey(ctx context.Context, db execer, table string, dropped, remaining []IndexInfo) error {
	keep := multikeyPaths(remaining)

	for _, p := range multikeyPaths(dropped) {
		if slices.Contains(keep, p) {
			continue
		}

		for _, q := range multikeyDropSQL(table, p[0], p[1]) {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return lazyerrors.Error(err)
			}
		}
	}

	return nil
}
