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

package metadata

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Everything a client can name - a database, a collection, an index, an index KEY
// - eventually reaches SQL. Values never do: they are bound as parameters. These
// three helpers are what stands between a name and the statement it appears in,
// and they are here, in the metadata package, because that is where names are
// made.

// QuoteIdent quotes a database, table, column or index name for MySQL and MariaDB.
//
// MySQL quotes identifiers with BACKTICKS; double quotes are string literals
// unless the session runs in ANSI_QUOTES, which this backend does not set. A
// backtick inside an identifier is escaped by doubling it.
func QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// QuoteString quotes a value as a MySQL string literal.
//
// User text reaches SQL in exactly one place that cannot use a placeholder: the
// JSON path of a generated column, which MySQL requires as a literal inside DDL.
// A single quote would end that literal, so it is doubled; a backslash, which
// MySQL treats as an escape inside string literals by default, is doubled too.
func QuoteString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'"
}

// SafeColumnName turns a user-provided index field path into something that can
// only be a column name.
//
// A MongoDB index key is any field path the client likes - `a.b`, but also
// `a')) STORED, ADD COLUMN pwned VARCHAR(1) GENERATED ALWAYS AS (('x`. It used to
// be spliced into ALTER TABLE and CREATE INDEX unescaped. Now it gets the same
// treatment table and index names get: everything outside [a-z0-9_] replaced,
// plus a hash of the ORIGINAL path, so that two different hostile paths cannot
// collapse into the same column.
func SafeColumnName(field string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(field))

	name := specialCharacters.ReplaceAllString(strings.ToLower(field), "_")

	suffix := fmt.Sprintf("_%08x", h.Sum32())
	if l := maxIndexNameLength - len(suffix); len(name) > l {
		name = name[:l]
	}

	return name + suffix
}
