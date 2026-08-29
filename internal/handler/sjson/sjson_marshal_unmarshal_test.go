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

package sjson

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

func TestMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		doc  *types.Document
		json string
	}{
		"Empty": {
			json: `{"$s":{}}`,
			doc:  must.NotFail(types.NewDocument()),
		},
		"Filled": {
			json: `{
			"$s": {
				"p": {"foo": {"t": "string"}},
				"$k": ["foo"]
			},
			"foo": "bar"
		}`,
			doc: must.NotFail(types.NewDocument(
				"foo", "bar",
			)),
		},
	} {
		tc := tc

		t.Run(name, func(t *testing.T) {
			doc, err := Unmarshal([]byte(tc.json))
			require.NoError(t, err)
			assert.Equal(t, tc.doc, doc)

			actualB, err := Marshal(tc.doc)
			require.NoError(t, err)
			actualB = testutil.IndentJSON(t, actualB)

			expectedB := testutil.IndentJSON(t, []byte(tc.json))
			assert.Equal(t, string(expectedB), string(actualB))
		})
	}
}

func TestUnmarshalFields(t *testing.T) {
	t.Parallel()

	nested := must.NotFail(types.NewDocument(
		"members", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("userId", "u1", "active", true)),
		)),
	))
	doc := must.NotFail(types.NewDocument(
		"title", "large board",
		"_id", types.ObjectID{1, 2, 3},
		"archived", false,
		"nested", nested,
	))
	b, err := Marshal(doc)
	require.NoError(t, err)

	actual, err := UnmarshalFields(b, []string{"archived", "_id", "missing"})
	require.NoError(t, err)
	assert.Equal(t, must.NotFail(types.NewDocument(
		"_id", types.ObjectID{1, 2, 3},
		"archived", false,
	)), actual, "selected fields retain stored order and missing fields remain missing")
	assert.False(t, actual.Has("nested"), "unobservable nested values must not be recursively decoded")
}

func TestDecodeSchemaCache(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"p":{"_id":{"t":"string"},"archived":{"t":"bool"}},"$k":["_id","archived"]}`)
	first, err := decodeSchema(raw)
	require.NoError(t, err)
	second, err := decodeSchema(raw)
	require.NoError(t, err)
	assert.Same(t, first, second, "a repeated bounded schema must reuse its immutable parse")

	_, err = decodeSchema([]byte(`{"unknown":true}`))
	require.ErrorContains(t, err, `unknown field "unknown"`, "validation must still run before a schema can be cached")
}

func TestSchemaCacheLRU(t *testing.T) {
	t.Parallel()

	cache := schemaCache{m: make(map[[sha256.Size]byte]*list.Element)}
	oneKey := sha256.Sum256([]byte("one"))
	twoKey := sha256.Sum256([]byte("two"))
	threeKey := sha256.Sum256([]byte("three"))
	one := new(schema)
	two := new(schema)
	three := new(schema)

	assert.Same(t, one, cache.add(oneKey, one, 2))
	assert.Same(t, two, cache.add(twoKey, two, 2))
	assert.Same(t, one, cache.get(oneKey), "a hit refreshes recency")
	assert.Same(t, three, cache.add(threeKey, three, 2))
	assert.Nil(t, cache.get(twoKey), "the least recently used schema is evicted")
	assert.Same(t, one, cache.add(oneKey, new(schema), 2), "an existing immutable schema wins an insertion race")
	assert.Len(t, cache.m, 2, "the configured capacity is a hard bound")
}

func BenchmarkUnmarshalRepeatedSchema(b *testing.B) {
	members := must.NotFail(types.NewArray())
	for i := 0; i < 20; i++ {
		members.Append(must.NotFail(types.NewDocument("userId", "user", "active", true, "index", int64(i))))
	}
	doc := must.NotFail(types.NewDocument(
		"_id", "card", "boardId", "board", "archived", false,
		"title", "Card title", "members", members,
	))
	raw, err := Marshal(doc)
	require.NoError(b, err)

	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Unmarshal(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalWideDocument(b *testing.B) {
	pairs := make([]any, 0, 80)
	for i := 0; i < 40; i++ {
		pairs = append(pairs, fmt.Sprintf("field%02d", i), int64(i))
	}
	doc := must.NotFail(types.NewDocument(pairs...))
	raw := must.NotFail(Marshal(doc))

	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Unmarshal(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func TestUnmarshalScalarFastPathRejectsWrongTypes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		typeName elemType
		value    string
	}{
		"BoolString":        {elemTypeBool, `"true"`},
		"FractionalInt":     {elemTypeInt, `1.5`},
		"NegativeTimestamp": {elemTypeTimestamp, `-1`},
		"StringDate":        {elemTypeDate, `"1"`},
		"BoolDouble":        {elemTypeDouble, `true`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := unmarshalSingleValue(json.RawMessage(tc.value), &elem{Type: tc.typeName})
			require.Error(t, err)
		})
	}
}

// TestUnmarshalInvalid checks that in case of invalid data, we return errors and not just ignore issues.
func TestUnmarshalInvalid(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		json     string
		expected string
	}{
		"NoData": {
			json:     `{"$s":{"p": {"foo": {"t": "string"}},"$k": ["foo"]}}`,
			expected: `the data must have the same number of schema keys and document fields`,
		},
		"InvalidData": {
			json:     `"foo"`,
			expected: `json: cannot unmarshal string into Go value of type map`,
		},
		"ExtraData": {
			json:     `{"$s":{"p": {"foo": {"t": "string"}},"$k": ["foo"]}, "foo": "bar"}foo`,
			expected: `invalid character 'f' after top-level value`,
		},
		"NoSchema": {
			json:     `{"foo": "bar"}`,
			expected: `schema is not set`,
		},
		"NoDataNoSchema": {
			json:     `{}`,
			expected: `schema is not set`,
		},
		"EmptySchema": {
			json:     `{"$s":{"p":{}, "$k": []}, "foo": "bar"}`,
			expected: `the data must have the same number of schema keys and document fields`,
		},
		"ExtraFieldInSchema": {
			json: `{
				"$s": {
					"p": {"foo": {"t": "string"}},
					"$k": ["foo"],
					"unknown": "field"
				},
				"foo": "bar"
			}`,
			expected: `json: unknown field "unknown"`,
		},
		"ExtraFieldInDoc": {
			json: `{
				"$s": {
					"p": {"foo": {"t": "string"}},
					"$k": ["foo"]
				},
				"foo": "bar",
				"fizz": "buzz"
			}`,
			expected: `the data must have the same number of schema keys and document fields`,
		},
		"MixedUpKeys": {
			json: `{
				"$s": {
					"p": {"foo": {"t": "string"}},
					"$k": ["foo"]
				},
				"fizz": "buzz"
			}`,
			expected: `missing key "foo"`,
		},
	} {
		tc := tc

		t.Run(name, func(t *testing.T) {
			doc, err := Unmarshal([]byte(tc.json))
			require.NotNil(t, err)
			require.Contains(t, err.Error(), tc.expected)
			require.Nil(t, doc)
		})
	}
}
