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
	"log/slog"
	"slices"
	"strings"
	"time"

	"golang.org/x/exp/maps"

	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// Documents stored as SQLite JSONB.
//
// Every collection table used to keep its documents as sjson TEXT, so every
// ->, ->> and json_each of a query, an index or a trigger parsed the text of a
// document again. SQLite's JSONB is its parsed form, kept: filters, sort keys
// and index builds read it without parsing (the backend's jsonb.go has the
// numbers). A new collection is created with a BLOB column and written with
// jsonb(?); reading turns JSONB back into the exact sjson text, in Go.
//
// A table created by an older version is converted once, when the database is
// opened: a new table with a BLOB column receives every row - with its rowid,
// which the natural order and record ids are - as jsonb(text), the old table is
// dropped, the new one takes its name, and its indexes and the multikey
// triggers are created again from their own definitions. All of it in one
// transaction, so a failure leaves the table exactly as it was; the collection
// then keeps working as text, which the backend still reads and writes, and is
// tried again at the next start.
//
// After the conversion an older FerretDB cannot read the database: its column
// holds JSONB, which it would hand to its decoder as text.

// StorageJSONB is Settings.Storage of a collection stored as SQLite JSONB.
const StorageJSONB = "jsonb"

// tableStorage returns the storage the table's document column has: StorageJSONB
// for a BLOB column, "" for TEXT.
func tableStorage(ctx context.Context, db queryer, table string) (string, error) {
	var typ string

	err := db.QueryRowContext(ctx, `SELECT type FROM pragma_table_info(?) WHERE name = ?`, table, DefaultColumn).Scan(&typ)
	if err != nil {
		return "", lazyerrors.Error(err)
	}

	if strings.EqualFold(typ, "BLOB") {
		return StorageJSONB, nil
	}

	return "", nil
}

// queryer is what both *fsql.DB and *fsql.Tx offer for reading.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ queryer = (*fsql.DB)(nil)
	_ queryer = (*fsql.Tx)(nil)
)

// upgradeStorage converts every collection of the opened databases that is not
// stored as JSONB yet, and records each one's storage as its column has it.
func (r *Registry) upgradeStorage(ctx context.Context, dbs map[string]*fsql.DB) error {
	dbNames := maps.Keys(r.colls)
	slices.Sort(dbNames)

	for _, dbName := range dbNames {
		db := dbs[dbName]
		if db == nil {
			continue
		}

		collNames := maps.Keys(r.colls[dbName])
		slices.Sort(collNames)

		for _, collName := range collNames {
			c := r.colls[dbName][collName]

			storage, err := tableStorage(ctx, db, c.TableName)
			if err != nil {
				return lazyerrors.Error(err)
			}

			if storage == "" {
				if err = r.convertToJSONB(ctx, db, c); err != nil {
					// Text is still read and written: the collection works, slower,
					// and the conversion is tried again at the next start.
					r.l.WarnContext(ctx, "SQLite collection not converted to JSONB; it stays text",
						slog.String("collection", c.Name), slog.String("error", err.Error()))

					continue
				}

				storage = StorageJSONB
			}

			if c.Settings.Storage == storage {
				continue
			}

			c.Settings.Storage = storage

			q := fmt.Sprintf("UPDATE %q SET settings = ? WHERE table_name = ?", metadataTableName)
			if _, err = db.ExecContext(ctx, q, c.Settings, c.TableName); err != nil {
				return lazyerrors.Error(err)
			}
		}
	}

	return nil
}

// convertToJSONB rebuilds the collection's table with a JSONB column, in one
// transaction.
func (r *Registry) convertToJSONB(ctx context.Context, db *fsql.DB, c *Collection) error {
	started := time.Now()
	tmp := c.TableName + "_jsonb_conversion"

	var rows int64

	err := db.InTransaction(ctx, func(tx *fsql.Tx) error {
		// The table's indexes and triggers, to create again on the new table.
		// Automatic indexes (sql IS NULL) belong to constraints and come back
		// with the table.
		var schema []string

		res, err := tx.QueryContext(ctx,
			`SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL `+
				`ORDER BY CASE type WHEN 'index' THEN 0 ELSE 1 END, name`,
			c.TableName)
		if err != nil {
			return lazyerrors.Error(err)
		}

		for res.Next() {
			var s string
			if err = res.Scan(&s); err != nil {
				_ = res.Close()
				return lazyerrors.Error(err)
			}

			schema = append(schema, s)
		}

		if err = res.Close(); err != nil {
			return lazyerrors.Error(err)
		}

		if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %q`, tmp)); err != nil {
			return lazyerrors.Error(err)
		}

		create := fmt.Sprintf(`CREATE TABLE %q (`, tmp)
		copyColumns := "rowid, " + DefaultColumn
		copyValues := "rowid, jsonb(" + DefaultColumn + ")"

		if c.Capped() {
			create += RecordIDColumn + " INTEGER PRIMARY KEY, "
			copyColumns = RecordIDColumn + ", " + DefaultColumn
			copyValues = RecordIDColumn + ", jsonb(" + DefaultColumn + ")"
		}

		create += DefaultColumn + " BLOB NOT NULL) STRICT"

		if _, err = tx.ExecContext(ctx, create); err != nil {
			return lazyerrors.Error(err)
		}

		result, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %q (%s) SELECT %s FROM %q`,
			tmp, copyColumns, copyValues, c.TableName))
		if err != nil {
			return lazyerrors.Error(err)
		}

		if rows, err = result.RowsAffected(); err != nil {
			return lazyerrors.Error(err)
		}

		for _, q := range []string{
			fmt.Sprintf(`DROP TABLE %q`, c.TableName),
			fmt.Sprintf(`ALTER TABLE %q RENAME TO %q`, tmp, c.TableName),
		} {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return lazyerrors.Error(err)
			}
		}

		for _, q := range schema {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return lazyerrors.Error(err)
			}
		}

		return nil
	})
	if err != nil {
		return lazyerrors.Error(err)
	}

	r.l.InfoContext(ctx, "Converted SQLite collection to JSONB",
		slog.String("collection", c.Name), slog.Int64("documents", rows),
		slog.Duration("took", time.Since(started).Round(time.Millisecond)))

	return nil
}
