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

	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	// WeKan #6467: was 100/100. Meteor opens ~100 sockets, and letting all of
	// them run SQLite queries CONCURRENTLY meant dozens of simultaneous scans
	// fighting over the pure-Go SQLite (modernc) allocator/WAL mutexes and the Go
	// GC on small machines — hundreds of thousands of futex calls per second and
	// 250-400% CPU with 1-2 real users. SQLite serves one writer at a time and
	// only NumCPU readers make progress anyway; queueing excess queries in
	// database/sql is far cheaper than mutex thrash. Keep it proportional to the
	// machine, capped where contention starts to dominate.
	conns := 2 * runtime.GOMAXPROCS(0)
	if conns < 4 {
		conns = 4
	}

	if conns > 16 {
		conns = 16
	}

	db.SetMaxIdleConns(conns)
	db.SetMaxOpenConns(conns)

	// Each connection to in-memory database uses its own database.
	// See https://www.sqlite.org/inmemorydb.html.
	// We don't want that.
	if memory {
		db.SetMaxIdleConns(1)
		db.SetMaxOpenConns(1)
	}

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
