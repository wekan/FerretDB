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
	"fmt"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// letOp represents `$let` operator.
type letOp struct {
	vars *types.Document
	in   any
}

// newLet returns `$let` operator.
//
// The specification document has the shape `{vars: {<name>: <expr>, ...}, in: <expr>}`.
// Each variable expression is evaluated against the current document and bound
// to `$$<name>`; `in` is then evaluated with those variables substituted.
func newLet(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$let",
			fmt.Sprintf("Expression $let takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("$let only supports an object as its argument, but got %s", handlerparams.AliasFromType(args[0])),
			"$let",
		)
	}

	varsVal, err := spec.Get("vars")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"Missing 'vars' parameter to $let",
			"$let",
		)
	}

	vars, ok := varsVal.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("invalid parameter: expected an object (vars), but found %s", handlerparams.AliasFromType(varsVal)),
			"$let",
		)
	}

	in, err := spec.Get("in")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"Missing 'in' parameter to $let",
			"$let",
		)
	}

	return &letOp{vars: vars, in: in}, nil
}

// Process implements Operator interface.
func (o *letOp) Process(doc *types.Document) (any, error) {
	bound := o.in

	iter := o.vars.Iterator()
	defer iter.Close()

	for {
		name, expr, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		value, err := evaluateExpression(expr, doc)
		if err != nil {
			return nil, err
		}

		// bind `$$<name>` to the evaluated value by substituting it into the
		// (partially bound) `in` expression, reusing the $map/$filter approach.
		bound, err = bindVars(bound, name, value)
		if err != nil {
			return nil, err
		}
	}

	return evaluateExpression(bound, doc)
}

// check interfaces
var (
	_ Operator = (*letOp)(nil)
)
