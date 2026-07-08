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

	"github.com/FerretDB/FerretDB/internal/bson"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// bsonSize represents `$bsonSize` operator.
type bsonSize struct {
	arg any
}

// newBsonSize returns `$bsonSize` operator that returns the size in bytes of the
// BSON encoding of a document, or null if the argument is null.
func newBsonSize(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$bsonSize",
			fmt.Sprintf("Expression $bsonSize takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &bsonSize{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *bsonSize) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	switch v := v.(type) {
	case *types.Document:
		wDoc, err := bson.FromDocument(v)
		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		raw, err := wDoc.Encode()
		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		return int32(len(raw)), nil
	case types.NullType:
		return types.Null, nil
	default:
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$bsonSize requires a document input, found: %s", handlerparams.AliasFromType(v)),
			"$bsonSize",
		)
	}
}

// check interfaces
var (
	_ Operator = (*bsonSize)(nil)
)
