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

// Package fsql provides [database/sql] utilities.
package fsql

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/logging"
	"github.com/FerretDB/FerretDB/internal/util/resource"
	"github.com/FerretDB/FerretDB/internal/util/sqlguard"
)

// DB wraps [*database/sql.DB] with tracing, metrics, logging, and resource tracking.
//
// It exposes the subset of *sql.DB methods we use except that it returns *Rows instead of *sql.Rows.
// It also exposes additional methods.
type DB struct {
	*metricsCollector

	sqlDB     *sql.DB
	l         *slog.Logger
	token     *resource.Token
	dialect   sqlguard.Dialect
	BatchSize int
}

// WrapDB creates a new DB.
//
// Name is used for metric label values, etc.
// Logger (that will be named) is used for query logging.
func WrapDB(db *sql.DB, name string, l *slog.Logger) *DB {
	if db == nil {
		return nil
	}

	res := &DB{
		metricsCollector: newMetricsCollector(name, db.Stats),
		sqlDB:            db,
		l:                logging.WithName(l, name),
		token:            resource.NewToken(),
		dialect:          sqlguard.DialectByName(name),
	}

	resource.Track(res, res.token)

	return res
}

// Close calls [*sql.DB.Close].
func (db *DB) Close() error {
	resource.Untrack(db, db.token)
	return db.sqlDB.Close()
}

// Ping calls [*sql.DB.Ping].
func (db *DB) Ping(ctx context.Context) error {
	return db.sqlDB.Ping()
}

// QueryContext calls [*sql.DB.QueryContext].
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	if err := db.guard(ctx, query); err != nil {
		return nil, err
	}

	start := time.Now()

	fields := []any{slog.Any("args", args)}
	db.l.With(fields...).DebugContext(ctx, fmt.Sprintf(">>> %s", query))

	rows, err := db.sqlDB.QueryContext(ctx, query, args...)

	dur := time.Since(start)
	fields = append(fields, slog.Duration("time", dur), logging.Error(err))
	db.l.With(fields...).DebugContext(ctx, fmt.Sprintf("<<< %s", query))
	logSlow(ctx, db.l, query, dur)

	return wrapRows(rows), err
}

// QueryRowContext calls [*sql.DB.QueryRowContext].
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()

	fields := []any{slog.Any("args", args)}
	db.l.With(fields...).DebugContext(ctx, fmt.Sprintf(">>> %s", query))

	row := db.sqlDB.QueryRowContext(ctx, query, args...)

	dur := time.Since(start)
	fields = append(fields, slog.Duration("time", dur), logging.Error(row.Err()))
	db.l.With(fields...).DebugContext(ctx, fmt.Sprintf("<<< %s", query))
	logSlow(ctx, db.l, query, dur)

	return row
}

// ExecContext calls [*sql.DB.ExecContext].
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := db.guard(ctx, query); err != nil {
		return nil, err
	}

	start := time.Now()

	fields := []any{slog.Any("args", args)}
	db.l.With(fields...).DebugContext(ctx, fmt.Sprintf(">>> %s", query))

	res, err := db.sqlDB.ExecContext(ctx, query, args...)

	if res != nil {
		ra, _ := res.RowsAffected()
		fields = append(fields, slog.Int64("rows", ra))
	}

	dur := time.Since(start)
	fields = append(fields, slog.Duration("time", dur), logging.Error(err))
	db.l.With(fields...).DebugContext(ctx, fmt.Sprintf("<<< %s", query))
	logSlow(ctx, db.l, query, dur)

	return res, err
}

// InTransaction wraps the given function f in a transaction.
//
// If f returns an error or context is canceled, the transaction is rolled back.
func (db *DB) InTransaction(ctx context.Context, f func(*Tx) error) (err error) {
	var sqlTx *sql.Tx

	if sqlTx, err = db.sqlDB.BeginTx(ctx, nil); err != nil {
		err = lazyerrors.Error(err)
		return
	}

	tx := wrapTx(sqlTx, db.l)

	var done bool

	defer func() {
		// It is not enough to check `err == nil` there,
		// because in tests `f` could contain testify/require.XXX or `testing.TB.FailNow()` calls
		// that call `runtime.Goexit()`, leaving `err` unset in `err = f(tx)` below.
		// This situation would hang a test.
		//
		// As a bonus, checking a separate variable also handles any panics in `f`,
		// including `panic(nil)` that is problematic for tests too.
		if done {
			return
		}

		if err == nil {
			err = lazyerrors.Errorf("transaction was not committed")
		}

		_ = tx.Rollback()
	}()

	if err = f(tx); err != nil {
		// do not wrap f's error because the caller depends on it in some cases
		return
	}

	if err = tx.Commit(); err != nil {
		err = lazyerrors.Error(err)
		return
	}

	done = true

	return
}

// check interfaces
var (
	_ prometheus.Collector = (*DB)(nil)
)

// guard is the last look at a statement before the database sees it.
//
// Values are bound, never formatted in, and names are sanitised where they are
// made - so nothing should ever reach here carrying a statement separator, a
// comment introducer or an unclosed quote. If something does, it is either a bug
// in a statement builder or an injection that got past one, and both are worth
// refusing rather than executing.
//
// The refusal is LOGGED at error level with a "SECURITY:" prefix and the
// statement, because that line is the only evidence such a thing happened. The
// error also carries a canary marker, so the client can surface the attempt to
// its operator (internal/util/canary).
func (db *DB) guard(ctx context.Context, query string) error {
	err := sqlguard.Check(db.dialect, query)
	if err == nil {
		return nil
	}

	db.l.ErrorContext(ctx, fmt.Sprintf("SECURITY: refusing to execute a suspicious statement: %s", err),
		slog.String("statement", query))

	return err
}
