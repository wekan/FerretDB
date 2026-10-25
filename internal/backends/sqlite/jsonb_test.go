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

package sqlite

import (
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func openMemory(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", "file::memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

// sqliteJSONB returns what SQLite's jsonb() and json(jsonb()) give for text.
func sqliteJSONB(t *testing.T, db *sql.DB, text string) ([]byte, string) {
	t.Helper()

	var b []byte
	var back string
	require.NoError(t, db.QueryRow(`SELECT jsonb(?), json(jsonb(?))`, text, text).Scan(&b, &back), text)

	return b, back
}

func TestJSONBToTextMatchesSQLite(t *testing.T) {
	t.Parallel()

	db := openMemory(t)

	long := func(n int) string { return strings.Repeat("x", n) }
	inputs := []string{
		`{}`, `[]`, `{"a":null}`, `{"a":true,"b":false}`, `[1,-1,0,-0,1.5,-2.25e-10,1e+300,1E5,123456789012345678]`,
		`{"s":"plain","e":"a\"b\\c\/d\b\f\n\r\t","u":"<>&","x":"ä ","emoji":"😀"}`,
		`{"raw":"äö","nested":{"a":[{"b":[[],{},[{}]]}]}}`,
		`{"k":"` + long(11) + `","k12":"` + long(12) + `","k255":"` + long(255) + `","k256":"` + long(256) + `"}`,
		`{"k":"` + long(70000) + `"}`,
		`{"$s":{"p":{"_id":{"t":"string"},"n":{"t":"int"}},"$k":["_id","n"]},"_id":"a","n":5}`,
		`{"":"empty label","a b":"space","é":"label"}`,
	}

	// Large arrays and objects, so a container's own size needs 2 and 4 bytes.
	var big []string
	for i := 0; i < 5000; i++ {
		big = append(big, fmt.Sprintf(`{"i":%d,"s":"v%d"}`, i, i))
	}
	inputs = append(inputs, `[`+strings.Join(big, ",")+`]`)

	for _, text := range inputs {
		b, back := sqliteJSONB(t, db, text)
		got, err := jsonbToText(b)
		require.NoError(t, err, text[:min(len(text), 80)])
		assert.Equal(t, back, string(got), "the same as SQLite's json()")
		assert.Equal(t, text, string(got), "the same as the text stored")
	}

	// TEXTRAW: a string from an SQL value, which needs escaping on output.
	for _, s := range []string{"a\"b", "back\\slash", "ctl\x01\x1f", "nl\nt\t", "plain", "äö"} {
		var b []byte
		var back string
		require.NoError(t, db.QueryRow(`SELECT jsonb_object('k', ?), json(jsonb_object('k', ?))`, s, s).Scan(&b, &back))
		got, err := jsonbToText(b)
		require.NoError(t, err)
		assert.Equal(t, back, string(got), "%q", s)
	}
}

func TestJSONBToTextRoundTripsSjson(t *testing.T) {
	// Every document sjson stores comes back as the same bytes - the canonical
	// form sjson's fast decoder expects - and decodes to the same document.
	t.Parallel()

	db := openMemory(t)
	r := rand.New(rand.NewSource(1))
	date := time.Date(2026, 10, 25, 1, 2, 3, 4_000_000, time.UTC)
	values := []any{
		"plain", "with \"quote\" and \\", "<html>&", "äö 😀", "", " ",
		int32(-7), int64(1 << 40), 1.5, -0.0, math.MaxFloat64, math.SmallestNonzeroFloat64,
		true, false, types.Null, types.ObjectID{1, 2, 3}, date,
		types.Binary{B: []byte{0, 1, 2}, Subtype: types.BinaryGeneric},
		types.Regex{Pattern: "^a.*", Options: "i"}, types.Timestamp(42),
	}

	var doc func(depth int) *types.Document
	doc = func(depth int) *types.Document {
		d := must.NotFail(types.NewDocument("_id", fmt.Sprintf("id%d", r.Int())))
		for i, n := 0, r.Intn(8); i < n; i++ {
			key := fmt.Sprintf("k%d", i)
			switch {
			case depth < 3 && r.Intn(5) == 0:
				d.Set(key, doc(depth+1))
			case depth < 3 && r.Intn(5) == 0:
				arr := must.NotFail(types.NewArray())
				for j, m := 0, r.Intn(4); j < m; j++ {
					arr.Append(values[r.Intn(len(values))])
				}
				d.Set(key, arr)
			default:
				d.Set(key, values[r.Intn(len(values))])
			}
		}
		return d
	}

	for i := 0; i < 2000; i++ {
		d := doc(0)
		text := must.NotFail(sjson.Marshal(d))
		b, _ := sqliteJSONB(t, db, string(text))
		got, err := storedJSON(b)
		require.NoError(t, err)
		require.Equal(t, string(text), string(got))
		back, err := sjson.Unmarshal(got)
		require.NoError(t, err)
		require.Equal(t, must.NotFail(sjson.Marshal(back)), text)
	}
}

func TestStoredJSONTextAndErrors(t *testing.T) {
	t.Parallel()

	// Text - an unconverted table, or json_object of the distinct pushdown - is
	// returned as it is.
	got, err := storedJSON([]byte(`{"a":1}`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(got))

	// Negative: damaged JSONB is an error, never wrong text.
	db := openMemory(t)
	b, _ := sqliteJSONB(t, db, `{"a":[1,2,{"b":"c"}],"d":"e"}`)
	for name, bad := range map[string][]byte{
		"truncated":      b[:len(b)-1],
		"trailing bytes": append(append([]byte{}, b...), 0x00),
		"reserved type":  {0x0e},
		"JSON5 int":      {0x14, '1'},
		"label a number": {0x2c, 0x13, '1'},
		"size overruns":  {0xcc, 0xff, 0x00},
		"empty number":   {0x03},
	} {
		_, err := storedJSON(bad)
		assert.ErrorIs(t, err, errJSONB, name)
	}
}
