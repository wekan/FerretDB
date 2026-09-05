package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestCheckSQLite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	healthy := filepath.Join(dir, "healthy.sqlite")
	db, err := sql.Open("sqlite", healthy)
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE cards (id TEXT PRIMARY KEY, title TEXT)")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, checkSQLite(healthy))

	missing := filepath.Join(dir, "missing.sqlite")
	require.Error(t, checkSQLite(missing), "read-only check must not create a missing file")
	_, err = os.Stat(missing)
	require.ErrorIs(t, err, os.ErrNotExist)

	corrupt := filepath.Join(dir, "corrupt.sqlite")
	require.NoError(t, os.WriteFile(corrupt, []byte("not a sqlite database"), 0o600))
	require.Error(t, checkSQLite(corrupt))
}
