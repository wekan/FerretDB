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

//go:build ferretdb_no_postgresql && ferretdb_no_mysql && !ferretdb_hana && !ferretdb_no_sqlite

package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSQLiteOnlyBuild runs only in the SQLite-only build
// (-tags ferretdb_no_postgresql,ferretdb_no_mysql): it has exactly one handler,
// and that handler is the default, so the binary starts without --handler.
func TestSQLiteOnlyBuild(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"sqlite"}, Handlers())
	assert.Equal(t, "sqlite", DefaultHandler())
}
