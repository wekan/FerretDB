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

// firstLast represents the $first and $last aggregation accumulators.
//
// They are the value of the expression in the first (or last) document of the
// group AS IT ARRIVES - which is why a pipeline that wants a meaningful answer
// puts a $sort before the $group. A document where the expression resolves to
// nothing still counts as a document: the answer for it is Null, not the value
// from the next document along.
type firstLast struct {
	expression *aggregations.Expression
	operator   operators.Operator
	value      any
	last       bool
}

// newFirst creates a new $first aggregation accumulator.
func newFirst(args ...any) (Accumulator, error) { return newFirstLast(false, "$first", args...) }

// newLast creates a new $last aggregation accumulator.
func newLast(args ...any) (Accumulator, error) { return newFirstLast(true, "$last", args...) }

// newFirstLast creates $first (last=false) or $last (last=true).
func newFirstLast(last bool, name string, args ...any) (Accumulator, error) {
	accumulator := &firstLast{last: last}

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
			// A plain string, not a field path: the same constant everywhere.
			accumulator.value = arg
			break
		}

		accumulator.expression = expr
	default:
		accumulator.value = arg
	}

	return accumulator, nil
}

// Accumulate implements Accumulator interface.
func (f *firstLast) Accumulate(iter types.DocumentsIterator) (any, error) {
	var found any = types.Null

	var seen bool

	for {
		_, doc, err := iter.Next()

		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		if seen && !f.last {
			// $first has its answer, but the iterator must still be drained.
			continue
		}

		var value any = types.Null

		switch {
		case f.operator != nil:
			if value, err = f.operator.Process(doc); err != nil {
				return nil, err
			}
		case f.expression != nil:
			if value, err = f.expression.Evaluate(doc); err != nil {
				value = types.Null
			}
		default:
			value = f.value
		}

		found, seen = value, true
	}

	return found, nil
}

// check interfaces
var (
	_ Accumulator = (*firstLast)(nil)
)
