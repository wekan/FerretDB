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

// setUnion represents `$setUnion` operator.
type setUnion struct {
	args []any
}

// newSetUnion returns `$setUnion` operator.
func newSetUnion(args ...any) (Operator, error) {
	return &setUnion{args: args}, nil
}

// Process implements Operator interface.
func (o *setUnion) Process(doc *types.Document) (any, error) {
	sets, isNull, err := evalSets("$setUnion", o.args, doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		return types.Null, nil
	}

	res := make([]any, 0)

	for _, set := range sets {
		res = appendUnique(res, set...)
	}

	return sortedArray(res), nil
}

// setIntersection represents `$setIntersection` operator.
type setIntersection struct {
	args []any
}

// newSetIntersection returns `$setIntersection` operator.
func newSetIntersection(args ...any) (Operator, error) {
	return &setIntersection{args: args}, nil
}

// Process implements Operator interface.
func (o *setIntersection) Process(doc *types.Document) (any, error) {
	sets, isNull, err := evalSets("$setIntersection", o.args, doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		return types.Null, nil
	}

	if len(sets) == 0 {
		return sortedArray(nil), nil
	}

	res := appendUnique(nil, sets[0]...)

	for _, set := range sets[1:] {
		var kept []any

		for _, v := range res {
			if containsValue(set, v) {
				kept = append(kept, v)
			}
		}

		res = kept
	}

	return sortedArray(res), nil
}

// setDifference represents `$setDifference` operator.
type setDifference struct {
	args []any
}

// newSetDifference returns `$setDifference` operator.
func newSetDifference(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$setDifference",
			fmt.Sprintf("Expression $setDifference takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &setDifference{args: args}, nil
}

// Process implements Operator interface.
func (o *setDifference) Process(doc *types.Document) (any, error) {
	sets, isNull, err := evalSets("$setDifference", o.args, doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		return types.Null, nil
	}

	first := appendUnique(nil, sets[0]...)

	var res []any

	for _, v := range first {
		if !containsValue(sets[1], v) {
			res = append(res, v)
		}
	}

	return sortedArray(res), nil
}

// setEquals represents `$setEquals` operator.
type setEquals struct {
	args []any
}

// newSetEquals returns `$setEquals` operator.
func newSetEquals(args ...any) (Operator, error) {
	if len(args) < 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$setEquals",
			fmt.Sprintf("$setEquals needs at least two arguments had: %d", len(args)),
		)
	}

	return &setEquals{args: args}, nil
}

// Process implements Operator interface.
func (o *setEquals) Process(doc *types.Document) (any, error) {
	sets, isNull, err := evalSets("$setEquals", o.args, doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		// null is tolerated during the $project dry-run validation.
		return types.Null, nil
	}

	first := appendUnique(nil, sets[0]...)

	for _, set := range sets[1:] {
		other := appendUnique(nil, set...)

		if !sameSet(first, other) {
			return false, nil
		}
	}

	return true, nil
}

// setIsSubset represents `$setIsSubset` operator.
type setIsSubset struct {
	args []any
}

// newSetIsSubset returns `$setIsSubset` operator.
func newSetIsSubset(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$setIsSubset",
			fmt.Sprintf("Expression $setIsSubset takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &setIsSubset{args: args}, nil
}

// Process implements Operator interface.
func (o *setIsSubset) Process(doc *types.Document) (any, error) {
	sets, isNull, err := evalSets("$setIsSubset", o.args, doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		// null is tolerated during the $project dry-run validation.
		return types.Null, nil
	}

	for _, v := range sets[0] {
		if !containsValue(sets[1], v) {
			return false, nil
		}
	}

	return true, nil
}

// evalSets evaluates each argument to an array and returns their elements.
//
// The second return value reports whether any argument resolved to Null, which
// callers translate into the operator-specific null/error behaviour.
func evalSets(op string, args []any, doc *types.Document) ([][]any, bool, error) {
	sets := make([][]any, 0, len(args))

	for _, arg := range args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, false, err
		}

		if isNullValue(v) {
			return nil, true, nil
		}

		arr, ok := v.(*types.Array)
		if !ok {
			return nil, false, newOperatorError(
				ErrArgsInvalidLen,
				op,
				fmt.Sprintf("All operands of %s must be arrays. One argument is of type: %s", op, handlerparams.AliasFromType(v)),
			)
		}

		elems := make([]any, 0, arr.Len())

		iter := arr.Iterator()

		for {
			_, elem, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				iter.Close()
				return nil, false, lazyerrors.Error(err)
			}

			elems = append(elems, elem)
		}

		iter.Close()

		sets = append(sets, elems)
	}

	return sets, false, nil
}

// containsValue reports whether set contains a value equal to v (by value equality).
func containsValue(set []any, v any) bool {
	for _, e := range set {
		if types.Compare(e, v) == types.Equal {
			return true
		}
	}

	return false
}

// appendUnique appends every value that is not already present (by value
// equality) to dst and returns the result.
func appendUnique(dst []any, values ...any) []any {
	for _, v := range values {
		if !containsValue(dst, v) {
			dst = append(dst, v)
		}
	}

	return dst
}

// sameSet reports whether two deduplicated sets contain the same values.
func sameSet(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}

	for _, v := range a {
		if !containsValue(b, v) {
			return false
		}
	}

	return true
}

// sortedArray returns a types.Array with the given values sorted in ascending
// order, matching how MongoDB returns set operator results.
func sortedArray(values []any) *types.Array {
	sort.SliceStable(values, func(i, j int) bool {
		return types.CompareOrderForSort(values[i], values[j], types.Ascending) == types.Less
	})

	res := types.MakeArray(len(values))
	res.Append(values...)

	return res
}

// check interfaces
var (
	_ Operator = (*setUnion)(nil)
	_ Operator = (*setIntersection)(nil)
	_ Operator = (*setDifference)(nil)
	_ Operator = (*setEquals)(nil)
	_ Operator = (*setIsSubset)(nil)
)
