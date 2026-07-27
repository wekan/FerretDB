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
)

// Everything a client can NAME reaches SQL: a database, a collection, an index,
// and an index KEY. Values never do - they are bound. These tests are the
// injection boundary for the names, written as the attacks themselves.

// hostile is the input a client would actually send to break out of a statement.
var hostile = []string{
	`a'); DROP TABLE users; --`,
	"a`); DROP TABLE users; --",
	`a')) STORED, ADD COLUMN pwned VARCHAR(1) GENERATED ALWAYS AS (('x`,
	`"; DROP DATABASE wekan; --`,
	`x' OR '1'='1`,
	`x\'; DROP TABLE t; --`,
	`../../etc/passwd`,
	"tab\there",
	"newline\nhere",
	"null\x00byte",
	`💣`,
	strings.Repeat("a", 500),
}

func TestQuoteIdent(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "`conformance`", QuoteIdent("conformance"))
	assert.Equal(t, "`we``kan`", QuoteIdent("we`kan"), "an inner backtick is doubled")
	assert.Equal(t, "``", QuoteIdent(""))

	for _, in := range hostile {
		out := QuoteIdent(in)

		// It starts and ends with a backtick, and every backtick between them is
		// doubled - which is the whole definition of "cannot end the identifier
		// early".
		require.True(t, strings.HasPrefix(out, "`") && strings.HasSuffix(out, "`"), out)

		inner := out[1 : len(out)-1]
		for i := 0; i < len(inner); i++ {
			if inner[i] != '`' {
				continue
			}

			require.Less(t, i+1, len(inner), "a trailing lone backtick would end the identifier: %q", out)
			require.Equal(t, byte('`'), inner[i+1], "a lone backtick inside: %q", out)

			i++
		}
	}
}

func TestQuoteString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `'$.a'`, QuoteString("$.a"))
	assert.Equal(t, `'it''s'`, QuoteString("it's"), "a quote is doubled")
	assert.Equal(t, `'a\\b'`, QuoteString(`a\b`), "a backslash is doubled - MySQL escapes with it")

	for _, in := range hostile {
		out := QuoteString(in)

		require.True(t, strings.HasPrefix(out, "'") && strings.HasSuffix(out, "'"), out)

		// No lone quote inside, and no lone backslash that could escape the
		// closing one.
		inner := out[1 : len(out)-1]
		for i := 0; i < len(inner); i++ {
			switch inner[i] {
			case '\'':
				require.Less(t, i+1, len(inner), "trailing lone quote: %q", out)
				require.Equal(t, byte('\''), inner[i+1], "lone quote inside: %q", out)

				i++
			case '\\':
				require.Less(t, i+1, len(inner), "trailing lone backslash: %q", out)
				require.Equal(t, byte('\\'), inner[i+1], "lone backslash inside: %q", out)

				i++
			}
		}
	}
}

func TestSafeColumnName(t *testing.T) {
	t.Parallel()

	for _, in := range hostile {
		out := SafeColumnName(in)

		// Nothing but a name: lower-case letters, digits and underscores.
		for _, r := range out {
			require.True(t, (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_',
				"%q produced %q, which contains %q", in, out, r)
		}

		require.LessOrEqual(t, len(out), maxIndexNameLength, "must fit MySQL's identifier limit")
		require.NotEmpty(t, out)
	}

	// Two different hostile paths that sanitise to the same letters must still be
	// two different columns - otherwise one index would silently answer for
	// another field.
	a := SafeColumnName(`a'); DROP TABLE x; --`)
	b := SafeColumnName(`a"); DROP TABLE y; --`)
	assert.NotEqual(t, a, b, "the hash of the original path keeps them apart")

	// And the ordinary case is still readable.
	assert.Contains(t, SafeColumnName("title"), "title")
}
