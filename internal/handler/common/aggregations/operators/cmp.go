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

// cmp represents `$cmp` operator.
type cmp struct {
	args []any
}

// newCmp returns `$cmp` operator that compares two evaluated operands.
//
// It returns -1 if the first operand is less than the second, 0 if they are
// equal and 1 if the first operand is greater than the second.
func newCmp(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$cmp",
			fmt.Sprintf("Expression $cmp takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &cmp{args: args}, nil
}

// Process implements Operator interface.
func (o *cmp) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	switch types.Compare(first, second) {
	case types.Less:
		return int32(-1), nil
	case types.Greater:
		return int32(1), nil
	case types.Equal:
		return int32(0), nil
	default:
		return int32(0), nil
	}
}

// check interfaces
var (
	_ Operator = (*cmp)(nil)
)
