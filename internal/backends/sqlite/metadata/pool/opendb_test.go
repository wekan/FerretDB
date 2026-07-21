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
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register database/sql driver
)

// quietLogger discards output, for exercising the auto-repair path without noise.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// openTestFileDB opens a fresh on-disk SQLite database in a temp dir.
func openTestFileDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "test.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())

	return db
}

// TestSqliteQuickCheckHealthy: a healthy database passes the fast integrity check.
func TestSqliteQuickCheckHealthy(t *testing.T) {
	t.Parallel()

	db := openTestFileDB(t)
	_, err := db.Exec(`CREATE TABLE t (x TEXT)`)
	require.NoError(t, err)

	res, err := sqliteQuickCheck(context.Background(), db)
	require.NoError(t, err)
	assert.Equal(t, "ok", res)
}

// TestSqliteBloatedAndVacuum: a file with a large freelist is detected as bloated,
// and checkAndRepairSQLite VACUUMs it so the free pages are reclaimed (#6492).
func TestSqliteBloatedAndVacuum(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestFileDB(t)

	_, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, blob TEXT)`)
	require.NoError(t, err)

	// Grow the file well past the min-pages threshold...
	blob := strings.Repeat("x", 400)
	for i := 0; i < 5000; i++ {
		_, err = db.Exec(`INSERT INTO t (blob) VALUES (?)`, blob)
		require.NoError(t, err)
	}

	// ...then delete 90% to leave a large freelist (bloat) without shrinking the file.
	_, err = db.Exec(`DELETE FROM t WHERE id % 10 != 0`)
	require.NoError(t, err)

	bloated, pageCount, freeCount, err := sqliteBloated(ctx, db)
	require.NoError(t, err)
	require.True(t, bloated, "expected bloat (pages=%d free=%d)", pageCount, freeCount)

	checkAndRepairSQLite(db, "test", false, quietLogger())

	var freeAfter int64
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freeAfter))
	assert.LessOrEqualf(t, freeAfter, int64(1), "VACUUM must reclaim the %d free pages", freeCount)
}

// TestSqliteBloatedNegativeSmallDB: a small healthy database is NOT flagged as
// bloated — tiny files are never churned.
func TestSqliteBloatedNegativeSmallDB(t *testing.T) {
	t.Parallel()

	db := openTestFileDB(t)
	_, err := db.Exec(`CREATE TABLE t (x TEXT)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO t VALUES ('hello')`)
	require.NoError(t, err)

	bloated, _, _, err := sqliteBloated(context.Background(), db)
	require.NoError(t, err)
	assert.False(t, bloated, "a tiny database must never be considered bloated")
}

// TestCheckAndRepairSQLiteNoopWhenDisabledOrMemory is a negative test: the repair is
// skipped for in-memory databases and when disabled via the env var.
func TestCheckAndRepairSQLiteNoopWhenDisabledOrMemory(t *testing.T) {
	db := openTestFileDB(t)

	// memory=true -> skipped (no panic).
	assert.NotPanics(t, func() { checkAndRepairSQLite(db, "mem", true, quietLogger()) })

	// disabled via env -> skipped, and the toggle reads false.
	t.Setenv("FERRETDB_SQLITE_AUTO_REPAIR", "false")
	assert.False(t, sqliteAutoRepairEnabled())
	assert.NotPanics(t, func() { checkAndRepairSQLite(db, "test", false, quietLogger()) })

	// default (any other value) -> enabled.
	t.Setenv("FERRETDB_SQLITE_AUTO_REPAIR", "")
	assert.True(t, sqliteAutoRepairEnabled())
}

// TestIdleConnLimit pins the warm (idle) connection cap: proportional to the
// machine, with a floor of 4 and a cap of 16.
func TestIdleConnLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		gomaxprocs int
		expected   int
	}{
		{gomaxprocs: 1, expected: 4},   // floor
		{gomaxprocs: 2, expected: 4},   // 2*2, still at the floor
		{gomaxprocs: 3, expected: 6},   // 2*3
		{gomaxprocs: 8, expected: 16},  // 2*8, exactly the cap
		{gomaxprocs: 9, expected: 16},  // capped
		{gomaxprocs: 64, expected: 16}, // capped
		// negative cases: a non-positive GOMAXPROCS must never yield <4 (or 0,
		// which would deadlock every query).
		{gomaxprocs: 0, expected: 4},
		{gomaxprocs: -1, expected: 4},
	} {
		assert.Equalf(t, tc.expected, idleConnLimit(tc.gomaxprocs),
			"idleConnLimit(%d)", tc.gomaxprocs)
	}
}

// TestConfigurePoolDoesNotStarveCheckout is the regression test for connection-pool
// checkout starvation: the earlier CPU fix capped MaxOpenConns at <=16, but each parked
// Meteor find cursor pins a pooled connection, so a small open cap starved every
// other query (minutes-long board loads, "Must be logged in"). MaxOpenConns must
// now be unlimited so connection checkout never starves.
func TestConfigurePoolDoesNotStarveCheckout(t *testing.T) {
	t.Parallel()

	// An on-disk file DB; the in-memory path deliberately forces a 1-connection
	// pool (see configurePool), which is a different case tested below.
	uri := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_pragma=busy_timeout(10000)"

	db, err := sql.Open("sqlite", uri)
	require.NoError(t, err)

	t.Cleanup(func() { _ = db.Close() })

	configurePool(db, false)

	// The open cap must be unlimited (0). With the regressed cap of 16, this
	// alone documents the fix.
	require.Equal(t, 0, db.Stats().MaxOpenConnections,
		"MaxOpenConns must be unlimited so parked cursors cannot starve checkout")

	// Behavioural proof: hold many MORE than the old cap of 16 connections
	// simultaneously. With MaxOpenConns=16 the 17th checkout would block until
	// the context deadline and fail; here every checkout must succeed promptly.
	const n = 32

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conns := make([]*sql.Conn, 0, n)

	for i := 0; i < n; i++ {
		c, err := db.Conn(ctx)
		require.NoErrorf(t, err, "connection checkout %d starved — open cap too low?", i)

		conns = append(conns, c)
	}

	assert.GreaterOrEqual(t, db.Stats().OpenConnections, n,
		"all %d connections should be open at once", n)

	for _, c := range conns {
		_ = c.Close()
	}
}

// TestConfigurePoolMemoryIsSingleConn keeps the existing in-memory contract:
// each connection to an in-memory database is its own database, so the pool must
// be limited to exactly one connection.
func TestConfigurePoolMemoryIsSingleConn(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:mem1?mode=memory&cache=shared")
	require.NoError(t, err)

	t.Cleanup(func() { _ = db.Close() })

	configurePool(db, true)

	assert.Equal(t, 1, db.Stats().MaxOpenConnections,
		"in-memory databases must be pinned to a single connection")
}
