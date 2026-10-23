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
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// fastPathDocuments are stored documents of every type the fast path decodes:
// each is marshaled the way the backends store it and decoded both ways.
func fastPathDocuments() map[string]*types.Document {
	nested := must.NotFail(types.NewDocument("userId", "u1", "isActive", true, "n", int32(3)))
	members := must.NotFail(types.NewArray(nested, must.NotFail(types.NewDocument("userId", "u2", "isActive", false))))

	return map[string]*types.Document{
		"Empty": must.NotFail(types.NewDocument()),
		"Scalars": must.NotFail(types.NewDocument(
			"_id", "abc", "s", "plain", "i", int32(-7), "l", int64(1<<40), "d", 3.25,
			"t", true, "f", false, "n", types.Null, "date", time.UnixMilli(1767225600123).UTC(),
			"ts", types.Timestamp(42), "oid", types.ObjectID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		)),
		"Strings that need json.Unmarshal": must.NotFail(types.NewDocument(
			"quote", `say "hi"`, "backslash", `a\b`, "newline", "line1\nline2", "tab", "a\tb",
			"unicode", "Hyvää päivää ✓", "control", "\x01\x1f", "html", "<a>&</a>", "empty", "",
			"escapedUnicode", "  ",
		)),
		"Doubles": must.NotFail(types.NewDocument(
			"nan", math.NaN(), "inf", math.Inf(1), "ninf", math.Inf(-1), "zero", 0.0, "neg", -1.5e-300,
		)),
		"Nested": must.NotFail(types.NewDocument(
			"_id", "card", "members", members, "empty", must.NotFail(types.NewArray()),
			"doc", must.NotFail(types.NewDocument("a", must.NotFail(types.NewDocument("b", must.NotFail(types.NewArray("x", int32(1), nested)))))),
			"emptyDoc", must.NotFail(types.NewDocument()),
			"arrays", must.NotFail(types.NewArray(must.NotFail(types.NewArray()), must.NotFail(types.NewArray("y")))),
		)),
		"Binary and regex": must.NotFail(types.NewDocument(
			"bin", types.Binary{Subtype: types.BinaryGeneric, B: []byte{0, 1, 2, 255}},
			"re", types.Regex{Pattern: `^a"b\d$`, Options: "i"},
		)),
	}
}

// sameDocument asserts deep equality. A NaN is not DeepEqual to itself, so a
// document holding one is compared by its printed values instead - both
// decoders return the same float64 NaN.
func sameDocument(t *testing.T, want, got *types.Document) {
	t.Helper()

	if fmt.Sprint(want.Map()) == fmt.Sprint(got.Map()) && types.Compare(want, got) == types.Equal &&
		fmt.Sprint(want.Keys()) == fmt.Sprint(got.Keys()) && containsNaN(want) {
		return
	}

	assert.Equal(t, want, got)
}

func containsNaN(doc *types.Document) bool {
	for _, v := range doc.Map() {
		if f, ok := v.(float64); ok && math.IsNaN(f) {
			return true
		}
	}

	return false
}

func TestFastPathMatchesOriginalDecoder(t *testing.T) {
	t.Parallel()

	for name, doc := range fastPathDocuments() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := Marshal(doc)
			require.NoError(t, err)

			fast, err := Unmarshal(data)
			require.NoError(t, err)
			slow, err := unmarshalSlow(data)
			require.NoError(t, err)
			sameDocument(t, slow, fast)
			assert.Equal(t, types.Equal, types.Compare(doc, fast), "the document round-trips")

			fields := doc.Keys()
			if len(fields) > 2 {
				fields = fields[1:3]
			}

			fastFields, err := UnmarshalFields(data, fields)
			require.NoError(t, err)
			slowFields, err := unmarshalFieldsSlow(data, fields)
			require.NoError(t, err)
			sameDocument(t, slowFields, fastFields)
		})
	}
}

// A document that is not in canonical form - other key order, whitespace, an
// escaped key, a duplicated key, truncated or malformed input - must be decoded
// or refused exactly as the original decoder does.
func TestFastPathFallsBack(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"other key order":   `{"$s":{"p":{"a":{"t":"string"},"b":{"t":"int"}},"$k":["a","b"]},"b":2,"a":"x"}`,
		"schema not first":  `{"a":"x","$s":{"p":{"a":{"t":"string"}},"$k":["a"]}}`,
		"whitespace":        "{ \"$s\" : {\"p\":{\"a\":{\"t\":\"string\"}},\"$k\":[\"a\"]} ,\n \"a\" : \"x\" }",
		"escaped key":       `{"$s":{"p":{"a\"b":{"t":"string"}},"$k":["a\"b"]},"a\"b":"x"}`,
		"duplicate key":     `{"$s":{"p":{"a":{"t":"string"}},"$k":["a"]},"a":"x","a":"y"}`,
		"duplicate in $k":   `{"$s":{"p":{"a":{"t":"string"}},"$k":["a","a"]},"a":"x","a":"y"}`,
		"missing value":     `{"$s":{"p":{"a":{"t":"string"},"b":{"t":"int"}},"$k":["a","b"]},"a":"x"}`,
		"extra value":       `{"$s":{"p":{"a":{"t":"string"}},"$k":["a"]},"a":"x","b":1}`,
		"truncated":         `{"$s":{"p":{"a":{"t":"string"}},"$k":["a"]},"a":"x"`,
		"trailing garbage":  `{"$s":{"p":{"a":{"t":"string"}},"$k":["a"]},"a":"x"} x`,
		"bad number":        `{"$s":{"p":{"a":{"t":"int"}},"$k":["a"]},"a":1x}`,
		"leading zero":      `{"$s":{"p":{"a":{"t":"int"}},"$k":["a"]},"a":00}`,
		"plus sign":         `{"$s":{"p":{"a":{"t":"long"}},"$k":["a"]},"a":+1}`,
		"underscore":        `{"$s":{"p":{"a":{"t":"double"}},"$k":["a"]},"a":1_0}`,
		"wrong type":        `{"$s":{"p":{"a":{"t":"int"}},"$k":["a"]},"a":"1"}`,
		"invalid UTF-8":     "{\"$s\":{\"p\":{\"a\":{\"t\":\"string\"}},\"$k\":[\"a\"]},\"a\":\"\xff\xfe\"}",
		"unterminated":      `{"$s":{"p":{"a":{"t":"string"}},"$k":["a"]},"a":"x}`,
		"unknown in schema": `{"$s":{"p":{"a":{"t":"string","zzz":1}},"$k":["a"]},"a":"x"}`,
		"array too short":   `{"$s":{"p":{"a":{"t":"array","i":[{"t":"int"},{"t":"int"}]}},"$k":["a"]},"a":[1]}`,
		"not an object":     `["$s"]`,
		"empty input":       ``,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fast, fastErr := Unmarshal([]byte(data))
			slow, slowErr := unmarshalSlow([]byte(data))
			assert.Equal(t, slowErr == nil, fastErr == nil, "fast: %v, original: %v", fastErr, slowErr)
			assert.Equal(t, slow, fast)

			fastFields, fastErr := UnmarshalFields([]byte(data), []string{"a"})
			slowFields, slowErr := unmarshalFieldsSlow([]byte(data), []string{"a"})
			assert.Equal(t, slowErr == nil, fastErr == nil, "fields fast: %v, original: %v", fastErr, slowErr)
			assert.Equal(t, slowFields, fastFields)
		})
	}
}

func TestValidScalar(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"0", "-0", "7", "-12", "1.5", "1e9", "1E-9", "-0.5e+3", "true", "false", "null"} {
		assert.True(t, validScalar([]byte(in)), in)
	}

	// Negative: what strconv accepts and JSON does not.
	for _, in := range []string{"00", "01", "+1", "1.", ".5", "1e", "1e+", "1_0", "inf", "NaN", "0x10", "--1", "-", "tru", "nul", "1.5.5"} {
		assert.False(t, validScalar([]byte(in)), in)
	}
}

func TestFastString(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{`"abc"`: "abc", `""`: "", `"Hyvää ✓"`: "Hyvää ✓"} {
		got, ok := fastString([]byte(in))
		assert.True(t, ok, in)
		assert.Equal(t, want, got)
	}

	// Negative: anything json.Unmarshal would change or reject is not taken.
	for _, in := range []string{`"a\"b"`, `"a\\b"`, `"a\nb"`, "\"a\x01b\"", "\"\xff\"", `abc`, `"abc`, `"`} {
		_, ok := fastString([]byte(in))
		assert.False(t, ok, in)
	}
}

// FuzzFastPath compares the fast and the original decoder on arbitrary input:
// both succeed with the same document, or both fail.
func FuzzFastPath(f *testing.F) {
	for _, doc := range fastPathDocuments() {
		f.Add(must.NotFail(Marshal(doc)))
	}

	f.Add([]byte(`{"$s":{"p":{"a":{"t":"string"}},"$k":["a"]},"a":"x","a":"y"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fast, fastErr := Unmarshal(data)
		slow, slowErr := unmarshalSlow(data)
		if (fastErr == nil) != (slowErr == nil) {
			t.Fatalf("fast: %v, original: %v", fastErr, slowErr)
		}

		if fastErr != nil {
			return
		}

		// Compared re-encoded: types.Compare panics on an invalid regex, which
		// arbitrary input can hold, and a NaN is not DeepEqual to itself.
		a, aErr := Marshal(fast)
		b, bErr := Marshal(slow)
		if (aErr == nil) != (bErr == nil) || string(a) != string(b) {
			t.Fatalf("fast %s (%v), original %s (%v)", a, aErr, b, bErr)
		}
	})
}
