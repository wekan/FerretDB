// Copyright 2026 FerretDB Inc.
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

package password

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSCRAMSHA1CodeQLSuppression(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("scramsha1.go")
	require.NoError(t, err)

	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		if !strings.Contains(line, "md5.Sum") {
			continue
		}

		require.Contains(t, line, "codeql[go/weak-sensitive-data-hashing]")
		require.Contains(t, line, "lgtm[go/weak-sensitive-data-hashing]")
		require.Equal(t, 1, strings.Count(string(b), "md5.Sum"))

		return
	}

	t.Fatal("SCRAM-SHA-1 MD5 preparation not found")
}
