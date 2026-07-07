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

// reduce represents `$reduce` operator.
type reduce struct {
	input        any
	initialValue any
	in           any
}

// newReduce returns `$reduce` operator.
//
// The specification document has the shape
// `{input: <arrayExpr>, initialValue: <expr>, in: <expr>}`.
// It folds the evaluated `input` array, evaluating `in` for each element with
// the variables `$$value` (the accumulator) and `$$this` (the current element)
// bound, and returns the final accumulator value.
func newReduce(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reduce",
			fmt.Sprintf("Expression $reduce takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reduce",
			fmt.Sprintf("$reduce only supports an object as its argument, but got %s", handlerparams.AliasFromType(args[0])),
		)
	}

	input, err := spec.Get("input")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reduce",
			"Missing 'input' parameter to $reduce",
		)
	}

	initialValue, err := spec.Get("initialValue")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reduce",
			"Missing 'initialValue' parameter to $reduce",
		)
	}

	in, err := spec.Get("in")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reduce",
			"Missing 'in' parameter to $reduce",
		)
	}

	return &reduce{
		input:        input,
		initialValue: initialValue,
		in:           in,
	}, nil
}

// Process implements Operator interface.
func (r *reduce) Process(doc *types.Document) (any, error) {
	input, err := evaluateExpression(r.input, doc)
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
			"$reduce",
			fmt.Sprintf("input to $reduce must be an array not %s", handlerparams.AliasFromType(input)),
		)
	}

	accumulator, err := evaluateExpression(r.initialValue, doc)
	if err != nil {
		return nil, err
	}

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

		// bind `$$value` to the current accumulator and `$$this` to the current
		// element by substituting them into `in`, then evaluate the resulting
		// variable-free expression against the document.
		bound, err := bindVars(r.in, "value", accumulator)
		if err != nil {
			return nil, err
		}

		bound, err = bindVars(bound, "this", elem)
		if err != nil {
			return nil, err
		}

		accumulator, err = evaluateExpression(bound, doc)
		if err != nil {
			return nil, err
		}
	}

	return accumulator, nil
}

// check interfaces
var (
	_ Operator = (*reduce)(nil)
)
