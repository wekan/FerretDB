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

package sqlguard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guard has two ways to be useless: passing something injected, and refusing
// the statements FerretDB actually builds. Both are tested, because a guard that
// cries wolf gets removed, and a guard that never fires was never there.

func TestCheckAcceptsRealStatements(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		dialect   Dialect
		statement string
	}{
		"PgSelect": {PostgreSQL, `SELECT _jsonb FROM "conformance"."cards_1a2b3c4d"`},
		"PgInsert": {PostgreSQL, `INSERT INTO "db"."t" (_jsonb) VALUES ($1), ($2)`},
		"PgIndex": {PostgreSQL, `CREATE UNIQUE INDEX "t_id_idx" ON "db"."t" (((_jsonb->'_id')))`},
		"PgRange": {PostgreSQL, `SELECT _jsonb FROM "db"."t" WHERE jsonb_typeof(_jsonb->'n') = 'number' AND (_jsonb->>'n')::numeric > $1`},
		"MySQLSelect": {MySQL, "SELECT _ferretdb_sjson FROM `db`.`t`"},
		"MySQLGenerated": {MySQL, "ALTER TABLE `db`.`t` ADD COLUMN `a_b_1a2b3c4d` VARCHAR(255) GENERATED ALWAYS AS ((_ferretdb_sjson->'$.a.b')) STORED"},
		"MySQLComment": {MySQL, "SELECT /* find on cards */ _ferretdb_sjson FROM `db`.`t`"},
		"MySQLEscaped": {MySQL, "SELECT _ferretdb_sjson FROM `db`.`t` WHERE _ferretdb_sjson->'$.a' = 'it\\'s'"},
		"SQLiteSelect": {SQLite, `SELECT _ferretdb_sjson FROM "cards_1a2b3c4d"`},
		"DoubledQuoteInLiteral": {PostgreSQL, `SELECT * FROM "t" WHERE a = 'it''s'`},
		"DoubledQuoteInIdent": {PostgreSQL, `SELECT * FROM "we""ird"`},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, Check(tc.dialect, tc.statement), tc.statement)
		})
	}
}

func TestCheckRefusesInjection(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		dialect   Dialect
		statement string
	}{
		// What an unquoted identifier lets through.
		"SecondStatement": {PostgreSQL, `SELECT * FROM "t"; DROP TABLE users`},
		"LineComment":     {PostgreSQL, `SELECT * FROM "t" WHERE a = 1 -- AND b = 2`},
		"HashComment":     {MySQL, "SELECT * FROM `t` # everything after me is gone"},
		"BlockComment":    {PostgreSQL, `SELECT * FROM "t" /* unterminated`},
		"NestedComment":   {PostgreSQL, `SELECT /* a /* b */ */ 1`},
		"UnclosedLiteral": {PostgreSQL, `SELECT * FROM "t" WHERE a = 'unterminated`},
		"UnclosedIdent":   {PostgreSQL, `SELECT * FROM "unterminated`},
		"UnclosedIdentMySQL": {MySQL, "SELECT * FROM `unterminated"},
		"ExtraParen":      {PostgreSQL, `SELECT * FROM "t" WHERE (a = 1))`},
		"MissingParen":    {PostgreSQL, `SELECT * FROM "t" WHERE (a = 1`},
		// The exact shapes the mysql index path used to allow.
		"IndexKeyBreakout": {MySQL, "ALTER TABLE `db`.`t` ADD COLUMN a VARCHAR(255) GENERATED ALWAYS AS ((_ferretdb_sjson->'$.a')); DROP TABLE users; --')) STORED"},
		"IdentBreakout":    {MySQL, "INSERT INTO `db`.`t`; DROP DATABASE wekan; --` (_ferretdb_sjson) VALUES (?)"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := Check(tc.dialect, tc.statement)
			require.Error(t, err, tc.statement)
			assert.ErrorIs(t, err, ErrSuspicious)
		})
	}
}

func TestSafeComment(t *testing.T) {
	t.Parallel()

	// A comment is the one piece of client text that is written into SQL rather
	// than bound, so these are the ways out of the block.
	for _, in := range []string{
		"*/ DROP TABLE users; /*",
		"/* nested */",
		"-- line",
		"a\x00b",
		"line\nbreak",
		strings.Repeat("x", 5000),
	} {
		out := SafeComment(in)

		assert.NotContains(t, out, "*/", in)
		assert.NotContains(t, out, "/*", in)
		assert.NotContains(t, out, "--", in)
		assert.NotContains(t, out, "\x00", in)
		assert.NotContains(t, out, "\n", in)
		assert.LessOrEqual(t, len(out), 1100, "a comment cannot be used to grow a statement without bound")

		// And the result is safe where it is actually used.
		assert.NoError(t, Check(MySQL, "SELECT /* "+out+" */ 1 FROM `t`"))
		assert.NoError(t, Check(PostgreSQL, `SELECT /* `+out+` */ 1 FROM "t"`))
	}
}

func TestDialectByName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, MySQL, DialectByName("mysql_pool"))
	assert.Equal(t, MySQL, DialectByName("mariadb"))
	assert.Equal(t, SQLite, DialectByName("sqlite_pool"))
	assert.Equal(t, PostgreSQL, DialectByName("postgresql_pool"))
	// An unknown backend is guarded MORE, not less: PostgreSQL rules have no
	// backslash escape inside literals, so nothing can hide behind one.
	assert.Equal(t, PostgreSQL, DialectByName("something-new"))
}
