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
	"errors"

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// evaluateExpression evaluates a single operator operand against the document.
//
// It resolves nested operator documents (e.g. `{$sum: 1}`), field path and
// variable string expressions (e.g. `"$field"`), plain documents and arrays
// (whose values are evaluated recursively) and plain literal values.
//
// A missing field path resolves to Null, matching how MongoDB treats a missing
// value as null in expression operators.
func evaluateExpression(arg any, doc *types.Document) (any, error) {
	switch arg := arg.(type) {
	case *types.Document:
		if IsOperator(arg) {
			op, err := NewOperator(arg)
			if err != nil {
				return nil, err
			}

			v, err := op.Process(doc)
			if err != nil {
				return nil, err
			}

			return v, nil
		}

		res := new(types.Document)

		iter := arg.Iterator()
		defer iter.Close()

		for {
			k, v, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				return nil, lazyerrors.Error(err)
			}

			processed, err := evaluateExpression(v, doc)
			if err != nil {
				return nil, err
			}

			res.Set(k, processed)
		}

		return res, nil
	case *types.Array:
		res := types.MakeArray(arg.Len())

		iter := arg.Iterator()
		defer iter.Close()

		for {
			_, v, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				return nil, lazyerrors.Error(err)
			}

			processed, err := evaluateExpression(v, doc)
			if err != nil {
				return nil, err
			}

			res.Append(processed)
		}

		return res, nil
	case string:
		expression, err := aggregations.NewExpression(arg, nil)

		var exprErr *aggregations.ExpressionError
		if errors.As(err, &exprErr) && exprErr.Code() == aggregations.ErrNotExpression {
			// plain string, not a field path expression
			return arg, nil
		}

		if err != nil {
			return nil, err
		}

		v, err := expression.Evaluate(doc)
		if err != nil {
			// a missing field evaluates to null
			return types.Null, nil
		}

		return v, nil
	default:
		return arg, nil
	}
}

// isTruthy reports whether value is truthy using MongoDB boolean semantics.
//
// Falsy values are false, null (and missing, which is resolved to null) and a
// numeric zero of any type; every other value is truthy.
func isTruthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case types.NullType:
		return false
	case float64:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	default:
		return true
	}
}
