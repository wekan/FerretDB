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
	"bytes"
	"unicode/utf8"
)

// The fast decoding path.
//
// Decoding a stored document used to start with json.Unmarshal into a
// map[string]json.RawMessage - reflection, a map, a copy of every value - and
// then json.Unmarshal again for every string and every nested document and
// array. A query returning many documents spent most of its time there.
//
// The scanner below only finds where each top-level value of an object or
// array begins and ends. The values are then decoded by the same
// unmarshalSingleValue as before, which validates every scalar it decodes and
// hands composite values back to this path or to json.Unmarshal.
//
// It is used ONLY for the canonical form Marshal writes - the object's keys in
// schema order, each exactly once, no escapes in a key. Anything else, and any
// syntax the scanner does not expect, returns ok=false and the caller takes the
// original encoding/json path, so a malformed or unusual document is decoded -
// or rejected, with the same error - exactly as before.

// rawField is one top-level member of a JSON object.
type rawField struct {
	key   string
	value []byte
}

// isSpace reports JSON insignificant whitespace.
func isSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t'
}

func skipSpace(data []byte, i int) int {
	for i < len(data) && isSpace(data[i]) {
		i++
	}

	return i
}

// scanString returns the index just past the string starting at data[i] (a
// '"'), and whether it contains a backslash escape.
func scanString(data []byte, i int) (int, bool, bool) {
	escaped := false

	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '\\':
			escaped = true
			j++
		case '"':
			return j + 1, escaped, true
		}
	}

	return 0, false, false
}

// scanValue returns the index just past the JSON value starting at data[i].
// Strings are skipped with their escapes and containers are matched; the
// contents are not validated here, the decoder of that value does it.
func scanValue(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return 0, false
	}

	switch data[i] {
	case '"':
		end, _, ok := scanString(data, i)
		return end, ok

	case '{', '[':
		depth := 0

		for j := i; j < len(data); j++ {
			switch data[j] {
			case '"':
				end, _, ok := scanString(data, j)
				if !ok {
					return 0, false
				}

				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, true
				}
			}
		}

		return 0, false

	default:
		// A number, true, false or null: up to the next delimiter, and it must
		// be exactly one of them. strconv accepts more than JSON does ("00",
		// "+1", "1_0", "inf"), and the outer json.Unmarshal of the original path
		// is what used to refuse those, so the scanner has to.
		j := i
		for j < len(data) && data[j] != ',' && data[j] != '}' && data[j] != ']' && !isSpace(data[j]) {
			j++
		}

		if j == i || !validScalar(data[i:j]) {
			return 0, false
		}

		return j, true
	}
}

// validScalar reports whether b is a JSON true, false, null or number, by the
// grammar of RFC 8259: -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func validScalar(b []byte) bool {
	switch string(b) {
	case "true", "false", "null":
		return true
	}

	i := 0
	if i < len(b) && b[i] == '-' {
		i++
	}

	digits := func() int {
		start := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}

		return i - start
	}

	switch {
	case i < len(b) && b[i] == '0':
		i++
	case i < len(b) && b[i] >= '1' && b[i] <= '9':
		digits()
	default:
		return false
	}

	if i < len(b) && b[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}

	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}

		if digits() == 0 {
			return false
		}
	}

	return i == len(b)
}

// scanObject splits a JSON object into its top-level members, in order.
func scanObject(data []byte) ([]rawField, bool) {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return nil, false
	}

	fields := make([]rawField, 0, 32)

	i = skipSpace(data, i+1)
	if i < len(data) && data[i] == '}' {
		return fields, skipSpace(data, i+1) == len(data)
	}

	for {
		if i >= len(data) || data[i] != '"' {
			return nil, false
		}

		end, escaped, ok := scanString(data, i)
		if !ok || escaped {
			return nil, false
		}

		key := string(data[i+1 : end-1])

		i = skipSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return nil, false
		}

		i = skipSpace(data, i+1)

		valueEnd, ok := scanValue(data, i)
		if !ok {
			return nil, false
		}

		fields = append(fields, rawField{key: key, value: data[i:valueEnd]})

		i = skipSpace(data, valueEnd)
		if i >= len(data) {
			return nil, false
		}

		switch data[i] {
		case ',':
			i = skipSpace(data, i+1)
		case '}':
			return fields, skipSpace(data, i+1) == len(data)
		default:
			return nil, false
		}
	}
}

// scanArray splits a JSON array into its elements, in order.
func scanArray(data []byte) ([][]byte, bool) {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '[' {
		return nil, false
	}

	var elems [][]byte

	i = skipSpace(data, i+1)
	if i < len(data) && data[i] == ']' {
		return elems, skipSpace(data, i+1) == len(data)
	}

	for {
		end, ok := scanValue(data, i)
		if !ok {
			return nil, false
		}

		elems = append(elems, data[i:end])

		i = skipSpace(data, end)
		if i >= len(data) {
			return nil, false
		}

		switch data[i] {
		case ',':
			i = skipSpace(data, i+1)
		case ']':
			return elems, skipSpace(data, i+1) == len(data)
		default:
			return nil, false
		}
	}
}

// fastString decodes a JSON string that has no escape and no control
// character, and is valid UTF-8 - for such a string, what json.Unmarshal
// returns is exactly the bytes between the quotes. Anything else returns
// ok=false and is left to json.Unmarshal.
func fastString(data []byte) (string, bool) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return "", false
	}

	body := data[1 : len(data)-1]
	if bytes.IndexByte(body, '\\') >= 0 || bytes.IndexByte(body, '"') >= 0 {
		return "", false
	}

	for _, c := range body {
		if c < 0x20 {
			return "", false
		}
	}

	if !utf8.Valid(body) {
		return "", false
	}

	return string(body), true
}

// inSchemaOrder reports whether fields (from offset) are exactly sch.Keys, in
// order, each once - the form Marshal writes. A schema listing a key twice has
// fewer properties than keys and is left to the original path, which reports it.
func inSchemaOrder(fields []rawField, offset int, sch *schema) bool {
	if len(fields)-offset != len(sch.Keys) || len(sch.Properties) != len(sch.Keys) {
		return false
	}

	for i, key := range sch.Keys {
		if fields[offset+i].key != key {
			return false
		}
	}

	return true
}
