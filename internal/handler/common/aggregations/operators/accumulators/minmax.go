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

// minMax represents the $min and $max aggregation accumulators.
//
// Both are the same walk over the group with one comparison flipped, so they are
// one type: whichever of the accumulated values sorts lowest (or highest) in
// MongoDB's total ordering of BSON types, which is what types.CompareOrder
// implements. Documents where the expression resolves to nothing are skipped -
// they are not "smaller than everything" - and a group where nothing resolved
// returns Null, as MongoDB does.
type minMax struct {
	expression *aggregations.Expression
	operator   operators.Operator
	value      any
	max        bool
}

// newMin creates a new $min aggregation accumulator.
func newMin(args ...any) (Accumulator, error) { return newMinMax(false, "$min", args...) }

// newMax creates a new $max aggregation accumulator.
func newMax(args ...any) (Accumulator, error) { return newMinMax(true, "$max", args...) }

func newMinMax(max bool, name string, args ...any) (Accumulator, error) {
	accumulator := &minMax{max: max}

	if len(args) != 1 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrStageGroupUnaryOperator,
			"The "+name+" accumulator is a unary operator",
			name+" (accumulator)",
		)
	}

	switch arg := args[0].(type) {
	case *types.Document:
		if !operators.IsOperator(arg) {
			accumulator.value = arg
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
		expr, err := aggregations.NewExpression(arg, nil)
		if err != nil {
			// Not a field path ("$x") but a plain string: the same constant for
			// every document, so that constant is the answer.
			accumulator.value = arg
			break
		}

		accumulator.expression = expr
	default:
		// A constant of any other type - the answer is that constant.
		accumulator.value = arg
	}

	return accumulator, nil
}

// Accumulate implements Accumulator interface.
func (m *minMax) Accumulate(iter types.DocumentsIterator) (any, error) {
	var found any
	var any_ bool

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
		case m.operator != nil:
			if value, err = m.operator.Process(doc); err != nil {
				return nil, err
			}
		case m.expression != nil:
			// A document that does not have the field takes no part: $min of a
			// field only some documents have is the smallest of those.
			if value, err = m.expression.Evaluate(doc); err != nil {
				continue
			}
		default:
			value = m.value
		}

		// Missing is skipped; an explicit null is a value, and sorts below numbers.
		if value == nil {
			continue
		}

		if !any_ {
			found, any_ = value, true
			continue
		}

		cmp := types.CompareOrder(value, found, types.Ascending)
		if (m.max && cmp == types.Greater) || (!m.max && cmp == types.Less) {
			found = value
		}
	}

	if !any_ {
		// MongoDB answers null for a group in which nothing resolved.
		return types.Null, nil
	}

	return found, nil
}

// check interfaces
var (
	_ Accumulator = (*minMax)(nil)
)
