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
	"math"

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations/operators"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// stdDev represents the $stdDevPop and $stdDevSamp aggregation accumulators.
//
// Population divides by n, sample by n-1; that is the only difference. Only
// numeric values take part, as in $avg. MongoDB answers Null when there is
// nothing numeric to measure, and $stdDevSamp of a single value is Null as well -
// the sample deviation of one sample is not zero, it is undefined.
//
// The sum of squared differences is accumulated in one pass with Welford's
// method, which does not lose the small differences that (sum of squares) minus
// (square of sum) loses on values that are large and close together.
type stdDev struct {
	expression *aggregations.Expression
	operator   operators.Operator
	number     any
	sample     bool
}

// newStdDevPop creates a new $stdDevPop aggregation accumulator.
func newStdDevPop(args ...any) (Accumulator, error) { return newStdDev(false, "$stdDevPop", args...) }

// newStdDevSamp creates a new $stdDevSamp aggregation accumulator.
func newStdDevSamp(args ...any) (Accumulator, error) { return newStdDev(true, "$stdDevSamp", args...) }

// newStdDev creates $stdDevPop (sample=false) or $stdDevSamp (sample=true).
func newStdDev(sample bool, name string, args ...any) (Accumulator, error) {
	accumulator := &stdDev{sample: sample}

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
			accumulator.expression = nil
		}
	case float64, int32, int64:
		accumulator.number = arg
	default:
		// A non-numeric constant measures nothing.
	}

	return accumulator, nil
}

// Accumulate implements Accumulator interface.
func (s *stdDev) Accumulate(iter types.DocumentsIterator) (any, error) {
	var n int64

	var mean, m2 float64

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
		case s.operator != nil:
			if value, err = s.operator.Process(doc); err != nil {
				return nil, err
			}
		case s.expression != nil:
			if value, err = s.expression.Evaluate(doc); err != nil {
				continue
			}
		case s.number != nil:
			value = s.number
		default:
			continue
		}

		var x float64

		switch value := value.(type) {
		case float64:
			x = value
		case int32:
			x = float64(value)
		case int64:
			x = float64(value)
		default:
			// Not a number: takes no part.
			continue
		}

		// Welford: mean and the sum of squared differences, one value at a time.
		n++
		delta := x - mean
		mean += delta / float64(n)
		m2 += delta * (x - mean)
	}

	switch {
	case n == 0:
		return types.Null, nil
	case s.sample && n < 2:
		// The sample deviation of a single sample is undefined, not zero.
		return types.Null, nil
	case !s.sample && n == 1:
		return float64(0), nil
	}

	divisor := float64(n)
	if s.sample {
		divisor = float64(n - 1)
	}

	return math.Sqrt(m2 / divisor), nil
}

// check interfaces
var (
	_ Accumulator = (*stdDev)(nil)
)
