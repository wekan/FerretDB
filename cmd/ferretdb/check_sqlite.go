package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// checkSQLite opens one existing file read-only and runs the same quick integrity
// check used by the SQLite backend. It is a CLI primitive for startup recovery;
// it never creates, repairs, vacuums, or otherwise writes the database.
func checkSQLite(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve SQLite path: %w", err)
	}

	u := &url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("open SQLite read-only: %w", err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil {
		return fmt.Errorf("SQLite quick_check failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite quick_check: %s", result)
	}

	fmt.Println("ok")
	return nil
}
