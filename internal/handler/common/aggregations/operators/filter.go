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

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// filter represents `$filter` operator.
type filter struct {
	input any
	cond  any
	limit any
	as    string
}

// newFilter returns `$filter` operator.
//
// The specification document has the shape
// `{input: <arrayExpr>, cond: <expr>, as: <name>, limit: <expr>}`.
// For each element of the evaluated `input` array, `cond` is evaluated with the
// variable `$$<as>` (defaulting to `$$this`) bound to that element, and the
// element is kept when `cond` is truthy. The optional `limit` caps the number of
// returned elements.
func newFilter(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$filter",
			fmt.Sprintf("Expression $filter takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$filter",
			fmt.Sprintf("$filter only supports an object as its argument, but got %s", handlerparams.AliasFromType(args[0])),
		)
	}

	input, err := spec.Get("input")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$filter",
			"Missing 'input' parameter to $filter",
		)
	}

	cond, err := spec.Get("cond")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$filter",
			"Missing 'cond' parameter to $filter",
		)
	}

	f := &filter{
		input: input,
		cond:  cond,
		as:    "this",
	}

	if spec.Has("as") {
		asValue, _ := spec.Get("as")

		s, ok := asValue.(string)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$filter",
				"the 'as' parameter to $filter must be a string",
			)
		}

		if s != "" {
			f.as = s
		}
	}

	if spec.Has("limit") {
		limitValue, _ := spec.Get("limit")
		f.limit = limitValue
	}

	return f, nil
}

// Process implements Operator interface.
func (f *filter) Process(doc *types.Document) (any, error) {
	input, err := evaluateExpression(f.input, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(input) {
		return types.Null, nil
	}

	arr, ok := input.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$filter",
			fmt.Sprintf("input to $filter must be an array not %s", handlerparams.AliasFromType(input)),
		)
	}

	limit := int64(-1)

	if f.limit != nil {
		limitValue, err := evaluateExpression(f.limit, doc)
		if err != nil {
			return nil, err
		}

		if !isNullValue(limitValue) {
			n, err := handlerparams.GetWholeNumberParam(limitValue)
			if err != nil {
				return nil, newOperatorError(
					ErrArgsInvalidLen,
					"$filter",
					fmt.Sprintf("$filter: limit must be represented as a 32-bit integral value: %s", handlerparams.AliasFromType(limitValue)),
				)
			}

			if n < 1 {
				return nil, newOperatorError(
					ErrArgsInvalidLen,
					"$filter",
					fmt.Sprintf("$filter: limit must be greater than 0: %d", n),
				)
			}

			limit = n
		}
	}

	res := types.MakeArray(0)

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

		if limit >= 0 && int64(res.Len()) >= limit {
			break
		}

		// bind the `$$<as>` variable to the element by substituting it into `cond`,
		// then evaluate the resulting variable-free expression against the document.
		bound, err := bindVars(f.cond, f.as, elem)
		if err != nil {
			return nil, err
		}

		v, err := evaluateExpression(bound, doc)
		if err != nil {
			return nil, err
		}

		if isTruthy(v) {
			res.Append(elem)
		}
	}

	return res, nil
}

// check interfaces
var (
	_ Operator = (*filter)(nil)
)
