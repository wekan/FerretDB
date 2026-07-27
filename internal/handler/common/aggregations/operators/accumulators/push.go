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
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// push represents the $push and $addToSet aggregation accumulators.
//
// $push collects the value of the expression from every document of the group, in
// the order the documents arrive; $addToSet is the same walk keeping only values
// that are not already there. A document where the expression resolves to nothing
// contributes nothing - the array is shorter, it does not gain a null.
type push struct {
	expression *aggregations.Expression
	operator   operators.Operator
	value      any
	set        bool
}

// newPush creates a new $push aggregation accumulator.
func newPush(args ...any) (Accumulator, error) { return newPushSet(false, "$push", args...) }

// newAddToSet creates a new $addToSet aggregation accumulator.
func newAddToSet(args ...any) (Accumulator, error) { return newPushSet(true, "$addToSet", args...) }

func newPushSet(set bool, name string, args ...any) (Accumulator, error) {
	accumulator := &push{set: set}

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
func (p *push) Accumulate(iter types.DocumentsIterator) (any, error) {
	array := types.MakeArray(0)

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
		case p.operator != nil:
			if value, err = p.operator.Process(doc); err != nil {
				return nil, err
			}
		case p.expression != nil:
			// The field is not there: this document adds nothing to the array.
			if value, err = p.expression.Evaluate(doc); err != nil {
				continue
			}
		default:
			value = p.value
		}

		if p.set && arrayContains(array, value) {
			continue
		}

		array.Append(value)
	}

	return array, nil
}

// arrayContains reports whether the array already holds an equal value, using the
// same equality the query language uses, so that 1 and 1.0 are one value.
func arrayContains(array *types.Array, value any) bool {
	for i := 0; i < array.Len(); i++ {
		if types.Compare(must.NotFail(array.Get(i)), value) == types.Equal {
			return true
		}
	}

	return false
}

// check interfaces
var (
	_ Accumulator = (*push)(nil)
)
