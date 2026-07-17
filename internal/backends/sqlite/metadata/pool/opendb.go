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

// idleConnLimit returns how many connections are kept WARM (idle) for a
// SQLite-backed database, given GOMAXPROCS.
//
// WeKan #6467: together with the #6468 filter pushdown — which turned WeKan's
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

	// WeKan #6467/#6469: do NOT cap the number of connections that may be OPEN at
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
