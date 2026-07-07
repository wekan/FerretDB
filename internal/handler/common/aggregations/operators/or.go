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

// or represents `$or` operator.
type or struct {
	args []any
}

// newOr returns `$or` operator that returns true when any operand is truthy.
func newOr(args ...any) (Operator, error) {
	return &or{args: args}, nil
}

// Process implements Operator interface.
func (o *or) Process(doc *types.Document) (any, error) {
	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		if isTruthy(v) {
			return true, nil
		}
	}

	return false, nil
}

// check interfaces
var (
	_ Operator = (*or)(nil)
)
