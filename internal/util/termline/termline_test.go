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

package termline

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlainLines(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	r := New(strings.NewReader("one\r\ntwo\nlast"), &out)
	assert.False(t, r.Terminal())
	assert.False(t, IsTerminal(strings.NewReader("")))

	for _, want := range []string{"one", "two", "last"} {
		line, err := r.ReadLine("> ")
		require.NoError(t, err)
		assert.Equal(t, want, line)
	}

	_, err := r.ReadLine("> ")
	assert.ErrorIs(t, err, io.EOF)
	assert.Empty(t, out.String(), "no prompt without a terminal")
}

func TestCtrlC(t *testing.T) {
	t.Parallel()

	c := &ctrlCReader{r: strings.NewReader("ab\x03cd")}

	b, err := io.ReadAll(c)
	require.NoError(t, err)
	assert.Equal(t, "ab\x05\x15\rcd", string(b), "Ctrl-C becomes end of line, erase line, Enter")
	assert.True(t, c.saw)

	c = &ctrlCReader{r: strings.NewReader("plain\r")}
	_, err = io.ReadAll(c)
	require.NoError(t, err)
	assert.False(t, c.saw, "negative: no Ctrl-C, no interrupt")
}
