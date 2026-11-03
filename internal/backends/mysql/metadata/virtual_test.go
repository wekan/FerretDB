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
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The metadata table's generated columns are VIRTUAL, not STORED: TiDB refuses
// `ALTER TABLE ... ADD COLUMN ... STORED` ("unsupported add column ... stored
// generated column"), and so a FerretDB on it could not create its first
// database. MySQL and MariaDB accept both, and the table holds one row per
// collection, so nothing is lost. This reads the source because the statement is
// built inline and only a server shows the difference.
func TestMetadataGeneratedColumnsAreVirtual(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("registry.go")
	require.NoError(t, err)
	src := string(b)

	// Every ADD COLUMN of a generated column in this file is VIRTUAL, except the
	// MariaDB fallback for a CAST (a DECIMAL column indexed instead of the
	// expression), which is outside the metadata table and keeps working as it did.
	for _, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, "ADD COLUMN %s VARCHAR(255) GENERATED ALWAYS AS") {
			continue
		}

		assert.NotContains(t, line, "STORED", line)
		assert.Contains(t, line, "VIRTUAL", line)
	}

	assert.Equal(t, 3, strings.Count(src, "ADD COLUMN %s VARCHAR(255)"), "the metadata table's two columns and the index column")
}
