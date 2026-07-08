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

package operators

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/dop251/goja"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// TypesToJS recursively converts a FerretDB internal/types value into a
// goja-friendly Go value.
//
// Documents become map[string]any, arrays become []any and scalars are passed
// through in a form the goja runtime understands. Values that have no natural
// JavaScript representation (ObjectID, Binary, Timestamp) are converted to a
// string so that evaluation never panics.
func TypesToJS(v any) any {
	switch v := v.(type) {
	case *types.Document:
		m := make(map[string]any, v.Len())

		iter := v.Iterator()
		defer iter.Close()

		for {
			k, val, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				break
			}

			m[k] = TypesToJS(val)
		}

		return m
	case *types.Array:
		s := make([]any, 0, v.Len())

		iter := v.Iterator()
		defer iter.Close()

		for {
			_, val, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				break
			}

			s = append(s, TypesToJS(val))
		}

		return s
	case types.NullType:
		return nil
	case types.ObjectID:
		return hex.EncodeToString(v[:])
	case types.Binary:
		return hex.EncodeToString(v.B)
	case types.Timestamp:
		return uint64(v)
	case float64, string, bool, int32, int64:
		return v
	default:
		// time.Time and any other scalar is passed through; goja handles time.Time.
		return v
	}
}

// JSToTypes converts a JavaScript value produced by goja back into a FerretDB
// internal/types value.
//
// Numbers become int32/int64 when they are integral and fit, otherwise float64;
// strings, booleans, objects and arrays are converted recursively; null and
// undefined become types.Null.
func JSToTypes(v goja.Value) (any, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return types.Null, nil
	}

	return exportedToTypes(v.Export())
}

// exportedToTypes converts a value produced by goja.Value.Export into a FerretDB
// internal/types value.
func exportedToTypes(v any) (any, error) {
	switch v := v.(type) {
	case nil:
		return types.Null, nil
	case bool:
		return v, nil
	case string:
		return v, nil
	case int:
		return normalizeInt(int64(v)), nil
	case int32:
		return v, nil
	case int64:
		return normalizeInt(v), nil
	case float64:
		// represent integral floats that fit in int32 as int32 for nicer results
		if v == float64(int32(v)) {
			return int32(v), nil
		}

		return v, nil
	case map[string]any:
		doc := types.MakeDocument(len(v))

		for k, val := range v {
			converted, err := exportedToTypes(val)
			if err != nil {
				return nil, err
			}

			doc.Set(k, converted)
		}

		return doc, nil
	case []any:
		arr := types.MakeArray(len(v))

		for _, val := range v {
			converted, err := exportedToTypes(val)
			if err != nil {
				return nil, err
			}

			arr.Append(converted)
		}

		return arr, nil
	default:
		return nil, lazyerrors.Errorf("cannot convert JavaScript value of type %T to BSON", v)
	}
}

// normalizeInt returns an int32 when v fits into its range, otherwise int64.
func normalizeInt(v int64) any {
	if v >= math.MinInt32 && v <= math.MaxInt32 {
		return int32(v)
	}

	return v
}

// EvalWhere evaluates a `$where` JavaScript predicate against doc.
//
// code is either a JavaScript expression (evaluated with `this` bound to the
// document, e.g. `this.a > 1`) or a JavaScript function source string that is
// called with `this` bound to the document. The result is reported using
// JavaScript truthiness.
func EvalWhere(code string, doc *types.Document) (bool, error) {
	vm := goja.New()

	this := vm.ToValue(TypesToJS(doc))

	// A function source string wrapped in parentheses evaluates to a callable
	// function; call it with `this` bound to the document.
	if val, err := vm.RunString("(" + code + ")"); err == nil {
		if callable, ok := goja.AssertFunction(val); ok {
			res, cerr := callable(this)
			if cerr != nil {
				return false, fmt.Errorf("$where execution failure: %w", cerr)
			}

			return res.ToBoolean(), nil
		}
	}

	// Otherwise treat the code as an expression and evaluate it inside a
	// function body so that `this` (and field access like `this.a`) is bound to
	// the document.
	fn, err := vm.RunString("(function(){ return (" + code + "); })")
	if err != nil {
		return false, fmt.Errorf("$where compile failure: %w", err)
	}

	callable, ok := goja.AssertFunction(fn)
	if !ok {
		return false, fmt.Errorf("$where compile failure: not a function")
	}

	res, err := callable(this)
	if err != nil {
		return false, fmt.Errorf("$where execution failure: %w", err)
	}

	return res.ToBoolean(), nil
}
