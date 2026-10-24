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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/util/sqlguard"
)

func TestMultikeyStatementsPassTheGuard(t *testing.T) {
	t.Parallel()

	// Every statement runs through fsql, whose guard refuses a `;`, a comment or
	// unbalanced quotes - so a trigger body must be ONE statement.
	statements := append(multikeyCreateSQL("wekan", "boards_1234abcd", "members", "userId"),
		multikeyDropSQL("wekan", "boards_1234abcd", "members", "userId")...)
	require.Len(t, statements, 11)

	for _, q := range statements {
		assert.NoError(t, sqlguard.Check(sqlguard.MySQL, q), q)
		assert.NotContains(t, q, ";")
	}

	// The triggers come before the backfill, so no write is missed while it runs.
	create := multikeyCreateSQL("wekan", "boards_1234abcd", "members", "userId")
	assert.True(t, strings.HasPrefix(create[0], "CREATE TABLE IF NOT EXISTS"))
	for _, q := range create[1:5] {
		assert.True(t, strings.HasPrefix(q, "CREATE TRIGGER IF NOT EXISTS"), q)
	}
	assert.True(t, strings.HasPrefix(create[5], "INSERT IGNORE INTO"))
	assert.Contains(t, create[5], "FROM `wekan`.`boards_1234abcd` AS doc, JSON_TABLE(doc._ferretdb_sjson,")
}
