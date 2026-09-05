//go:build ferretdb_no_sqlite

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckSQLiteDisabled(t *testing.T) {
	t.Parallel()
	require.EqualError(t, checkSQLite("ignored"), "SQLite support is disabled in this build")
}
