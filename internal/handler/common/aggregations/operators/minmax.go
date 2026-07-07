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

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// minMaxAvg represents the `$min`, `$max` and `$avg` expression operators.
type minMaxAvg struct {
	name string
	args []any
}

// newMinMaxAvg returns a constructor for the `$min`, `$max` or `$avg` expression
// operator with the given name.
func newMinMaxAvg(name string) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		return &minMaxAvg{name: name, args: args}, nil
	}
}

// newMax returns `$max` expression operator.
func newMax(args ...any) (Operator, error) {
	return newMinMaxAvg("$max")(args...)
}

// newMin returns `$min` expression operator.
func newMin(args ...any) (Operator, error) {
	return newMinMaxAvg("$min")(args...)
}

// newAvg returns `$avg` expression operator.
func newAvg(args ...any) (Operator, error) {
	return newMinMaxAvg("$avg")(args...)
}

// values evaluates the operator arguments and flattens them into a list of
// values. When called with a single argument that resolves to an array, the
// array elements are used; otherwise the evaluated arguments are used directly.
func (o *minMaxAvg) values(doc *types.Document) ([]any, error) {
	evaluated := make([]any, 0, len(o.args))

	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		evaluated = append(evaluated, v)
	}

	if len(evaluated) == 1 {
		arr, ok := evaluated[0].(*types.Array)
		if !ok {
			return evaluated, nil
		}

		res := make([]any, 0, arr.Len())

		iter := arr.Iterator()
		defer iter.Close()

		for {
			_, v, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				return nil, lazyerrors.Error(err)
			}

			res = append(res, v)
		}

		return res, nil
	}

	return evaluated, nil
}

// Process implements Operator interface.
func (o *minMaxAvg) Process(doc *types.Document) (any, error) {
	values, err := o.values(doc)
	if err != nil {
		return nil, err
	}

	if o.name == "$avg" {
		return avg(values), nil
	}

	return minMax(o.name, values), nil
}

// minMax returns the minimum (for `$min`) or maximum (for `$max`) of values,
// ignoring null and missing values. It returns Null for an empty result.
func minMax(name string, values []any) any {
	var best any

	found := false

	for _, v := range values {
		if isNullValue(v) {
			continue
		}

		if !found {
			best = v
			found = true

			continue
		}

		cmp := types.CompareForAggregation(v, best)

		if name == "$max" && cmp == types.Greater {
			best = v
		}

		if name == "$min" && cmp == types.Less {
			best = v
		}
	}

	if !found {
		return types.Null
	}

	return best
}

// avg returns the average of the numeric values, ignoring non-numeric, null and
// missing values. It returns Null when there are no numeric values.
func avg(values []any) any {
	var sum float64

	count := 0

	for _, v := range values {
		if !numberValue(v) {
			continue
		}

		sum += toFloat64(v)
		count++
	}

	if count == 0 {
		return types.Null
	}

	return sum / float64(count)
}

// check interfaces
var (
	_ Operator = (*minMaxAvg)(nil)
)
