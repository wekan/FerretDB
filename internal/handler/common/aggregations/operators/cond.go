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

	"github.com/FerretDB/FerretDB/internal/types"
)

// cond represents `$cond` operator.
type cond struct {
	ifExpr   any
	thenExpr any
	elseExpr any
}

// newCond returns `$cond` operator.
//
// It supports both the array form `{$cond: [<if>, <then>, <else>]}` and the
// object form `{$cond: {if: <e>, then: <e>, else: <e>}}`. It evaluates `if` and
// returns `then` when it is truthy, otherwise `else`.
func newCond(args ...any) (Operator, error) {
	switch len(args) {
	case 3:
		return &cond{ifExpr: args[0], thenExpr: args[1], elseExpr: args[2]}, nil
	case 1:
		spec, ok := args[0].(*types.Document)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$cond",
				fmt.Sprintf("Expression $cond takes exactly 3 arguments. %d were passed in.", len(args)),
			)
		}

		ifExpr, err := spec.Get("if")
		if err != nil {
			return nil, newOperatorError(ErrArgsInvalidLen, "$cond", "Missing 'if' parameter to $cond")
		}

		thenExpr, err := spec.Get("then")
		if err != nil {
			return nil, newOperatorError(ErrArgsInvalidLen, "$cond", "Missing 'then' parameter to $cond")
		}

		elseExpr, err := spec.Get("else")
		if err != nil {
			return nil, newOperatorError(ErrArgsInvalidLen, "$cond", "Missing 'else' parameter to $cond")
		}

		return &cond{ifExpr: ifExpr, thenExpr: thenExpr, elseExpr: elseExpr}, nil
	default:
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$cond",
			fmt.Sprintf("Expression $cond takes exactly 3 arguments. %d were passed in.", len(args)),
		)
	}
}

// Process implements Operator interface.
func (o *cond) Process(doc *types.Document) (any, error) {
	condition, err := evaluateExpression(o.ifExpr, doc)
	if err != nil {
		return nil, err
	}

	if isTruthy(condition) {
		return evaluateExpression(o.thenExpr, doc)
	}

	return evaluateExpression(o.elseExpr, doc)
}

// check interfaces
var (
	_ Operator = (*cond)(nil)
)
