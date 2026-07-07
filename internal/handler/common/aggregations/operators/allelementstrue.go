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

// allElementsTrue represents `$allElementsTrue` operator.
type allElementsTrue struct {
	arg any
}

// newAllElementsTrue returns `$allElementsTrue` operator.
//
// It takes a single argument which must evaluate to an array; it returns true
// when every element of that array is truthy (an empty array yields true).
func newAllElementsTrue(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$allElementsTrue",
			fmt.Sprintf("Expression $allElementsTrue takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &allElementsTrue{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *allElementsTrue) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	if _, isNull := v.(types.NullType); isNull {
		// tolerate the validation dry-run, where the argument resolves to null
		return true, nil
	}

	arr, ok := v.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$allElementsTrue",
			fmt.Sprintf("$allElementsTrue's argument must be an array, but is %s", handlerparams.AliasFromType(v)),
		)
	}

	iter := arr.Iterator()
	defer iter.Close()

	for {
		_, elem, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		if !isTruthy(elem) {
			return false, nil
		}
	}

	return true, nil
}

// check interfaces
var (
	_ Operator = (*allElementsTrue)(nil)
)
