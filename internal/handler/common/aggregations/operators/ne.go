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

// ne represents `$ne` operator.
type ne struct {
	args []any
}

// newNe returns `$ne` operator that compares two evaluated operands for inequality.
func newNe(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$ne",
			fmt.Sprintf("Expression $ne takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &ne{args: args}, nil
}

// Process implements Operator interface.
func (o *ne) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	return types.Compare(first, second) != types.Equal, nil
}

// check interfaces
var (
	_ Operator = (*ne)(nil)
)
