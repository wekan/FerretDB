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

package stages

import (
	"context"
	"errors"
	"fmt"

	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// replaceRoot represents both the $replaceRoot and the $replaceWith stages.
//
//	{ $replaceRoot: { newRoot: <expression> } }
//	{ $replaceWith: <expression> }
//
// For each input document the newRoot expression is evaluated. The result must
// be a document, which replaces the current root. A non-document result is an error.
type replaceRoot struct {
	// newRoot is the expression that must evaluate to a document.
	newRoot any

	// alias is the stage name used in error messages ("$replaceRoot" or "$replaceWith").
	alias string
}

// newReplaceRoot creates a new $replaceRoot stage.
func newReplaceRoot(stage *types.Document) (aggregations.Stage, error) {
	fields, err := common.GetRequiredParam[*types.Document](stage, "$replaceRoot")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"expected an object as specification for $replaceRoot stage",
			"$replaceRoot (stage)",
		)
	}

	newRoot, err := fields.Get("newRoot")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$replaceRoot requires an object with a 'newRoot' field",
			"$replaceRoot (stage)",
		)
	}

	return &replaceRoot{
		newRoot: newRoot,
		alias:   "$replaceRoot",
	}, nil
}

// newReplaceWith creates a new $replaceWith stage, the shorthand form of $replaceRoot.
func newReplaceWith(stage *types.Document) (aggregations.Stage, error) {
	newRoot, err := stage.Get("$replaceWith")
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return &replaceRoot{
		newRoot: newRoot,
		alias:   "$replaceWith",
	}, nil
}

// Process implements Stage interface.
func (r *replaceRoot) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	res := make([]*types.Document, 0, len(docs))

	for _, doc := range docs {
		v, found, err := evaluateExpressionValue(r.newRoot, doc)
		if err != nil {
			return nil, err
		}

		newRootDoc, ok := v.(*types.Document)
		if !found || !ok {
			formatted := "missing"
			if found {
				formatted = types.FormatAnyValue(v)
			}

			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				fmt.Sprintf(
					"'newRoot' expression must evaluate to an object, but resulting value was: %s",
					formatted,
				),
				r.alias+" (stage)",
			)
		}

		res = append(res, newRootDoc)
	}

	iter = iterator.Values(iterator.ForSlice(res))
	closer.Add(iter)

	return iter, nil
}

// evaluateExpressionValue evaluates an aggregation expression value against the given
// document. It returns the evaluated value and a boolean reporting whether the value
// was found (a missing field path evaluates to not-found).
func evaluateExpressionValue(expr any, doc *types.Document) (any, bool, error) {
	switch expr := expr.(type) {
	case *types.Document:
		v, err := evaluateDocument(expr, doc, false)
		if err != nil {
			return nil, false, err
		}

		return v, true, nil
	case string:
		expression, err := aggregations.NewExpression(expr, nil)

		var exprErr *aggregations.ExpressionError
		if errors.As(err, &exprErr) && exprErr.Code() == aggregations.ErrNotExpression {
			// a plain string literal
			return expr, true, nil
		}

		if err != nil {
			return nil, false, processGroupStageError(err)
		}

		v, err := expression.Evaluate(doc)
		if err != nil {
			// non-existent field path evaluates to "missing"
			return nil, false, nil
		}

		return v, true, nil
	default:
		return expr, true, nil
	}
}

// check interfaces
var (
	_ aggregations.Stage = (*replaceRoot)(nil)
)
