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
	"errors"
	"fmt"
	"strconv"
)

// Documents stored as SQLite JSONB.
//
// A document column used to hold sjson TEXT, and every ->, ->> and json_each in
// a query - every filter, sort key, index entry and multikey trigger - parsed
// that text again. SQLite's own binary form, JSONB (its "JSONB" documentation page),
// is read without parsing: on 61,507 cards a due cards query took 261 ms
// instead of 713, a filter on one field of every document 155 ms instead of
// 1,494, and the column is a fifth smaller. Asking SQLite to turn JSONB back
// into text for Go, json(), took four times as long as reading the text did,
// so it is turned back here instead: jsonbToText gives the exact bytes json()
// gives - which are the bytes sjson stored, since jsonb() keeps a number's and
// an escaped string's text as written - and sjson decodes them as before.
//
// A column may hold either: the distinct pushdown builds text with
// json_object, and a table not yet converted holds text. Text starts with '{';
// a JSONB object's first byte has type 12 in its low four bits, so it never is.

// JSONB element types, the low four bits of an element's first byte.
const (
	jsonbNull    = 0
	jsonbTrue    = 1
	jsonbFalse   = 2
	jsonbInt     = 3
	jsonbInt5    = 4
	jsonbFloat   = 5
	jsonbFloat5  = 6
	jsonbText    = 7
	jsonbTextJ   = 8
	jsonbText5   = 9
	jsonbTextRaw = 10
	jsonbArray   = 11
	jsonbObject  = 12
)

// errJSONB is a value that is not JSONB jsonbToText can render as JSON.
var errJSONB = errors.New("invalid or unsupported SQLite JSONB")

// storedJSON returns the sjson text of a stored document, which is either text
// or JSONB.
func storedJSON(b []byte) ([]byte, error) {
	if len(b) == 0 || b[0] == '{' || b[0] == ' ' || b[0] == '\n' || b[0] == '\t' || b[0] == '\r' {
		return b, nil
	}

	return jsonbToText(b)
}

// jsonbToText renders JSONB as JSON text, byte for byte as SQLite's json() does.
func jsonbToText(b []byte) ([]byte, error) {
	out := make([]byte, 0, len(b)+len(b)/4)

	out, n, err := appendJSONB(out, b, 0)
	if err != nil {
		return nil, err
	}

	if n != len(b) {
		return nil, fmt.Errorf("%w: %d trailing bytes", errJSONB, len(b)-n)
	}

	return out, nil
}

// jsonbHeader returns an element's type, the offset of its payload and the
// payload's size.
func jsonbHeader(b []byte, at int) (typ byte, payload, size int, err error) {
	if at >= len(b) {
		return 0, 0, 0, fmt.Errorf("%w: truncated at %d", errJSONB, at)
	}

	typ = b[at] & 0x0f
	x := int(b[at] >> 4)

	var extra int

	switch {
	case x <= 11:
		size = x
	case x == 12:
		extra = 1
	case x == 13:
		extra = 2
	case x == 14:
		extra = 4
	default:
		extra = 8
	}

	payload = at + 1 + extra
	if payload > len(b) {
		return 0, 0, 0, fmt.Errorf("%w: truncated header at %d", errJSONB, at)
	}

	for i := 0; i < extra; i++ {
		size = size<<8 | int(b[at+1+i])
	}

	if size < 0 || payload+size > len(b) {
		return 0, 0, 0, fmt.Errorf("%w: element at %d overruns", errJSONB, at)
	}

	return typ, payload, size, nil
}

// appendJSONB appends the element at b[at:] as JSON text and returns where the
// next element starts.
func appendJSONB(out, b []byte, at int) ([]byte, int, error) {
	typ, p, size, err := jsonbHeader(b, at)
	if err != nil {
		return nil, 0, err
	}

	end := p + size
	payload := b[p:end]

	switch typ {
	case jsonbNull:
		out = append(out, "null"...)
	case jsonbTrue:
		out = append(out, "true"...)
	case jsonbFalse:
		out = append(out, "false"...)
	case jsonbInt, jsonbFloat:
		if size == 0 {
			return nil, 0, fmt.Errorf("%w: empty number at %d", errJSONB, at)
		}
		out = append(out, payload...)
	case jsonbText, jsonbTextJ:
		// TEXT needs no escaping; TEXTJ holds the escapes as they were written.
		out = append(out, '"')
		out = append(out, payload...)
		out = append(out, '"')
	case jsonbTextRaw:
		out = appendEscaped(out, payload)
	case jsonbArray:
		out = append(out, '[')
		for i := p; i < end; {
			if i > p {
				out = append(out, ',')
			}
			if out, i, err = appendJSONB(out, b[:end], i); err != nil {
				return nil, 0, err
			}
		}
		out = append(out, ']')
	case jsonbObject:
		out = append(out, '{')
		for i, n := p, 0; i < end; n++ {
			if n%2 == 0 {
				if n > 0 {
					out = append(out, ',')
				}
				if t := b[i] & 0x0f; t < jsonbText || t > jsonbTextRaw {
					return nil, 0, fmt.Errorf("%w: label of type %d at %d", errJSONB, t, i)
				}
			} else {
				out = append(out, ':')
			}
			if out, i, err = appendJSONB(out, b[:end], i); err != nil {
				return nil, 0, err
			}
			if i >= end && n%2 == 0 {
				return nil, 0, fmt.Errorf("%w: label without a value at %d", errJSONB, at)
			}
		}
		out = append(out, '}')
	default:
		// INT5, FLOAT5 and TEXT5 are JSON5, which jsonb() of strict JSON - the
		// only way a document reaches the column - never produces.
		return nil, 0, fmt.Errorf("%w: element type %d at %d", errJSONB, typ, at)
	}

	return out, end, nil
}

// appendEscaped appends text that needs escaping as a JSON string, the way
// SQLite's json() escapes a TEXTRAW element.
func appendEscaped(out, s []byte) []byte {
	out = append(out, '"')

	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			out = append(out, '\\', c)
		case c == '\b':
			out = append(out, `\b`...)
		case c == '\f':
			out = append(out, `\f`...)
		case c == '\n':
			out = append(out, `\n`...)
		case c == '\r':
			out = append(out, `\r`...)
		case c == '\t':
			out = append(out, `\t`...)
		case c < 0x20:
			out = append(out, `\u00`...)
			h := strconv.FormatInt(int64(c), 16)
			if len(h) == 1 {
				out = append(out, '0')
			}
			out = append(out, h...)
		default:
			out = append(out, c)
		}
	}

	return append(out, '"')
}
