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

package accumulators

import (
	"errors"

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations/operators"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// avg represents the $avg aggregation accumulator.
//
// Only NUMERIC values take part, and only where the expression resolves at all:
// $avg of a field that is a string in one document and a number in the others is
// the average of the numbers, and $avg of a group where nothing is numeric is
// Null - not zero, which would claim an average that does not exist.
type avg struct {
	expression *aggregations.Expression
	operator   operators.Operator
	number     any
}

// newAvg creates a new $avg aggregation accumulator.
func newAvg(args ...any) (Accumulator, error) {
	accumulator := new(avg)

	if len(args) != 1 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrStageGroupUnaryOperator,
			"The $avg accumulator is a unary operator",
			"$avg (accumulator)",
		)
	}

	switch arg := args[0].(type) {
	case *types.Document:
		if !operators.IsOperator(arg) {
			break
		}

		op, err := operators.NewOperator(arg)
		if err != nil {
			var opErr operators.OperatorError
			if !errors.As(err, &opErr) {
				return nil, lazyerrors.Error(err)
			}

			return nil, opErr
		}

		accumulator.operator = op
	case string:
		var err error
		if accumulator.expression, err = aggregations.NewExpression(arg, nil); err != nil {
			// A plain string is not numeric, so it contributes nothing.
			accumulator.expression = nil
		}
	case float64, int32, int64:
		accumulator.number = arg
	default:
		// Non-numeric constant: contributes nothing, exactly like a non-numeric field.
	}

	return accumulator, nil
}

// Accumulate implements Accumulator interface.
func (a *avg) Accumulate(iter types.DocumentsIterator) (any, error) {
	var numbers []any

	for {
		_, doc, err := iter.Next()

		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		var value any

		switch {
		case a.operator != nil:
			if value, err = a.operator.Process(doc); err != nil {
				return nil, err
			}
		case a.expression != nil:
			if value, err = a.expression.Evaluate(doc); err != nil {
				continue
			}
		case a.number != nil:
			value = a.number
		default:
			continue
		}

		switch value.(type) {
		case float64, int32, int64:
			numbers = append(numbers, value)
		default:
			// Not a number: takes no part, as in MongoDB.
		}
	}

	if len(numbers) == 0 {
		return types.Null, nil
	}

	sum := aggregations.SumNumbers(numbers...)

	var total float64

	switch sum := sum.(type) {
	case float64:
		total = sum
	case int32:
		total = float64(sum)
	case int64:
		total = float64(sum)
	default:
		return types.Null, nil
	}

	// The average of integers is a double in MongoDB, always.
	return total / float64(len(numbers)), nil
}

// check interfaces
var (
	_ Accumulator = (*avg)(nil)
)
