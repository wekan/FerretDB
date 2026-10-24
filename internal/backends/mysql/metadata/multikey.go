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
	"fmt"
	"hash/fnv"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// Multikey side tables, the same idea as the SQLite backend's.
//
// An index on {"members.userId": 1} is a generated column of
// JSON_EXTRACT(doc, '$."members"."userId"'), which is NULL for every document
// whose members is an array - that is, for every document - so a membership
// test ({members: {$elemMatch: {userId: X}}}) read the whole table. MySQL has
// multi-valued indexes for this, MariaDB has none, so both get what SQLite has:
// for every index whose leading key is ARRAY.FIELD, a side table of (value,
// _id) with one row per string FIELD holds in an element of ARRAY - the
// element's FIELD itself, or each element of it when it is an array - kept
// current by triggers and filled when it is created.
//
// Both columns hold JSON text as MySQL renders it (CAST(... AS CHAR)): `id` is
// what the _id generated column holds, so `_id IN (SELECT id ...)` is an index
// lookup, and `v` is compared only with values whose text is certain
// (query.go, certainText). Only strings are kept - the only values a query
// asks the table for - and only those that fit the column; a longer one is
// never asked for either.
//
// query_multikey.go uses it only to NARROW the candidates of a filter whose
// every match has such an element. The Go filter still applies the whole
// filter, and no candidate can be missing: every string of FIELD in every
// element is in the table.

// plainName is a field name the side table SQL may contain as it is: the names
// are written into JSON paths of trigger bodies.
var plainName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// multikeyValueChars is the longest JSON text the side table keeps.
const multikeyValueChars = 255

// MultikeyPath returns ARRAY and FIELD of an index whose leading key is the
// two-segment dotted path ARRAY.FIELD, the only shape a side table is kept for.
func MultikeyPath(index IndexInfo) (string, string, bool) {
	if len(index.Key) == 0 {
		return "", "", false
	}

	array, field, ok := strings.Cut(index.Key[0].Field, ".")
	if !ok || !plainName.MatchString(array) || !plainName.MatchString(field) {
		return "", "", false
	}

	return array, field, true
}

// MultikeyArrays returns, for every ARRAY with a side table, its FIELDs.
func MultikeyArrays(indexes Indexes) map[string][]string {
	res := map[string][]string{}

	for _, p := range multikeyPaths(indexes) {
		if !slices.Contains(res[p[0]], p[1]) {
			res[p[0]] = append(res[p[0]], p[1])
		}
	}

	return res
}

// MultikeyTableName returns the side table of ARRAY.FIELD for the given
// collection table. It is hashed: a table name is already up to 64 characters,
// MySQL's limit, and client field names never become identifiers.
func MultikeyTableName(table, array, field string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(table + "/" + array + "." + field))

	return fmt.Sprintf("_ferretdb_mk_%016x", h.Sum64())
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

// asText is the JSON text of a JSON expression, as the generated columns hold it.
func asText(expr string) string {
	return `CAST(` + expr + ` AS CHAR CHARACTER SET utf8mb4)`
}

// multikeyValuesSQL selects the (v, id) rows of one document row, aliased row.
//
// JSON_TABLE only numbers the elements (FOR ORDINALITY), and JSON_EXTRACT reads
// each value: a JSON_TABLE value column is typed differently by MySQL (a JSON
// value) and MariaDB (text without its quotes), JSON_EXTRACT's text is the
// same in both. The NESTED PATH numbers the elements of FIELD when it is an
// array; when it is not, j is NULL and FIELD itself is read.
func multikeyValuesSQL(row, array, field string) string {
	doc := row + "." + DefaultColumn
	id := asText(fmt.Sprintf(`JSON_EXTRACT(%s, '$."_id"')`, doc))
	value := asText(fmt.Sprintf(
		`JSON_EXTRACT(%[1]s, CONCAT('$."%[2]s"[', mkjt.i - 1, ']."%[3]s"', IF(mkjt.j IS NULL, '', CONCAT('[', mkjt.j - 1, ']'))))`,
		doc, array, field,
	))

	return fmt.Sprintf(
		`SELECT mkv.v, mkv.id FROM (SELECT %[1]s AS v, %[2]s AS id FROM JSON_TABLE(%[3]s, '$."%[4]s"[*]' `+
			`COLUMNS (i FOR ORDINALITY, NESTED PATH '$."%[5]s"[*]' COLUMNS (j FOR ORDINALITY))) AS mkjt) AS mkv `+
			`WHERE mkv.v LIKE '"%%' AND CHAR_LENGTH(mkv.v) <= %[6]d`,
		value, id, doc, array, field, multikeyValueChars,
	)
}

// multikeyCreateSQL returns the statements that create, maintain and fill the
// side table of ARRAY.FIELD. The triggers come before the backfill, so a write
// made while it runs is not missed; INSERT IGNORE makes both safe to repeat.
func multikeyCreateSQL(dbName, table, array, field string) []string {
	mk := MultikeyTableName(table, array, field)
	qdb, qt, qmk := QuoteIdent(dbName), QuoteIdent(table), QuoteIdent(mk)
	trigger := func(suffix string) string { return qdb + "." + QuoteIdent(mk+suffix) }
	oldID := asText(fmt.Sprintf(`JSON_EXTRACT(OLD.%s, '$."_id"')`, DefaultColumn))
	column := fmt.Sprintf(`VARCHAR(%d) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL`, multikeyValueChars)

	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (v %s, id %s, PRIMARY KEY (v, id), KEY (id))`, qdb, qmk, column, column),
		// One statement per trigger body, so no `;` inside one. An update is two
		// triggers, BEFORE removing the old values and AFTER adding the new,
		// which is right even when _id itself changed.
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s AFTER INSERT ON %s.%s FOR EACH ROW INSERT IGNORE INTO %s.%s (v, id) %s`,
			trigger("_ai"), qdb, qt, qdb, qmk, multikeyValuesSQL("NEW", array, field)),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s BEFORE UPDATE ON %s.%s FOR EACH ROW DELETE FROM %s.%s WHERE id = %s`,
			trigger("_bu"), qdb, qt, qdb, qmk, oldID),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s AFTER UPDATE ON %s.%s FOR EACH ROW INSERT IGNORE INTO %s.%s (v, id) %s`,
			trigger("_au"), qdb, qt, qdb, qmk, multikeyValuesSQL("NEW", array, field)),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s AFTER DELETE ON %s.%s FOR EACH ROW DELETE FROM %s.%s WHERE id = %s`,
			trigger("_ad"), qdb, qt, qdb, qmk, oldID),
		fmt.Sprintf(`INSERT IGNORE INTO %s.%s (v, id) %s`, qdb, qmk,
			strings.Replace(multikeyValuesSQL("doc", array, field), `FROM JSON_TABLE(`, fmt.Sprintf(`FROM %s.%s AS doc, JSON_TABLE(`, qdb, qt), 1)),
	}
}

// multikeyDropSQL returns the statements that remove the side table of ARRAY.FIELD.
func multikeyDropSQL(dbName, table, array, field string) []string {
	mk := MultikeyTableName(table, array, field)
	qdb := QuoteIdent(dbName)

	res := make([]string, 0, 5)
	for _, suffix := range []string{"_ai", "_bu", "_au", "_ad"} {
		res = append(res, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s.%s`, qdb, QuoteIdent(mk+suffix)))
	}

	return append(res, fmt.Sprintf(`DROP TABLE IF EXISTS %s.%s`, qdb, QuoteIdent(mk)))
}

// MultikeyReady reports whether the side table of that name is complete - created,
// filled and kept current by all four triggers - so a query may narrow by it.
func (r *Registry) MultikeyReady(name string) bool {
	r.rw.RLock()
	defer r.rw.RUnlock()

	_, ok := r.multikey[name]

	return ok
}

// multikeyComplete reports whether the side table and all four of its triggers exist.
func multikeyComplete(ctx context.Context, p *fsql.DB, dbName, mk string) (bool, error) {
	q := `SELECT ` +
		`(SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?), ` +
		`(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = ? AND trigger_name IN (?, ?, ?, ?))`

	var tables, triggers int
	if err := p.QueryRowContext(ctx, q, dbName, mk, dbName, mk+"_ai", mk+"_bu", mk+"_au", mk+"_ad").Scan(&tables, &triggers); err != nil {
		return false, lazyerrors.Error(err)
	}

	return tables == 1 && triggers == 4, nil
}

// ensureMultikey creates and fills the side tables the indexes need and marks
// them ready. One that is already complete is only marked: its triggers keep it
// current, and filling it again would read the whole collection at every start.
//
// A side table is an optimization, so failing to make one fails nothing: the
// statements are undone, the table is not marked, and queries read the
// collection as before. That matters because MySQL may refuse CREATE TRIGGER to
// an account without SUPER while binary logging is on (error 1419), and a side
// table without its triggers would go stale - and then miss matches.
//
// It must be called with the write lock held.
func (r *Registry) ensureMultikey(ctx context.Context, p *fsql.DB, dbName, table string, indexes []IndexInfo) {
	if r.multikey == nil {
		r.multikey = map[string]struct{}{}
	}

	for _, path := range multikeyPaths(indexes) {
		mk := MultikeyTableName(table, path[0], path[1])

		ok, err := multikeyComplete(ctx, p, dbName, mk)
		if err == nil && !ok {
			for _, q := range multikeyDropSQL(dbName, table, path[0], path[1]) {
				if _, err = p.ExecContext(ctx, q); err != nil {
					break
				}
			}

			for _, q := range multikeyCreateSQL(dbName, table, path[0], path[1]) {
				if err != nil {
					break
				}

				_, err = p.ExecContext(ctx, q)
			}
		}

		if err != nil {
			r.l.WarnContext(ctx, "Multikey side table not created; the index is used without it",
				slog.String("table", table), slog.String("path", path[0]+"."+path[1]), slog.String("error", err.Error()))

			for _, q := range multikeyDropSQL(dbName, table, path[0], path[1]) {
				_, _ = p.ExecContext(ctx, q)
			}

			delete(r.multikey, mk)

			continue
		}

		r.multikey[mk] = struct{}{}
	}
}

// dropMultikey removes the side tables of paths in dropped that no index in
// remaining has. It must be called with the write lock held.
func (r *Registry) dropMultikey(ctx context.Context, p *fsql.DB, dbName, table string, dropped, remaining []IndexInfo) error {
	keep := multikeyPaths(remaining)

	for _, path := range multikeyPaths(dropped) {
		if slices.Contains(keep, path) {
			continue
		}

		delete(r.multikey, MultikeyTableName(table, path[0], path[1]))

		for _, q := range multikeyDropSQL(dbName, table, path[0], path[1]) {
			if _, err := p.ExecContext(ctx, q); err != nil {
				return lazyerrors.Error(err)
			}
		}
	}

	return nil
}
