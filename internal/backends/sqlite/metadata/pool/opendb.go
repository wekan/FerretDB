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

package pool

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"runtime"

	_ "modernc.org/sqlite" // register database/sql driver

	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/logging"
	"github.com/FerretDB/FerretDB/internal/util/state"
)

// openDB opens existing database or creates a new one.
//
// All valid FerretDB database names are valid SQLite database names / file names,
// so no validation is needed.
// One exception is very long full path names for the filesystem,
// but we don't check it.
func openDB(name, uri string, memory bool, l *slog.Logger, sp *state.Provider) (*fsql.DB, error) {
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	configurePool(db, memory)

	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, lazyerrors.Error(err)
	}

	checkAndRepairSQLite(db, name, memory, l)

	// do it only once because version can't change
	if sp.Get().BackendVersion == "" {
		err := sp.Update(func(s *state.State) {
			s.BackendName = "SQLite"

			row := db.QueryRowContext(context.Background(), "SELECT sqlite_version()")
			if err := row.Scan(&s.BackendVersion); err != nil {
				l.Error("sqlite.metadata.pool.openDB: failed to query SQLite version", logging.Error(err))
			}
		})
		if err != nil {
			l.Error("sqlite.metadata.pool.openDB: failed to update state", logging.Error(err))
		}
	}

	return fsql.WrapDB(db, name, l), nil
}

// sqliteAutoRepairEnabled reports whether automatic SQLite corruption detection and
// bloat repair is on (the default). Set FERRETDB_SQLITE_AUTO_REPAIR=false to disable.
func sqliteAutoRepairEnabled() bool {
	return os.Getenv("FERRETDB_SQLITE_AUTO_REPAIR") != "false"
}

// sqliteQuickCheck runs SQLite's fast, read-only integrity check and returns its
// result ("ok" when healthy, otherwise the first reported problem).
func sqliteQuickCheck(ctx context.Context, db *sql.DB) (string, error) {
	var res string
	err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&res)

	return res, err
}

// sqliteBloatMinPages is the smallest database (in pages) worth VACUUMing — ~1 MiB
// at 4 KiB pages — so tiny databases are never churned.
const sqliteBloatMinPages = int64(256)

// sqliteBloated reports whether the database file is bloated with free pages — at
// least sqliteBloatMinPages total and at least a quarter of them on the freelist —
// so a VACUUM would reclaim meaningful space. Returns the page counts for logging.
func sqliteBloated(ctx context.Context, db *sql.DB) (bloated bool, pageCount, freeCount int64, err error) {
	if err = db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return false, 0, 0, err
	}

	if err = db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freeCount); err != nil {
		return false, pageCount, 0, err
	}

	bloated = pageCount >= sqliteBloatMinPages && freeCount*4 >= pageCount

	return bloated, pageCount, freeCount, nil
}

// checkAndRepairSQLite is an automatic safety measure (#6492) run when a
// persistent SQLite database is opened:
//
//  1. DETECT corruption with a fast quick_check and log it prominently. Corruption
//     cannot be repaired in place — the client restores from its backup copy
//     or re-migrates the text data from MongoDB — so this only detects and reports.
//  2. FIX bloat automatically: VACUUM a file whose free pages dominate it. The
//     simulated OpLog and busy client collections churn heavily, leaving free pages
//     that keep the file large and the CPU high; reclaiming them shrinks the file.
//
// In-memory databases have nothing to check or reclaim. Best-effort: every failure
// is logged but never blocks opening the database.
func checkAndRepairSQLite(db *sql.DB, name string, memory bool, l *slog.Logger) {
	if memory || !sqliteAutoRepairEnabled() {
		return
	}

	ctx := context.Background()
	l = logging.WithName(l, "autorepair")

	if res, err := sqliteQuickCheck(ctx, db); err != nil {
		l.Warn("quick_check could not run", slog.String("db", name), logging.Error(err))
	} else if res != "ok" {
		l.Error("SQLite CORRUPTION DETECTED - restore from backup or re-migrate text data",
			slog.String("db", name), slog.String("quick_check", res))
	}

	bloated, pageCount, freeCount, err := sqliteBloated(ctx, db)
	if err != nil {
		l.Warn("bloat check could not run", slog.String("db", name), logging.Error(err))
		return
	}

	if bloated {
		l.Info("VACUUMing bloated database to reclaim free pages",
			slog.String("db", name), slog.Int64("pages", pageCount), slog.Int64("freePages", freeCount))

		if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
			l.Warn("VACUUM failed", slog.String("db", name), logging.Error(err))
		}
	}
}

// idleConnLimit returns how many connections are kept WARM (idle) for a
// SQLite-backed database, given GOMAXPROCS.
//
// Together with the filter pushdown — which turned the application's
// queries from whole-collection scans into indexed lookups — bounding the warm
// set is what keeps the pure-Go SQLite (modernc) allocator/WAL mutexes and the
// Go GC from thrashing (the reported 821k futex + 530k nanosleep syscalls per
// 30s and 250-400% CPU with 1-2 real users). Proportional to the machine, with
// a floor of 4 (small machines still get a usable pool) and a cap of 16 (where
// contention starts to dominate). Non-positive GOMAXPROCS yields the floor.
func idleConnLimit(gomaxprocs int) int {
	idle := 2 * gomaxprocs

	if idle < 4 {
		idle = 4
	}

	if idle > 16 {
		idle = 16
	}

	return idle
}

// configurePool applies the connection-pool limits to a SQLite-backed database.
// Split out from openDB so the limits can be unit-tested.
func configurePool(db *sql.DB, memory bool) {
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	db.SetMaxIdleConns(idleConnLimit(runtime.GOMAXPROCS(0)))

	// Do NOT cap the number of connections that may be OPEN at
	// once (an earlier fix set both idle and open to <=16, which regressed).
	// Meteor keeps a server-side find cursor open between getMore round-trips, and
	// each open cursor PINS one pooled connection for its entire lifetime. With a
	// small open cap a handful of parked Meteor cursors exhausted the pool, so
	// every other query — login-token lookups, board loads — blocked waiting for a
	// free connection for minutes and logins failed with "Must be logged in".
	// Leaving MaxOpenConns unlimited means connection checkout never starves;
	// SQLite still serialises writers itself, and steady-state warm connections
	// stay bounded by SetMaxIdleConns above.
	db.SetMaxOpenConns(0)

	// Each connection to in-memory database uses its own database.
	// See https://www.sqlite.org/inmemorydb.html.
	// We don't want that.
	if memory {
		db.SetMaxIdleConns(1)
		db.SetMaxOpenConns(1)
	}
}
