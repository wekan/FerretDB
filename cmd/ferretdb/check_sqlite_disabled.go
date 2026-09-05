//go:build ferretdb_no_sqlite

package main

import "fmt"

// checkSQLite keeps the CLI shape deterministic in SQLite-disabled builds
// while ensuring that the SQLite driver and backend are not linked into them.
func checkSQLite(_ string) error {
	return fmt.Errorf("SQLite support is disabled in this build")
}
