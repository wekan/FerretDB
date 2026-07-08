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

// newSin returns `$sin` operator.
func newSin(args ...any) (Operator, error) {
	return newMathOp("$sin", false, math.Sin)(args...)
}

// newCos returns `$cos` operator.
func newCos(args ...any) (Operator, error) {
	return newMathOp("$cos", false, math.Cos)(args...)
}

// newTan returns `$tan` operator.
func newTan(args ...any) (Operator, error) {
	return newMathOp("$tan", false, math.Tan)(args...)
}

// newAsin returns `$asin` operator.
func newAsin(args ...any) (Operator, error) {
	return newMathOp("$asin", false, math.Asin)(args...)
}

// newAcos returns `$acos` operator.
func newAcos(args ...any) (Operator, error) {
	return newMathOp("$acos", false, math.Acos)(args...)
}

// newAtan returns `$atan` operator.
func newAtan(args ...any) (Operator, error) {
	return newMathOp("$atan", false, math.Atan)(args...)
}

// newSinh returns `$sinh` operator.
func newSinh(args ...any) (Operator, error) {
	return newMathOp("$sinh", false, math.Sinh)(args...)
}

// newCosh returns `$cosh` operator.
func newCosh(args ...any) (Operator, error) {
	return newMathOp("$cosh", false, math.Cosh)(args...)
}

// newTanh returns `$tanh` operator.
func newTanh(args ...any) (Operator, error) {
	return newMathOp("$tanh", false, math.Tanh)(args...)
}

// newAsinh returns `$asinh` operator.
func newAsinh(args ...any) (Operator, error) {
	return newMathOp("$asinh", false, math.Asinh)(args...)
}

// newAcosh returns `$acosh` operator.
func newAcosh(args ...any) (Operator, error) {
	return newMathOp("$acosh", false, math.Acosh)(args...)
}

// newAtanh returns `$atanh` operator.
func newAtanh(args ...any) (Operator, error) {
	return newMathOp("$atanh", false, math.Atanh)(args...)
}

// newLog10 returns `$log10` operator.
func newLog10(args ...any) (Operator, error) {
	return newMathOp("$log10", false, math.Log10)(args...)
}

// newDegreesToRadians returns `$degreesToRadians` operator.
func newDegreesToRadians(args ...any) (Operator, error) {
	return newMathOp("$degreesToRadians", false, func(x float64) float64 {
		return x * math.Pi / 180
	})(args...)
}

// newRadiansToDegrees returns `$radiansToDegrees` operator.
func newRadiansToDegrees(args ...any) (Operator, error) {
	return newMathOp("$radiansToDegrees", false, func(x float64) float64 {
		return x * 180 / math.Pi
	})(args...)
}

// atan2Op represents the `$atan2` operator: `{$atan2: [<y>, <x>]}`.
type atan2Op struct {
	y any
	x any
}

// newAtan2 returns `$atan2` operator that returns the arc tangent of y/x,
// using the signs of the arguments to determine the quadrant of the result.
func newAtan2(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$atan2",
			fmt.Sprintf("Expression $atan2 takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &atan2Op{y: args[0], x: args[1]}, nil
}

// Process implements Operator interface.
func (o *atan2Op) Process(doc *types.Document) (any, error) {
	y, err := evaluateExpression(o.y, doc)
	if err != nil {
		return nil, err
	}

	x, err := evaluateExpression(o.x, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(y) || isNullValue(x) {
		return types.Null, nil
	}

	if !numberValue(y) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$atan2 only supports numeric types, not %s", handlerparams.AliasFromType(y)),
			"$atan2",
		)
	}

	if !numberValue(x) {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$atan2 only supports numeric types, not %s", handlerparams.AliasFromType(x)),
			"$atan2",
		)
	}

	return math.Atan2(toFloat64(y), toFloat64(x)), nil
}

// check interfaces
var (
	_ Operator = (*atan2Op)(nil)
)
