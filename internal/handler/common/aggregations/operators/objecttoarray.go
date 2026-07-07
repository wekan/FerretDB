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
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// objectToArray represents `$objectToArray` operator.
type objectToArray struct {
	arg any
}

// newObjectToArray returns `$objectToArray` operator.
//
// It takes a single argument which must evaluate to a document; it returns an
// array of `{k, v}` documents preserving the original field order.
func newObjectToArray(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$objectToArray",
			fmt.Sprintf("Expression $objectToArray takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &objectToArray{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *objectToArray) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	if _, isNull := v.(types.NullType); isNull {
		return types.Null, nil
	}

	d, ok := v.(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$objectToArray",
			fmt.Sprintf("$objectToArray requires a document input, but got %s", handlerparams.AliasFromType(v)),
		)
	}

	res := types.MakeArray(d.Len())

	iter := d.Iterator()
	defer iter.Close()

	for {
		k, val, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		res.Append(must.NotFail(types.NewDocument("k", k, "v", val)))
	}

	return res, nil
}

// check interfaces
var (
	_ Operator = (*objectToArray)(nil)
)
