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

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// switchBranch is a single `case`/`then` pair of the `$switch` operator.
type switchBranch struct {
	caseExpr any
	thenExpr any
}

// switchOp represents `$switch` operator.
type switchOp struct {
	defaultExpr any
	branches    []switchBranch
	hasDefault  bool
}

// newSwitch returns `$switch` operator.
//
// The specification has the shape
// `{branches: [{case: <e>, then: <e>}, ...], default: <e>}`.
// The `then` of the first branch whose `case` evaluates to a truthy value is
// returned; if no branch matches, `default` is returned, or an error is raised
// when there is no `default`.
func newSwitch(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$switch",
			fmt.Sprintf("Expression $switch takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$switch",
			fmt.Sprintf("$switch requires an object as an argument, found: %s", handlerparams.AliasFromType(args[0])),
		)
	}

	branchesValue, err := spec.Get("branches")
	if err != nil {
		return nil, newOperatorError(ErrArgsInvalidLen, "$switch", "$switch requires at least one branch.")
	}

	branchesArr, ok := branchesValue.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$switch",
			fmt.Sprintf("$switch expected an array for 'branches', found: %s", handlerparams.AliasFromType(branchesValue)),
		)
	}

	if branchesArr.Len() == 0 {
		return nil, newOperatorError(ErrArgsInvalidLen, "$switch", "$switch requires at least one branch.")
	}

	var branches []switchBranch

	iter := branchesArr.Iterator()
	defer iter.Close()

	for {
		_, v, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		branchDoc, ok := v.(*types.Document)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$switch",
				fmt.Sprintf("$switch expected each branch to be an object, found: %s", handlerparams.AliasFromType(v)),
			)
		}

		caseExpr, err := branchDoc.Get("case")
		if err != nil {
			return nil, newOperatorError(ErrArgsInvalidLen, "$switch", "$switch requires each branch have a 'case' expression")
		}

		thenExpr, err := branchDoc.Get("then")
		if err != nil {
			return nil, newOperatorError(ErrArgsInvalidLen, "$switch", "$switch requires each branch have a 'then' expression.")
		}

		branches = append(branches, switchBranch{caseExpr: caseExpr, thenExpr: thenExpr})
	}

	res := &switchOp{branches: branches}

	if spec.Has("default") {
		res.defaultExpr, _ = spec.Get("default")
		res.hasDefault = true
	}

	return res, nil
}

// Process implements Operator interface.
func (o *switchOp) Process(doc *types.Document) (any, error) {
	for _, branch := range o.branches {
		v, err := evaluateExpression(branch.caseExpr, doc)
		if err != nil {
			return nil, err
		}

		if isTruthy(v) {
			return evaluateExpression(branch.thenExpr, doc)
		}
	}

	if o.hasDefault {
		return evaluateExpression(o.defaultExpr, doc)
	}

	if isValidationDoc(doc) {
		// tolerate the `$project` validation dry-run, where every field path
		// resolves to null and therefore no branch can match
		return types.Null, nil
	}

	return nil, newOperatorError(
		ErrArgsInvalidLen,
		"$switch",
		"$switch could not find a matching branch for an input, and no default was specified.",
	)
}

// isValidationDoc reports whether doc is the sentinel document used by the
// `$project`/`$addFields` operator validation dry-run (`{key: "value"}`).
func isValidationDoc(doc *types.Document) bool {
	if doc.Len() != 1 {
		return false
	}

	v, err := doc.Get("key")
	if err != nil {
		return false
	}

	s, ok := v.(string)

	return ok && s == "value"
}

// check interfaces
var (
	_ Operator = (*switchOp)(nil)
)
