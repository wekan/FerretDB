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

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
)

// binarySize represents `$binarySize` operator.
type binarySize struct {
	arg any
}

// newBinarySize returns `$binarySize` operator that returns the number of bytes
// in a string (its UTF-8 byte length) or in binary data.
func newBinarySize(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$binarySize",
			fmt.Sprintf("Expression $binarySize takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &binarySize{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *binarySize) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	switch v := v.(type) {
	case types.NullType:
		return types.Null, nil
	case string:
		return int32(len(v)), nil
	case types.Binary:
		return int32(len(v.B)), nil
	default:
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$binarySize requires a string or binData argument, found: %s", handlerparams.AliasFromType(v)),
			"$binarySize",
		)
	}
}

// check interfaces
var (
	_ Operator = (*binarySize)(nil)
)
