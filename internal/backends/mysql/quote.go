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

package mysql

import "strings"

// quoteIdent quotes a database, table or column name for MySQL and MariaDB.
//
// MySQL quotes identifiers with BACKTICKS. Double quotes are string literals
// unless the session runs in ANSI_QUOTES mode, which is not the default and which
// this backend does not set - so Go's %q verb, correct for the PostgreSQL backend
// this code was modelled on, produced statements MySQL refuses outright:
//
//	INSERT INTO "conformance"."conformance_7d687332" (_ferretdb_sjson) VALUES (?)
//	Error 1064 (42000): You have an error in your SQL syntax ...
//
// Every insert failed with that, which is to say the mysql backend could not store
// anything at all. A backtick inside an identifier is escaped by doubling it, as
// MySQL requires; the names this backend generates never contain one, but a
// quoting helper that only works for well-behaved input is not a quoting helper.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}
