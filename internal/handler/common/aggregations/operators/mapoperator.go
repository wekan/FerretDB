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
	"fmt"
	"strings"

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// mapOp represents `$map` operator.
type mapOp struct {
	input any
	in    any
	as    string
}

// newMap returns `$map` operator.
//
// The specification document has the shape `{input: <arrayExpr>, as: <name>, in: <expr>}`.
// For each element of the evaluated `input` array, `in` is evaluated with the
// variable `$$<as>` (defaulting to `$$this`) bound to that element, and the
// array of results is returned.
func newMap(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$map",
			fmt.Sprintf("Expression $map takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$map",
			fmt.Sprintf("$map only supports an object as its argument, but got %s", handlerparams.AliasFromType(args[0])),
		)
	}

	input, err := spec.Get("input")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$map",
			"Missing 'input' parameter to $map",
		)
	}

	in, err := spec.Get("in")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$map",
			"Missing 'in' parameter to $map",
		)
	}

	as := "this"

	if spec.Has("as") {
		asValue, _ := spec.Get("as")

		s, ok := asValue.(string)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$map",
				"the 'as' parameter to $map must be a string",
			)
		}

		if s != "" {
			as = s
		}
	}

	return &mapOp{
		input: input,
		as:    as,
		in:    in,
	}, nil
}

// Process implements Operator interface.
func (m *mapOp) Process(doc *types.Document) (any, error) {
	input, err := evaluateExpression(m.input, doc)
	if err != nil {
		return nil, err
	}

	if _, isNull := input.(types.NullType); isNull {
		return types.Null, nil
	}

	arr, ok := input.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$map",
			fmt.Sprintf("input to $map must be an array not %s", handlerparams.AliasFromType(input)),
		)
	}

	res := types.MakeArray(arr.Len())

	iter := arr.Iterator()
	defer iter.Close()

	for {
		_, elem, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		// bind the `$$<as>` variable to the element by substituting it into `in`,
		// then evaluate the resulting variable-free expression against the document
		bound, err := bindVars(m.in, m.as, elem)
		if err != nil {
			return nil, err
		}

		v, err := evaluateExpression(bound, doc)
		if err != nil {
			return nil, err
		}

		res.Append(v)
	}

	return res, nil
}

// bindVars returns a copy of expression v where every reference to the variable
// `$$name` (and dotted sub-paths `$$name.field`) is replaced by the resolved
// value taken from elem. Other values are returned unchanged, so the result is a
// variable-free expression that can be evaluated by evaluateExpression.
func bindVars(v any, name string, elem any) (any, error) {
	switch v := v.(type) {
	case *types.Document:
		res := new(types.Document)

		iter := v.Iterator()
		defer iter.Close()

		for {
			k, val, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				return nil, lazyerrors.Error(err)
			}

			bound, err := bindVars(val, name, elem)
			if err != nil {
				return nil, err
			}

			res.Set(k, bound)
		}

		return res, nil
	case *types.Array:
		res := types.MakeArray(v.Len())

		iter := v.Iterator()
		defer iter.Close()

		for {
			_, val, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				return nil, lazyerrors.Error(err)
			}

			bound, err := bindVars(val, name, elem)
			if err != nil {
				return nil, err
			}

			res.Append(bound)
		}

		return res, nil
	case string:
		variable := "$$" + name

		if v == variable {
			return elem, nil
		}

		if strings.HasPrefix(v, variable+".") {
			suffix := strings.TrimPrefix(v, variable+".")

			d, ok := elem.(*types.Document)
			if !ok {
				return types.Null, nil
			}

			expression, err := aggregations.NewExpression("$"+suffix, nil)
			if err != nil {
				return types.Null, nil
			}

			resolved, err := expression.Evaluate(d)
			if err != nil {
				return types.Null, nil
			}

			return resolved, nil
		}

		return v, nil
	default:
		return v, nil
	}
}

// check interfaces
var (
	_ Operator = (*mapOp)(nil)
)
