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

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
)

// numberValue reports whether v is a numeric BSON value (int32, int64 or float64).
func numberValue(v any) bool {
	switch v.(type) {
	case int32, int64, float64:
		return true
	default:
		return false
	}
}

// isNullValue reports whether v is Null (or a missing value resolved to Null).
func isNullValue(v any) bool {
	_, ok := v.(types.NullType)

	return ok
}

// toFloat64 converts a numeric BSON value to float64.
// It panics for non-numeric values, so numberValue must be checked first.
func toFloat64(v any) float64 {
	switch v := v.(type) {
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	default:
		panic(fmt.Sprintf("not a number: %T", v))
	}
}

// add represents `$add` operator.
type add struct {
	args []any
}

// newAdd returns `$add` operator that returns the sum of its numeric operands.
func newAdd(args ...any) (Operator, error) {
	return &add{args: args}, nil
}

// Process implements Operator interface.
func (o *add) Process(doc *types.Document) (any, error) {
	numbers := make([]any, 0, len(o.args))

	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(v) {
			return types.Null, nil
		}

		if !numberValue(v) {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				fmt.Sprintf("$add only supports numeric or date types, not %s", handlerparams.AliasFromType(v)),
				"$add",
			)
		}

		numbers = append(numbers, v)
	}

	return aggregations.SumNumbers(numbers...), nil
}

// subtract represents `$subtract` operator.
type subtract struct {
	args []any
}

// newSubtract returns `$subtract` operator that subtracts the second operand from the first.
func newSubtract(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$subtract",
			fmt.Sprintf("Expression $subtract takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &subtract{args: args}, nil
}

// Process implements Operator interface.
func (o *subtract) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(first) || isNullValue(second) {
		return types.Null, nil
	}

	if !numberValue(first) || !numberValue(second) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf(
				"can't $subtract %s from %s",
				handlerparams.AliasFromType(second), handlerparams.AliasFromType(first),
			),
			"$subtract",
		)
	}

	if _, ok := first.(float64); ok {
		return toFloat64(first) - toFloat64(second), nil
	}

	if _, ok := second.(float64); ok {
		return toFloat64(first) - toFloat64(second), nil
	}

	return intResult(intValue(first)-intValue(second), first, second), nil
}

// multiply represents `$multiply` operator.
type multiply struct {
	args []any
}

// newMultiply returns `$multiply` operator that returns the product of its numeric operands.
func newMultiply(args ...any) (Operator, error) {
	return &multiply{args: args}, nil
}

// Process implements Operator interface.
func (o *multiply) Process(doc *types.Document) (any, error) {
	values := make([]any, 0, len(o.args))

	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(v) {
			return types.Null, nil
		}

		if !numberValue(v) {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				fmt.Sprintf("$multiply only supports numeric types, not %s", handlerparams.AliasFromType(v)),
				"$multiply",
			)
		}

		values = append(values, v)
	}

	if len(values) == 0 {
		return int32(0), nil
	}

	hasFloat := false

	for _, v := range values {
		if _, ok := v.(float64); ok {
			hasFloat = true
		}
	}

	if hasFloat {
		product := 1.0
		for _, v := range values {
			product *= toFloat64(v)
		}

		return product, nil
	}

	product := int64(1)
	for _, v := range values {
		product *= intValue(v)
	}

	return intResult(product, values...), nil
}

// divide represents `$divide` operator.
type divide struct {
	args []any
}

// newDivide returns `$divide` operator that divides the first operand by the second.
func newDivide(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$divide",
			fmt.Sprintf("Expression $divide takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &divide{args: args}, nil
}

// Process implements Operator interface.
func (o *divide) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(first) || isNullValue(second) {
		return types.Null, nil
	}

	if !numberValue(first) || !numberValue(second) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"$divide only supports numeric types",
			"$divide",
		)
	}

	divisor := toFloat64(second)
	if divisor == 0 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrBadValue,
			"can't $divide by zero",
			"$divide",
		)
	}

	return toFloat64(first) / divisor, nil
}

// mod represents `$mod` operator.
type mod struct {
	args []any
}

// newMod returns `$mod` operator that returns the remainder of dividing the first operand by the second.
func newMod(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$mod",
			fmt.Sprintf("Expression $mod takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &mod{args: args}, nil
}

// Process implements Operator interface.
func (o *mod) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(first) || isNullValue(second) {
		return types.Null, nil
	}

	if !numberValue(first) || !numberValue(second) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"$mod only supports numeric types",
			"$mod",
		)
	}

	divisor := toFloat64(second)
	if divisor == 0 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrBadValue,
			"can't $mod by zero",
			"$mod",
		)
	}

	_, firstFloat := first.(float64)
	_, secondFloat := second.(float64)

	if firstFloat || secondFloat {
		return math.Mod(toFloat64(first), divisor), nil
	}

	return intResult(intValue(first)%intValue(second), first, second), nil
}

// intValue returns the int64 value of an int32 or int64 value.
// It panics for other types, so numberValue must be checked first.
func intValue(v any) int64 {
	switch v := v.(type) {
	case int32:
		return int64(v)
	case int64:
		return int64(v)
	default:
		panic(fmt.Sprintf("not an integer: %T", v))
	}
}

// intResult returns result typed as int32 when all operands are int32 and the
// result fits into int32, otherwise it returns int64. It mirrors how MongoDB
// widens integer arithmetic results.
func intResult(result int64, operands ...any) any {
	hasInt64 := false

	for _, v := range operands {
		if _, ok := v.(int64); ok {
			hasInt64 = true
		}
	}

	if !hasInt64 && result <= math.MaxInt32 && result >= math.MinInt32 {
		return int32(result)
	}

	return result
}

// check interfaces
var (
	_ Operator = (*add)(nil)
	_ Operator = (*subtract)(nil)
	_ Operator = (*multiply)(nil)
	_ Operator = (*divide)(nil)
	_ Operator = (*mod)(nil)
)
