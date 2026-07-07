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
	"fmt"
	"math"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
)

// mathOp represents a single-argument math operator such as `$abs`, `$ceil`,
// `$floor`, `$trunc`, `$sqrt`, `$exp`, `$ln` and `$log10`.
type mathOp struct {
	name string
	arg  any
	// preserveInt keeps the integer type of an integer input (used by `$abs`,
	// `$ceil`, `$floor` and `$trunc`).
	preserveInt bool
	compute     func(float64) float64
}

// newMathOp returns a constructor for the single-argument math operator with the
// given name computing compute over its numeric operand.
func newMathOp(name string, preserveInt bool, compute func(float64) float64) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 argument. %d were passed in.", name, len(args)),
			)
		}

		return &mathOp{name: name, arg: args[0], preserveInt: preserveInt, compute: compute}, nil
	}
}

// newAbs returns `$abs` operator.
func newAbs(args ...any) (Operator, error) {
	return newMathOp("$abs", true, math.Abs)(args...)
}

// newCeil returns `$ceil` operator.
func newCeil(args ...any) (Operator, error) {
	return newMathOp("$ceil", true, math.Ceil)(args...)
}

// newFloor returns `$floor` operator.
func newFloor(args ...any) (Operator, error) {
	return newMathOp("$floor", true, math.Floor)(args...)
}

// newTrunc returns `$trunc` single-argument form; the two-argument form is
// handled together with `$round` in newRound-like logic below.
func newTrunc(args ...any) (Operator, error) {
	return newRounding("$trunc", math.Trunc)(args...)
}

// newSqrt returns `$sqrt` operator.
func newSqrt(args ...any) (Operator, error) {
	return newMathOp("$sqrt", false, math.Sqrt)(args...)
}

// newExp returns `$exp` operator.
func newExp(args ...any) (Operator, error) {
	return newMathOp("$exp", false, math.Exp)(args...)
}

// newLn returns `$ln` operator.
func newLn(args ...any) (Operator, error) {
	return newMathOp("$ln", false, math.Log)(args...)
}

// Process implements Operator interface.
func (o *mathOp) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(v) {
		return types.Null, nil
	}

	if !numberValue(v) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s only supports numeric types, not %s", o.name, handlerparams.AliasFromType(v)),
			o.name,
		)
	}

	if o.preserveInt {
		switch v := v.(type) {
		case int32:
			return int32(int64(o.compute(float64(v)))), nil
		case int64:
			return int64(o.compute(float64(v))), nil
		}
	}

	return o.compute(toFloat64(v)), nil
}

// rounding represents the `$round` and `$trunc` operators which accept an
// optional place argument: `{$round: [<number>, <place>]}`.
type rounding struct {
	name    string
	number  any
	place   any
	compute func(float64) float64
}

// newRounding returns a constructor for a rounding operator (`$round` or
// `$trunc`) with the given name using compute for the zero-place operation.
func newRounding(name string, compute func(float64) float64) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes at least 1 argument, and at most 2 arguments.", name),
			)
		}

		op := &rounding{name: name, number: args[0], compute: compute}
		if len(args) == 2 {
			op.place = args[1]
		}

		return op, nil
	}
}

// newRound returns `$round` operator.
func newRound(args ...any) (Operator, error) {
	return newRounding("$round", math.Round)(args...)
}

// Process implements Operator interface.
func (o *rounding) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.number, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(v) {
		return types.Null, nil
	}

	if !numberValue(v) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s only supports numeric types, not %s", o.name, handlerparams.AliasFromType(v)),
			o.name,
		)
	}

	place := int64(0)

	if o.place != nil {
		p, err := evaluateExpression(o.place, doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(p) {
			return types.Null, nil
		}

		if !numberValue(p) {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				fmt.Sprintf("%s requires a numeric place, not %s", o.name, handlerparams.AliasFromType(p)),
				o.name,
			)
		}

		place = int64(toFloat64(p))
	}

	factor := math.Pow(10, float64(place))
	rounded := o.compute(toFloat64(v)*factor) / factor

	switch v := v.(type) {
	case int32:
		if place >= 0 {
			return v, nil
		}

		return int32(int64(rounded)), nil
	case int64:
		if place >= 0 {
			return v, nil
		}

		return int64(rounded), nil
	default:
		return rounded, nil
	}
}

// logOp represents the `$log` operator: `{$log: [<number>, <base>]}`.
type logOp struct {
	number any
	base   any
}

// newLog returns `$log` operator that returns the log of a number in the given base.
func newLog(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$log",
			fmt.Sprintf("Expression $log takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &logOp{number: args[0], base: args[1]}, nil
}

// Process implements Operator interface.
func (o *logOp) Process(doc *types.Document) (any, error) {
	number, err := evaluateExpression(o.number, doc)
	if err != nil {
		return nil, err
	}

	base, err := evaluateExpression(o.base, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(number) || isNullValue(base) {
		return types.Null, nil
	}

	if !numberValue(number) || !numberValue(base) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"$log only supports numeric types",
			"$log",
		)
	}

	return math.Log(toFloat64(number)) / math.Log(toFloat64(base)), nil
}

// powOp represents the `$pow` operator: `{$pow: [<number>, <exponent>]}`.
type powOp struct {
	number   any
	exponent any
}

// newPow returns `$pow` operator that raises a number to the given exponent.
func newPow(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$pow",
			fmt.Sprintf("Expression $pow takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &powOp{number: args[0], exponent: args[1]}, nil
}

// Process implements Operator interface.
func (o *powOp) Process(doc *types.Document) (any, error) {
	number, err := evaluateExpression(o.number, doc)
	if err != nil {
		return nil, err
	}

	exponent, err := evaluateExpression(o.exponent, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(number) || isNullValue(exponent) {
		return types.Null, nil
	}

	if !numberValue(number) || !numberValue(exponent) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"$pow only supports numeric types",
			"$pow",
		)
	}

	result := math.Pow(toFloat64(number), toFloat64(exponent))

	// MongoDB returns an integer when both the base and exponent are integers
	// and the exponent is non-negative.
	_, numberFloat := number.(float64)
	_, exponentFloat := exponent.(float64)

	if !numberFloat && !exponentFloat && toFloat64(exponent) >= 0 {
		return intResult(int64(result), number, exponent), nil
	}

	return result, nil
}

// check interfaces
var (
	_ Operator = (*mathOp)(nil)
	_ Operator = (*rounding)(nil)
	_ Operator = (*logOp)(nil)
	_ Operator = (*powOp)(nil)
)
