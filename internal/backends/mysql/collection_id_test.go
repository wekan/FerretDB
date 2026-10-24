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

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"

	"github.com/FerretDB/FerretDB/internal/backends/mysql/metadata"
)

// An update or delete by _id compares the _id index's generated column with the
// id's JSON text as a plain string, then exactly. MariaDB 12.3 compares two equal
// JSON strings (JSON_EXTRACT(doc, '$._id') = JSON_EXTRACT(?, '$')) as different,
// so every update and delete by a string _id matched nothing.
func TestIDEqualsUsesTheIndexedColumn(t *testing.T) {
	t.Parallel()

	col := metadata.QuoteIdent(metadata.SafeColumnName("_id"))

	cond := idEquals()
	assert.True(t, strings.HasPrefix(cond, col+" = ?"), "the indexed column first: %s", cond)
	assert.Contains(t, cond, "CAST("+col+" AS BINARY) = CAST(? AS BINARY)", "and an exact, case-sensitive match")
	assert.NotContains(t, cond, "JSON_EXTRACT", "negative: no JSON-to-JSON comparison")

	q, args := deleteByIDs("db", "t", []any{`"a"`, `"b"`})
	assert.Contains(t, q, col+" IN (?, ?) AND CAST("+col+" AS BINARY) IN (CAST(? AS BINARY), CAST(? AS BINARY))")
	assert.Equal(t, []any{`"a"`, `"b"`, `"a"`, `"b"`}, args)
	assert.NotContains(t, q, "JSON_EXTRACT")

	// A table made before the column had this name falls back, on error 1054 only.
	assert.True(t, unknownColumn(&mysql.MySQLError{Number: 1054}))
	assert.False(t, unknownColumn(&mysql.MySQLError{Number: 1062}), "negative: a duplicate key is not a missing column")
	assert.False(t, unknownColumn(errors.New("x")))
	assert.False(t, unknownColumn(nil))
}
