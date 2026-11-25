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
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// skipValue is returned by toBSON for values a document does not store
// (undefined and functions), as the JavaScript driver ignores them.
type skipValue struct{}

// toBSON converts a JavaScript value to a value the Go driver marshals the
// way the JavaScript driver would: an integral number that fits int32 is an
// int32, any other number a double, an object a bson.D in property order.
func (s *Shell) toBSON(v goja.Value) any {
	return s.toBSONDepth(v, 0)
}

func (s *Shell) toBSONDepth(v goja.Value, depth int) any {
	if depth > 150 {
		s.throw("MongoshInvalidInputError", "value is nested too deeply (or is circular)")
	}

	if v == nil || goja.IsUndefined(v) {
		return skipValue{}
	}

	if goja.IsNull(v) {
		return nil
	}

	obj, ok := v.(*goja.Object)
	if !ok {
		switch e := v.Export().(type) {
		case int64:
			if e >= math.MinInt32 && e <= math.MaxInt32 {
				return int32(e)
			}

			return float64(e)
		case float64:
			if e == math.Trunc(e) && e >= math.MinInt32 && e <= math.MaxInt32 && !(e == 0 && math.Signbit(e)) {
				return int32(e)
			}

			return e
		case *big.Int:
			if e.IsInt64() {
				return e.Int64()
			}

			s.throw("MongoshInvalidInputError", "BigInt "+e.String()+" does not fit a 64-bit integer")
		case string, bool:
			return e
		default:
			return v.String()
		}
	}

	if t := obj.Get("_bsontype"); t != nil && !goja.IsUndefined(t) {
		return s.bsonClassToBSON(obj, t.String(), depth)
	}

	switch obj.ClassName() {
	case "Function":
		return skipValue{}
	case "Date":
		if t, ok := obj.Export().(time.Time); ok {
			return primitive.NewDateTimeFromTime(t)
		}

		return primitive.DateTime(int64(obj.ToFloat()))
	case "RegExp":
		var opts []byte

		for _, f := range obj.Get("flags").String() {
			if strings.ContainsRune("imsux", f) {
				opts = append(opts, byte(f))
			}
		}

		return primitive.Regex{Pattern: obj.Get("source").String(), Options: string(opts)}
	case "Array":
		n := int(obj.Get("length").ToInteger())
		a := make(bson.A, 0, n)

		for i := range n {
			e := s.toBSONDepth(obj.Get(strconv.Itoa(i)), depth+1)
			if _, skip := e.(skipValue); skip {
				e = nil
			}

			a = append(a, e)
		}

		return a
	}

	switch e := obj.Export().(type) {
	case *dbObject:
		return e.name
	case *collObject:
		return e.name
	}

	d := bson.D{}

	for _, k := range obj.Keys() {
		e := s.toBSONDepth(obj.Get(k), depth+1)
		if _, skip := e.(skipValue); skip {
			continue
		}

		d = append(d, bson.E{Key: k, Value: e})
	}

	return d
}

// bsonClassToBSON converts an instance of one of the prelude.js classes.
func (s *Shell) bsonClassToBSON(obj *goja.Object, t string, depth int) any {
	str := func(name string) string { return obj.Get(name).String() }

	switch t {
	case "ObjectId":
		id, err := primitive.ObjectIDFromHex(str("__id"))
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		return id
	case "Long":
		n, err := strconv.ParseInt(str("__v"), 10, 64)
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		return n
	case "Int32":
		return int32(obj.Get("__v").ToInteger())
	case "Double":
		return obj.Get("__v").ToFloat()
	case "Decimal128":
		d, err := primitive.ParseDecimal128(str("__v"))
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		return d
	case "Binary":
		b, err := base64.StdEncoding.DecodeString(str("__b64"))
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		return primitive.Binary{Subtype: byte(obj.Get("sub_type").ToInteger()), Data: b}
	case "Timestamp":
		return primitive.Timestamp{T: uint32(obj.Get("t").ToInteger()), I: uint32(obj.Get("i").ToInteger())}
	case "MinKey":
		return primitive.MinKey{}
	case "MaxKey":
		return primitive.MaxKey{}
	case "BSONRegExp":
		return primitive.Regex{Pattern: str("pattern"), Options: str("options")}
	case "Code":
		if sc := obj.Get("scope"); sc != nil && !goja.IsNull(sc) && !goja.IsUndefined(sc) {
			return primitive.CodeWithScope{Code: primitive.JavaScript(str("code")), Scope: s.toBSONDepth(sc, depth+1)}
		}

		return primitive.JavaScript(str("code"))
	}

	s.throw("BSONError", "unsupported BSON type "+t)

	return nil
}

// toDoc converts a JavaScript value that must be a document (a filter, an
// update, a command); undefined and null are an empty document.
func (s *Shell) toDoc(v goja.Value, what string) bson.D {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return bson.D{}
	}

	d, ok := s.toBSON(v).(bson.D)
	if !ok {
		s.throw("MongoshInvalidInputError", what+" must be an object")
	}

	return d
}

// toDocOrPipeline converts an update: a document, or an aggregation pipeline.
func (s *Shell) toDocOrPipeline(v goja.Value, what string) any {
	switch u := s.toBSON(v).(type) {
	case bson.D, bson.A:
		return u
	default:
		s.throw("MongoshInvalidInputError", what+" must be an object or an array")
		return nil
	}
}

// construct calls one of the prelude.js constructors.
func (s *Shell) construct(name string, args ...any) goja.Value {
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = s.vm.ToValue(a)
	}

	o, err := s.vm.New(s.vm.Get(name), vals...)
	if err != nil {
		panic(err)
	}

	return o
}

// fromRaw converts a BSON document to a JavaScript object.
func (s *Shell) fromRaw(raw bson.Raw) goja.Value {
	obj := s.vm.NewObject()

	elems, err := raw.Elements()
	if err != nil {
		s.throw("BSONError", err.Error())
	}

	for _, e := range elems {
		_ = obj.Set(e.Key(), s.fromRawValue(e.Value()))
	}

	return obj
}

// fromRawValue converts one BSON value to JavaScript, as the shell shows it:
// int32 and double are numbers, int64 a Long, a date a Date.
func (s *Shell) fromRawValue(v bson.RawValue) goja.Value {
	switch v.Type {
	case bson.TypeDouble:
		return s.vm.ToValue(v.Double())
	case bson.TypeString:
		return s.vm.ToValue(v.StringValue())
	case bson.TypeEmbeddedDocument:
		return s.fromRaw(v.Document())
	case bson.TypeArray:
		vals, err := v.Array().Values()
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		items := make([]any, len(vals))
		for i, e := range vals {
			items[i] = s.fromRawValue(e)
		}

		return s.vm.NewArray(items...)
	case bson.TypeBinary:
		sub, data := v.Binary()
		return s.construct("Binary", base64.StdEncoding.EncodeToString(data), int(sub))
	case bson.TypeUndefined:
		return goja.Undefined()
	case bson.TypeObjectID:
		return s.construct("ObjectId", v.ObjectID().Hex())
	case bson.TypeBoolean:
		return s.vm.ToValue(v.Boolean())
	case bson.TypeDateTime:
		return s.date(v.DateTime())
	case bson.TypeNull:
		return goja.Null()
	case bson.TypeRegex:
		p, o := v.Regex()
		return s.construct("BSONRegExp", p, o)
	case bson.TypeDBPointer:
		ns, id := v.DBPointer()
		obj := s.vm.NewObject()
		_ = obj.Set("$ref", ns)
		_ = obj.Set("$id", s.construct("ObjectId", id.Hex()))

		return obj
	case bson.TypeJavaScript:
		return s.construct("Code", v.JavaScript())
	case bson.TypeSymbol:
		return s.vm.ToValue(v.Symbol())
	case bson.TypeCodeWithScope:
		c, sc := v.CodeWithScope()
		return s.construct("Code", c, s.fromRaw(sc))
	case bson.TypeInt32:
		return s.vm.ToValue(v.Int32())
	case bson.TypeTimestamp:
		t, i := v.Timestamp()
		return s.construct("Timestamp", t, i)
	case bson.TypeInt64:
		return s.construct("Long", strconv.FormatInt(v.Int64(), 10))
	case bson.TypeDecimal128:
		return s.construct("Decimal128", v.Decimal128().String())
	case bson.TypeMinKey:
		return s.construct("MinKey")
	case bson.TypeMaxKey:
		return s.construct("MaxKey")
	default:
		return goja.Undefined()
	}
}

// fromGo converts a value the Go driver returned (an ID, a count) to JavaScript.
func (s *Shell) fromGo(v any) goja.Value {
	if v == nil {
		return goja.Null()
	}

	t, data, err := bson.MarshalValue(v)
	if err != nil {
		return s.vm.ToValue(fmt.Sprint(v))
	}

	return s.fromRawValue(bson.RawValue{Type: t, Value: data})
}

// date returns a JavaScript Date for milliseconds since the epoch.
func (s *Shell) date(ms int64) goja.Value {
	d, err := s.vm.New(s.vm.Get("Date"), s.vm.ToValue(ms))
	if err != nil {
		panic(err)
	}

	return d
}

// native returns the Go helpers prelude.js uses.
func (s *Shell) native() *goja.Object {
	n := s.vm.NewObject()
	set := func(name string, f func(goja.FunctionCall) goja.Value) { _ = n.Set(name, f) }
	arg := func(c goja.FunctionCall) string { return c.Argument(0).String() }

	set("oidNew", func(goja.FunctionCall) goja.Value { return s.vm.ToValue(primitive.NewObjectID().Hex()) })
	set("oidFromTime", func(c goja.FunctionCall) goja.Value {
		return s.vm.ToValue(primitive.NewObjectIDFromTimestamp(time.Unix(c.Argument(0).ToInteger(), 0)).Hex())
	})
	set("oidCheck", func(c goja.FunctionCall) goja.Value {
		id, err := primitive.ObjectIDFromHex(arg(c))
		if err != nil {
			s.throw("BSONError", "Argument passed in must be a string of 24 hex characters, got "+strconv.Quote(arg(c)))
		}

		return s.vm.ToValue(id.Hex())
	})
	set("longCheck", func(c goja.FunctionCall) goja.Value {
		str := arg(c)
		if n, err := strconv.ParseInt(str, 10, 64); err == nil {
			return s.vm.ToValue(strconv.FormatInt(n, 10))
		}

		if f, err := strconv.ParseFloat(str, 64); err == nil && f >= math.MinInt64 && f < math.MaxInt64 {
			return s.vm.ToValue(strconv.FormatInt(int64(f), 10))
		}

		s.throw("BSONError", "invalid 64-bit integer "+strconv.Quote(str))

		return nil
	})
	set("int32Check", func(c goja.FunctionCall) goja.Value {
		f := c.Argument(0).ToFloat()
		if math.IsNaN(f) || f < math.MinInt32 || f > math.MaxInt32 {
			s.throw("BSONError", "invalid 32-bit integer "+c.Argument(0).String())
		}

		return s.vm.ToValue(int32(f))
	})
	set("uint32Check", func(c goja.FunctionCall) goja.Value {
		f := c.Argument(0).ToFloat()
		if math.IsNaN(f) || f < 0 || f > math.MaxUint32 {
			s.throw("BSONError", "invalid unsigned 32-bit integer "+c.Argument(0).String())
		}

		return s.vm.ToValue(uint32(f))
	})
	set("decimalCheck", func(c goja.FunctionCall) goja.Value {
		d, err := primitive.ParseDecimal128(arg(c))
		if err != nil {
			s.throw("BSONError", "invalid Decimal128 "+strconv.Quote(arg(c)))
		}

		return s.vm.ToValue(d.String())
	})
	set("base64Check", func(c goja.FunctionCall) goja.Value {
		if _, err := base64.StdEncoding.DecodeString(arg(c)); err != nil {
			s.throw("BSONError", "invalid base64 "+strconv.Quote(arg(c)))
		}

		return s.vm.ToValue(arg(c))
	})
	set("hexToBase64", func(c goja.FunctionCall) goja.Value {
		b, err := hex.DecodeString(arg(c))
		if err != nil {
			s.throw("BSONError", "invalid hex "+strconv.Quote(arg(c)))
		}

		return s.vm.ToValue(base64.StdEncoding.EncodeToString(b))
	})
	set("base64ToHex", func(c goja.FunctionCall) goja.Value {
		b, _ := base64.StdEncoding.DecodeString(arg(c))
		return s.vm.ToValue(hex.EncodeToString(b))
	})
	set("base64Len", func(c goja.FunctionCall) goja.Value {
		b, _ := base64.StdEncoding.DecodeString(arg(c))
		return s.vm.ToValue(len(b))
	})
	set("uuidNew", func(goja.FunctionCall) goja.Value {
		u := uuid.New()
		return s.vm.ToValue(base64.StdEncoding.EncodeToString(u[:]))
	})
	set("uuidParse", func(c goja.FunctionCall) goja.Value {
		u, err := uuid.Parse(arg(c))
		if err != nil {
			s.throw("BSONError", "UUID must be a 32 hex digit string, got "+strconv.Quote(arg(c)))
		}

		return s.vm.ToValue(base64.StdEncoding.EncodeToString(u[:]))
	})
	set("uuidFormat", func(c goja.FunctionCall) goja.Value {
		b, _ := base64.StdEncoding.DecodeString(arg(c))

		u, err := uuid.FromBytes(b)
		if err != nil {
			return s.vm.ToValue(hex.EncodeToString(b))
		}

		return s.vm.ToValue(u.String())
	})
	set("parseDate", func(c goja.FunctionCall) goja.Value {
		t, err := parseISODate(arg(c))
		if err != nil {
			s.throw("BSONError", err.Error())
		}

		return s.vm.ToValue(t.UnixMilli())
	})

	return n
}

// parseISODate accepts the forms ISODate() does: a date, a date and time
// with optional fractional seconds, with or without a zone ("Z" or an offset).
func parseISODate(str string) (time.Time, error) {
	str = strings.TrimSpace(str)

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999Z0700",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02",
		"20060102T150405Z0700",
		"20060102",
	}

	for _, l := range layouts {
		if t, err := time.Parse(l, str); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("%q is not a valid ISO-8601 date", str)
}
