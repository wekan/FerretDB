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

//go:build ferretdb_sqlite_short && cgo

package pool

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"

	"github.com/mattn/go-sqlite3"
)

type shortSQLiteConnector struct {
	dsn    string
	driver *sqlite3.SQLiteDriver
}

func (c shortSQLiteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.driver.Open(c.dsn)
}

func (c shortSQLiteConnector) Driver() driver.Driver {
	return c.driver
}

func init() {
	openShortSQLite = func(dsn string) (*sql.DB, error) {
		uri, err := url.Parse(dsn)
		if err != nil {
			return nil, err
		}
		pragmas := uri.Query()["_pragma"]
		d := &sqlite3.SQLiteDriver{ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			for _, pragma := range pragmas {
				if _, err := conn.Exec("PRAGMA "+pragma, nil); err != nil {
					return err
				}
			}
			return nil
		}}
		return sql.OpenDB(shortSQLiteConnector{dsn: dsn, driver: d}), nil
	}
	isShortUniqueConstraint = func(err error) bool {
		var sqliteError sqlite3.Error
		return errors.As(err, &sqliteError) && sqliteError.ExtendedCode == sqlite3.ErrConstraintUnique
	}
}
