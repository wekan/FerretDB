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
	"github.com/FerretDB/FerretDB/internal/types"
)

// and represents `$and` operator.
type and struct {
	args []any
}

// newAnd returns `$and` operator that returns true only when every operand is
// truthy. It short-circuits and returns false on the first falsy operand.
func newAnd(args ...any) (Operator, error) {
	return &and{args: args}, nil
}

// Process implements Operator interface.
func (o *and) Process(doc *types.Document) (any, error) {
	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		if !isTruthy(v) {
			return false, nil
		}
	}

	return true, nil
}

// check interfaces
var (
	_ Operator = (*and)(nil)
)
