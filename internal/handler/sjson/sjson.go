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

// Package sjson provides converters from/to jsonb with some extensions for built-in and `types` types.
//
// See contributing guidelines and documentation for package `types` for details.
//
// # Mapping
//
// SJSON uses schema to map values to data types.
// Schema is stored in the `$s` field of the document and contains information about the fields.
// A document with schema looks like this:
//
//	{
//	   "$s": {
//	     "$k": ["field1", "field2", ...],
//	     "p": {
//	       "field1": {<schema>},
//	       "field2": {<schema>},
//	       ...
//	   }
//	   "field1": <json representation>,
//	   "field2": <json representation>,
//	   ...
//	}
//
// Composite types
//
//	Alias      types package    sjson package        sjson schema                                             JSON representation
//
//	object     *types.Document  *sjson.documentType  {"t":"object", "$s": {"$k":[<keys>], "p":{<properties>}} JSON object
//	array      *types.Array     *sjson.arrayType     {"t":"array", "i": [<item 1>, <item 2>]}                 JSON array
//
// Scalar types
//
//	Alias      types package    sjson package         sjson schema                           JSON representation
//
//	double     float64          *sjson.doubleType     {"t":"double"}                         JSON number
//	string     string           *sjson.stringType     {"t":"string"}                         JSON string
//	binData    types.Binary     *sjson.binaryType     {"t":"binData",
//	                                                   "s":<subtype number>}                 "<base 64 string>"
//	objectId   types.ObjectID   *sjson.objectIDType   {"t":"objectId"}                       "<ObjectID as 24 character hex string>"
//	bool       bool             *sjson.boolType       {"t":"bool"}                           JSON true / false values
//	date       time.Time        *sjson.dateTimeType   {"t":"date"}                           milliseconds since epoch as JSON number
//	null       types.NullType   *sjson.nullType       {"t":"null"}                           JSON null
//	regex      types.Regex      *sjson.regexType      {"t":"regex",
//	                                                   "o": "<string w/o terminating 0x0>"}  "<string w/o terminating 0x0>"
//	int        int32            *sjson.int32Type      {"t":"int"}                            JSON number
//	timestamp  types.Timestamp  *sjson.timestampType  {"t":"timestamp"}                      JSON number
//	long       int64            *sjson.int64Type      {"t":"long"}                           JSON number
//
//nolint:lll // for readability
package sjson

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/AlekSi/pointer"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

const (
	schemaCacheEntries  = 128
	schemaCacheMaxBytes = 32 * 1024
)

var decodedSchemas = struct {
	sync.RWMutex
	m map[[sha256.Size]byte]*schema
}{m: make(map[[sha256.Size]byte]*schema)}

// decodeSchema reuses immutable parsed schemas for repeated document shapes.
// Both the raw-size limit and entry cap are deliberately small and hard: SJSON
// schemas can describe every array element, so caching arbitrary restored input
// would otherwise exchange CPU pressure for unbounded resident memory.
func decodeSchema(data []byte) (*schema, error) {
	cacheable := len(data) <= schemaCacheMaxBytes
	var key [sha256.Size]byte
	if cacheable {
		key = sha256.Sum256(data)
		decodedSchemas.RLock()
		cached := decodedSchemas.m[key]
		decodedSchemas.RUnlock()
		if cached != nil {
			return cached, nil
		}
	}

	var sch schema
	r := bytes.NewReader(data)
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sch); err != nil {
		return nil, lazyerrors.Error(err)
	}
	if err := checkConsumed(dec, r); err != nil {
		return nil, lazyerrors.Error(err)
	}

	if cacheable {
		decodedSchemas.Lock()
		if len(decodedSchemas.m) >= schemaCacheEntries {
			clear(decodedSchemas.m)
		}
		decodedSchemas.m[key] = &sch
		decodedSchemas.Unlock()
	}
	return &sch, nil
}

// sjsontype is a type that can be marshaled from/to sjson.
//
//sumtype:decl
type sjsontype interface {
	sjsontype() // seal for sumtype

	json.Marshaler
}

// checkConsumed returns error if decoder or reader have buffered or unread data.
func checkConsumed(dec *json.Decoder, r *bytes.Reader) error {
	if dr := dec.Buffered().(*bytes.Reader); dr.Len() != 0 {
		b, _ := io.ReadAll(dr)

		if l := len(b); l != 0 {
			return lazyerrors.Errorf("%d bytes remains in the decoder: %s", l, b)
		}
	}

	if l := r.Len(); l != 0 {
		b, _ := io.ReadAll(r)
		return lazyerrors.Errorf("%d bytes remains in the reader: %s", l, b)
	}

	return nil
}

// fromSJSON converts sjsontype value to matching built-in or types' package value.
func fromSJSON(v sjsontype) any {
	switch v := v.(type) {
	case *documentType:
		return pointer.To(types.Document(*v))
	case *arrayType:
		return pointer.To(types.Array(*v))
	case *doubleType:
		return float64(*v)
	case *stringType:
		return string(*v)
	case *binaryType:
		return types.Binary(*v)
	case *objectIDType:
		return types.ObjectID(*v)
	case *boolType:
		return bool(*v)
	case *dateTimeType:
		return time.Time(*v)
	case *nullType:
		return types.Null
	case *regexType:
		return types.Regex(*v)
	case *int32Type:
		return int32(*v)
	case *timestampType:
		return types.Timestamp(*v)
	case *int64Type:
		return int64(*v)
	}

	panic(fmt.Sprintf("not reached: %T", v)) // for sumtype to work
}

// toSJSON converts built-in or types' package value to sjsontype value.
func toSJSON(v any) sjsontype {
	switch v := v.(type) {
	case *types.Document:
		return pointer.To(documentType(*v))
	case *types.Array:
		return pointer.To(arrayType(*v))
	case float64:
		return pointer.To(doubleType(v))
	case string:
		return pointer.To(stringType(v))
	case types.Binary:
		return pointer.To(binaryType(v))
	case types.ObjectID:
		return pointer.To(objectIDType(v))
	case bool:
		return pointer.To(boolType(v))
	case time.Time:
		return pointer.To(dateTimeType(v))
	case types.NullType:
		return pointer.To(nullType(v))
	case types.Regex:
		return pointer.To(regexType(v))
	case int32:
		return pointer.To(int32Type(v))
	case types.Timestamp:
		return pointer.To(timestampType(v))
	case int64:
		return pointer.To(int64Type(v))
	}

	panic(fmt.Sprintf("not reached: %T", v)) // for sumtype to work
}

// Unmarshal decodes the top-level document.
// It decodes document's schema from the `$s` field and uses it to decode the data of the document.
func Unmarshal(data []byte) (*types.Document, error) {
	var v map[string]json.RawMessage
	// Unmarshal already rejects trailing non-whitespace input. Using it directly
	// avoids allocating a Reader, Decoder and its read buffer for every document
	// returned by a large query.
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, lazyerrors.Error(err)
	}

	// decode schema from the $s field of the document
	jsch, ok := v["$s"]
	if !ok {
		return nil, lazyerrors.Errorf("schema is not set")
	}

	sch, err := decodeSchema(jsch)
	if err != nil {
		return nil, err
	}

	delete(v, "$s")

	// decode data from the rest of the document using the schema
	if len(sch.Keys) != len(v) {
		return nil, lazyerrors.Errorf(
			"sjson.Unmarshal: the data must have the same number of schema keys and document fields (keys: %d, fields: %d)",
			len(sch.Keys), len(v),
		)
	}

	d := types.MakeDocument(len(sch.Keys))

	for _, key := range sch.Keys {
		b, ok := v[key]

		if !ok {
			return nil, lazyerrors.Errorf("sjson.Unmarshal: missing key %q", key)
		}

		v, err := unmarshalSingleValue(b, sch.Properties[key])
		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		d.Set(key, v)
	}

	return d, nil
}

// UnmarshalFields decodes only selected top-level fields. It still parses the
// complete outer JSON object, but avoids recursively decoding unobservable
// values and their often much larger schemas (for example member arrays when a
// client requested only _id). Missing selected fields remain missing.
func UnmarshalFields(data []byte, fields []string) (*types.Document, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, lazyerrors.Error(err)
	}

	jsch, ok := values["$s"]
	if !ok {
		return nil, lazyerrors.Errorf("schema is not set")
	}
	var sch struct {
		Properties map[string]json.RawMessage `json:"p"`
		Keys       []string                   `json:"$k"`
	}
	if err := json.Unmarshal(jsch, &sch); err != nil {
		return nil, lazyerrors.Error(err)
	}

	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	d := must.NotFail(types.NewDocument())
	for _, key := range sch.Keys {
		if _, ok := wanted[key]; !ok {
			continue
		}
		value, exists := values[key]
		if !exists {
			return nil, lazyerrors.Errorf("sjson.UnmarshalFields: missing key %q", key)
		}
		rawElem, exists := sch.Properties[key]
		if !exists {
			return nil, lazyerrors.Errorf("sjson.UnmarshalFields: missing schema for key %q", key)
		}
		var fieldSchema elem
		if err := json.Unmarshal(rawElem, &fieldSchema); err != nil {
			return nil, lazyerrors.Error(err)
		}
		decoded, err := unmarshalSingleValue(value, &fieldSchema)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}
		d.Set(key, decoded)
	}
	return d, nil
}

// unmarshalSingleValue decodes the given sjson-encoded data element by the given schema.
func unmarshalSingleValue(data json.RawMessage, sch *elem) (any, error) {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		return fromSJSON(new(nullType)), nil
	}

	if sch == nil {
		return nil, lazyerrors.Errorf("schema is not set")
	}

	// Scalar JSON values need no streaming decoder. json.Unmarshal consumes
	// exactly one complete value and rejects trailing non-whitespace data, while
	// avoiding a decoder, reader and buffered-state allocation for every field.
	switch sch.Type {
	case elemTypeString:
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, lazyerrors.Error(err)
		}
		return v, nil
	case elemTypeBool:
		switch string(data) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, lazyerrors.Errorf("invalid bool %q", data)
		}
	case elemTypeInt:
		v, err := strconv.ParseInt(string(data), 10, 32)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}
		return int32(v), nil
	case elemTypeLong:
		v, err := strconv.ParseInt(string(data), 10, 64)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}
		return v, nil
	case elemTypeTimestamp:
		v, err := strconv.ParseUint(string(data), 10, 64)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}
		return types.Timestamp(v), nil
	case elemTypeDate:
		v, err := strconv.ParseInt(string(data), 10, 64)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}
		return time.UnixMilli(v), nil
	case elemTypeDouble:
		if bytes.Equal(data, []byte(`"NaN"`)) {
			return math.NaN(), nil
		}
		v, err := strconv.ParseFloat(string(data), 64)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}
		return v, nil
	}

	var res sjsontype
	var err error

	switch sch.Type {
	case elemTypeObject:
		if sch.Schema == nil {
			return nil, lazyerrors.Errorf("sjson.unmarshalSingleValue: schema is not set")
		}

		var d documentType
		err = d.UnmarshalJSONWithSchema(data, sch.Schema)
		res = &d
	case elemTypeArray:
		if sch.Items == nil {
			return nil, lazyerrors.Errorf("sjson.unmarshalSingleValue: schema's items are not set")
		}

		var a arrayType
		err = a.UnmarshalJSONWithSchema(data, sch.Items)
		res = &a
	case elemTypeDouble:
		var d doubleType
		err = d.UnmarshalJSON(data)
		res = &d
	case elemTypeString:
		var s stringType
		err = s.UnmarshalJSON(data)
		res = &s
	case elemTypeBinData:
		var b binaryType
		err = b.UnmarshalJSONWithSchema(data, sch)
		res = &b
	case elemTypeObjectID:
		var o objectIDType
		err = o.UnmarshalJSON(data)
		res = &o
	case elemTypeBool:
		var b boolType
		err = b.UnmarshalJSON(data)
		res = &b
	case elemTypeDate:
		var d dateTimeType
		err = d.UnmarshalJSON(data)
		res = &d
	case elemTypeNull:
		return nil, lazyerrors.Errorf("sjson.unmarshalSingleValue: expected null, got %s", data)
	case elemTypeRegex:
		var r regexType
		err = r.UnmarshalJSONWithSchema(data, sch)
		res = &r
	case elemTypeInt:
		var i int32Type
		err = i.UnmarshalJSON(data)
		res = &i
	case elemTypeTimestamp:
		var t timestampType
		err = t.UnmarshalJSON(data)
		res = &t
	case elemTypeLong:
		var l int64Type
		err = l.UnmarshalJSON(data)
		res = &l
	default:
		return nil, lazyerrors.Errorf("sjson.unmarshalSingleValue: unhandled type %q", sch.Type)
	}

	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return fromSJSON(res), nil
}

// Marshal encodes given document fields and set its schema in the field $s.
// Use it when you need to encode a document with schema, for example, when you want to store it in a database.
func Marshal(d *types.Document) ([]byte, error) {
	if d == nil {
		panic("v is nil")
	}

	schema, err := marshalSchemaForDoc(d)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	buf.WriteString(`{"$s":`)
	buf.Write(schema)

	keys := d.Keys()
	values := d.Values()

	for i, key := range keys {
		buf.WriteByte(',')
		buf.WriteString(`"`)
		buf.WriteString(key)
		buf.WriteString(`":`)

		b, err := toSJSON(values[i]).MarshalJSON()
		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		buf.Write(b)
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}

// MarshalSingleValue encodes given built-in or types' package value into sjson.
// Use it when you need to encode a single value, for example in a where clause.
func MarshalSingleValue(v any) ([]byte, error) {
	if v == nil {
		panic("v is nil")
	}

	b, err := toSJSON(v).MarshalJSON()
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return b, nil
}
