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
	"sort"

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// sortArray represents `$sortArray` operator.
type sortArray struct {
	input  any
	sortBy any
}

// newSortArray returns `$sortArray` operator.
//
// The specification document has the shape `{input: <arrayExpr>, sortBy: <spec>}`.
// `sortBy` is either `1`/`-1` to sort by whole values, or a document such as
// `{field: 1}` to sort documents by one or more fields.
func newSortArray(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$sortArray",
			fmt.Sprintf("Expression $sortArray takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$sortArray",
			fmt.Sprintf("$sortArray only supports an object as its argument, but got %s", handlerparams.AliasFromType(args[0])),
		)
	}

	input, err := spec.Get("input")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$sortArray",
			"$sortArray requires 'input' to be specified",
		)
	}

	sortBy, err := spec.Get("sortBy")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$sortArray",
			"$sortArray requires 'sortBy' to be specified",
		)
	}

	return &sortArray{input: input, sortBy: sortBy}, nil
}

// Process implements Operator interface.
func (o *sortArray) Process(doc *types.Document) (any, error) {
	input, err := evaluateExpression(o.input, doc)
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
			"$sortArray",
			fmt.Sprintf("The input argument to $sortArray must be an array, but was of type: %s", handlerparams.AliasFromType(input)),
		)
	}

	elems := make([]any, 0, arr.Len())

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

		elems = append(elems, elem)
	}

	switch sortBy := o.sortBy.(type) {
	case *types.Document:
		if err := sortByFields(elems, sortBy); err != nil {
			return nil, err
		}
	case int32, int64, float64:
		n, err := handlerparams.GetWholeNumberParam(sortBy)
		if err != nil || (n != 1 && n != -1) {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$sortArray",
				"The $sort element value must be either 1 or -1",
			)
		}

		order := types.Ascending
		if n == -1 {
			order = types.Descending
		}

		sort.SliceStable(elems, func(i, j int) bool {
			return types.CompareOrderForSort(elems[i], elems[j], order) == types.Less
		})
	default:
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$sortArray",
			"The $sortArray sortBy must be an object or an integer",
		)
	}

	res := types.MakeArray(len(elems))
	res.Append(elems...)

	return res, nil
}

// fieldSort holds a resolved field path and its sort order.
type fieldSort struct {
	path  types.Path
	order types.SortType
}

// sortByFields sorts elems in place using the field/order pairs from sortBy.
func sortByFields(elems []any, sortBy *types.Document) error {
	fields := make([]fieldSort, 0, sortBy.Len())

	iter := sortBy.Iterator()
	defer iter.Close()

	for {
		key, value, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return lazyerrors.Error(err)
		}

		n, err := handlerparams.GetWholeNumberParam(value)
		if err != nil || (n != 1 && n != -1) {
			return newOperatorError(
				ErrArgsInvalidLen,
				"$sortArray",
				"The $sort element value must be either 1 or -1",
			)
		}

		path, err := types.NewPathFromString(key)
		if err != nil {
			return newOperatorError(
				ErrArgsInvalidLen,
				"$sortArray",
				fmt.Sprintf("$sortArray sort key is not valid: %s", key),
			)
		}

		order := types.Ascending
		if n == -1 {
			order = types.Descending
		}

		fields = append(fields, fieldSort{path: path, order: order})
	}

	sort.SliceStable(elems, func(i, j int) bool {
		for _, f := range fields {
			a := fieldValue(elems[i], f.path)
			b := fieldValue(elems[j], f.path)

			switch types.CompareOrderForSort(a, b, f.order) {
			case types.Less:
				return true
			case types.Greater:
				return false
			case types.Equal:
				continue
			}
		}

		return false
	})

	return nil
}

// fieldValue resolves the value at path within elem, returning Null when elem is
// not a document or the path is missing.
func fieldValue(elem any, path types.Path) any {
	d, ok := elem.(*types.Document)
	if !ok {
		return types.Null
	}

	v, err := d.GetByPath(path)
	if err != nil {
		return types.Null
	}

	if v == nil {
		return types.Null
	}

	return v
}

// check interfaces
var (
	_ Operator = (*sortArray)(nil)
)
