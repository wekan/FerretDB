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

// ifNull represents `$ifNull` operator.
type ifNull struct {
	args []any
}

// newIfNull returns `$ifNull` operator.
//
// It supports the MongoDB 5.0+ multi-input form: all operands but the last one
// are inputs, and the last operand is the replacement returned when every input
// evaluates to null or missing.
func newIfNull(args ...any) (Operator, error) {
	if len(args) < 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$ifNull",
			fmt.Sprintf("$ifNull needs at least two arguments, had: %d", len(args)),
		)
	}

	return &ifNull{args: args}, nil
}

// Process implements Operator interface.
func (o *ifNull) Process(doc *types.Document) (any, error) {
	// the last operand is the replacement, all others are inputs
	for i := 0; i < len(o.args)-1; i++ {
		v, err := evaluateExpression(o.args[i], doc)
		if err != nil {
			return nil, err
		}

		if _, isNull := v.(types.NullType); !isNull {
			return v, nil
		}
	}

	replacement, err := evaluateExpression(o.args[len(o.args)-1], doc)
	if err != nil {
		return nil, err
	}

	return replacement, nil
}

// check interfaces
var (
	_ Operator = (*ifNull)(nil)
)
