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

package mongosh

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// lineWidth is the width a value is printed within on one line, as mongosh
// (Node.js util.inspect with breakLength 80) does.
const lineWidth = 80

// maxDepth is how deep nested values are printed before "[Object]".
const maxDepth = 20

var identifierRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// inspector formats values the way mongosh prints them: single-quoted
// strings, unquoted keys where possible, ObjectId('…'), ISODate('…'),
// Long('…'), short objects on one line and longer ones broken over lines.
type inspector struct {
	s       *Shell
	seen    map[*goja.Object]bool
	deepest int // the deepest object level formatted so far
}

// inspect formats v for printing; a top-level string is printed as it is.
func (s *Shell) inspect(v goja.Value) string {
	if _, isObj := v.(*goja.Object); !isObj && v != nil {
		if str, ok := v.Export().(string); ok {
			return str
		}
	}

	return s.inspectValue(v)
}

// inspectValue formats v, quoting a string.
func (s *Shell) inspectValue(v goja.Value) string {
	in := &inspector{s: s, seen: map[*goja.Object]bool{}}
	return in.format(v, 0, 0)
}

// quote quotes a string as util.inspect does: single quotes unless the
// string contains a single quote and no double quote.
func quote(str string) string {
	q := byte('\'')
	if strings.Contains(str, "'") && !strings.Contains(str, `"`) {
		q = '"'
	}

	var b strings.Builder

	b.WriteByte(q)

	for _, r := range str {
		switch {
		case r == rune(q):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x`)
			b.WriteString(strconv.FormatInt(int64(r)+0x100, 16)[1:])
		default:
			b.WriteRune(r)
		}
	}

	b.WriteByte(q)

	return b.String()
}

// formatNumber formats a JavaScript number, keeping the sign of -0.
func formatNumber(v goja.Value) string {
	if f := v.ToFloat(); f == 0 && math.Signbit(f) {
		return "-0"
	}

	return v.String()
}

func formatDate(ms float64) string {
	if math.IsNaN(ms) {
		return "Invalid Date"
	}

	return "ISODate('" + time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z") + "')"
}

// format formats v at the given indentation and nesting depth.
func (in *inspector) format(v goja.Value, indent, depth int) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}

	if goja.IsNull(v) {
		return "null"
	}

	obj, ok := v.(*goja.Object)
	if !ok {
		switch e := v.Export().(type) {
		case string:
			return quote(e)
		case float64, int64:
			return formatNumber(v)
		default:
			if v.ExportType() != nil && v.ExportType().String() == "*big.Int" {
				return v.String() + "n"
			}

			return v.String()
		}
	}

	switch e := obj.Export().(type) {
	case *dbObject:
		return e.name
	case *collObject:
		return e.db + "." + e.name
	}

	if c := cursorOf(obj); c != nil {
		return in.s.cursorString(c, false)
	}

	if t := obj.Get("_bsontype"); t != nil && !goja.IsUndefined(t) {
		return in.bsonClass(obj, t.String(), indent, depth)
	}

	switch obj.ClassName() {
	case "Function":
		name := obj.Get("name").String()
		if name == "" {
			return "[Function (anonymous)]"
		}

		return "[Function: " + name + "]"
	case "Date":
		return formatDate(obj.ToFloat())
	case "RegExp":
		return obj.String()
	case "Error":
		msg := obj.Get("name").String() + ": " + obj.Get("message").String()
		if depth == 0 {
			return msg
		}

		return "[" + msg + "]"
	case "String", "Number", "Boolean":
		return "[" + obj.ClassName() + ": " + obj.String() + "]"
	}

	if in.seen[obj] {
		return "[Circular]"
	}

	if depth >= maxDepth {
		if obj.ClassName() == "Array" {
			return "[Array]"
		}

		return "[Object]"
	}

	in.seen[obj] = true
	defer delete(in.seen, obj)

	outer := in.deepest
	in.deepest = depth

	var entries []string

	open, closing := "{", "}"
	numeric := false

	if obj.ClassName() == "Array" {
		open, closing = "[", "]"
		n := obj.Get("length").ToInteger()
		numeric = true

		for i := int64(0); i < n && i < 100; i++ {
			e := obj.Get(strconv.FormatInt(i, 10))
			if _, isNum := e.Export().(float64); !isNum {
				if _, isInt := e.Export().(int64); !isInt {
					numeric = false
				}
			}

			entries = append(entries, in.format(e, indent+2, depth+1))
		}

		if n > 100 {
			entries = append(entries, "... "+strconv.FormatInt(n-100, 10)+" more items")
		}

		if len(entries) > 6 {
			entries = groupArrayElements(entries, indent, numeric, n > 100)
		}
	} else {
		for _, k := range obj.Keys() {
			key := k
			if !identifierRe.MatchString(k) {
				key = quote(k)
			}

			entries = append(entries, key+": "+in.format(obj.Get(k), indent+2, depth+1))
		}
	}

	levels := in.deepest - depth
	in.deepest = max(outer, in.deepest)

	return wrap(open, closing, entries, indent, levels < 3)
}

// wrap puts entries on one line when they fit (as Node.js util.inspect
// does with breakLength 80 and compact 3), or one per line.
func wrap(open, closing string, entries []string, indent int, combine bool) string {
	if len(entries) == 0 {
		return open + closing
	}

	if combine {
		total := len(entries) + len(entries) + indent + len(open) + 10
		fits := total <= lineWidth

		for _, e := range entries {
			total += len(e)
			if total > lineWidth || strings.Contains(e, "\n") {
				fits = false
				break
			}
		}

		if fits {
			return open + " " + strings.Join(entries, ", ") + " " + closing
		}
	}

	pad := "\n" + strings.Repeat(" ", indent+2)

	return open + pad + strings.Join(entries, ","+pad) + "\n" + strings.Repeat(" ", indent) + closing
}

// groupArrayElements arranges the entries of a long array of short values
// in columns, as Node.js util.inspect does.
func groupArrayElements(output []string, indent int, numeric, more bool) []string {
	outputLength := len(output)
	if more {
		outputLength--
	}

	const separatorSpace = 2

	dataLen := make([]int, outputLength)
	totalLength, maxLength := 0, 0

	for i := range outputLength {
		if strings.Contains(output[i], "\n") {
			return output
		}

		l := utf8.RuneCountInString(output[i])
		dataLen[i] = l
		totalLength += l + separatorSpace
		maxLength = max(maxLength, l)
	}

	actualMax := maxLength + separatorSpace

	if actualMax*3+indent >= lineWidth || (float64(totalLength)/float64(actualMax) <= 5 && maxLength > 6) {
		return output
	}

	averageBias := math.Sqrt(float64(actualMax) - float64(totalLength)/float64(len(output)))
	biasedMax := math.Max(float64(actualMax)-3-averageBias, 1)
	columns := min(
		int(math.Round(math.Sqrt(2.5*biasedMax*float64(outputLength))/biasedMax)),
		(lineWidth-indent)/actualMax,
		3*4,
		15,
	)

	if columns <= 1 {
		return output
	}

	maxLineLength := make([]int, 0, columns)

	for i := range columns {
		lineLength := 0
		for j := i; j < outputLength; j += columns {
			lineLength = max(lineLength, dataLen[j])
		}

		maxLineLength = append(maxLineLength, lineLength+separatorSpace)
	}

	pad := func(s string, width int, start bool) string {
		n := width - utf8.RuneCountInString(s)
		if n <= 0 {
			return s
		}

		if start {
			return strings.Repeat(" ", n) + s
		}

		return s + strings.Repeat(" ", n)
	}

	var tmp []string

	for i := 0; i < outputLength; i += columns {
		end := min(i+columns, outputLength)

		var b strings.Builder

		j := i
		for ; j < end-1; j++ {
			b.WriteString(pad(output[j]+", ", maxLineLength[j-i], numeric))
		}

		if numeric {
			b.WriteString(pad(output[j], maxLineLength[j-i]-separatorSpace, true))
		} else {
			b.WriteString(output[j])
		}

		tmp = append(tmp, b.String())
	}

	if more {
		tmp = append(tmp, output[len(output)-1])
	}

	return tmp
}

// bsonClass formats an instance of one of the prelude.js classes.
func (in *inspector) bsonClass(obj *goja.Object, t string, indent, depth int) string {
	str := func(name string) string { return obj.Get(name).String() }

	switch t {
	case "ObjectId":
		return "ObjectId('" + str("__id") + "')"
	case "Long":
		return "Long('" + str("__v") + "')"
	case "Int32":
		return "Int32(" + str("__v") + ")"
	case "Double":
		f := obj.Get("__v").ToFloat()
		s := formatNumber(obj.Get("__v"))

		if f == math.Trunc(f) && !math.IsInf(f, 0) && !strings.ContainsAny(s, "e") {
			s += ".0"
		}

		return "Double(" + s + ")"
	case "Decimal128":
		return "Decimal128('" + str("__v") + "')"
	case "Binary":
		sub := obj.Get("sub_type").ToInteger()
		if sub == 4 && obj.Get("length").Export() != nil {
			if f, ok := goja.AssertFunction(obj.Get("toString")); ok {
				if u, err := f(obj); err == nil && len(u.String()) == 36 {
					return "UUID('" + u.String() + "')"
				}
			}
		}

		return "Binary.createFromBase64('" + str("__b64") + "', " + strconv.FormatInt(sub, 10) + ")"
	case "Timestamp":
		return "Timestamp({ t: " + str("t") + ", i: " + str("i") + " })"
	case "MinKey":
		return "MinKey()"
	case "MaxKey":
		return "MaxKey()"
	case "BSONRegExp":
		p, o := str("pattern"), str("options")
		if _, err := regexp.Compile(p); err == nil && !strings.ContainsAny(o, "lx") {
			return "/" + strings.ReplaceAll(p, "/", `\/`) + "/" + o
		}

		return "BSONRegExp(" + quote(p) + ", " + quote(o) + ")"
	case "Code":
		sc := obj.Get("scope")
		if sc == nil || goja.IsNull(sc) || goja.IsUndefined(sc) {
			return "Code(" + quote(str("code")) + ")"
		}

		return "Code(" + quote(str("code")) + ", " + in.format(sc, indent, depth+1) + ")"
	}

	return "[" + t + "]"
}
