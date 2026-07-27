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
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestQuoteIdent pins what MySQL accepts: backticks, and a backtick inside an
// identifier doubled. Double quotes - what Go's %q produced here before - are
// string literals to MySQL, and every INSERT this backend built was rejected with
// "Error 1064 (42000): You have an error in your SQL syntax".
func TestQuoteIdent(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in       string
		expected string
	}{
		"Simple":       {in: "conformance", expected: "`conformance`"},
		"Generated":    {in: "conformance_7d687332", expected: "`conformance_7d687332`"},
		"WithBacktick": {in: "we`kan", expected: "`we``kan`"},
		"Empty":        {in: "", expected: "``"},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.expected, quoteIdent(tc.in))
			// Never the double-quoted form, whatever the input.
			assert.NotContains(t, quoteIdent(tc.in), `"`)
		})
	}
}
