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

// not represents `$not` operator.
type not struct {
	arg any
}

// newNot returns `$not` operator that returns the boolean negation of the
// truthiness of its single operand.
//
// The operand may be given either as a single-element array (`{$not: [<e>]}`)
// or as a single non-array expression (`{$not: <e>}`).
func newNot(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$not",
			fmt.Sprintf("Expression $not takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	return &not{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *not) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	return !isTruthy(v), nil
}

// check interfaces
var (
	_ Operator = (*not)(nil)
)
