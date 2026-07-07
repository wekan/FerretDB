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
	"math/rand"

	"github.com/FerretDB/FerretDB/internal/types"
)

// literal represents `$literal` operator.
type literal struct {
	value any
}

// newLiteral returns `$literal` operator.
//
// It returns its argument verbatim, without evaluating it as an expression, so
// `{$literal: "$x"}` yields the string "$x" rather than the value of field `x`.
//
// Because a document array argument is flattened into multiple positional
// arguments before the operator is constructed, a multi-argument (or empty)
// argument list is reassembled into an array to preserve the original value.
func newLiteral(args ...any) (Operator, error) {
	if len(args) == 1 {
		return &literal{value: args[0]}, nil
	}

	arr := types.MakeArray(len(args))
	for _, a := range args {
		arr.Append(a)
	}

	return &literal{value: arr}, nil
}

// Process implements Operator interface.
func (o *literal) Process(doc *types.Document) (any, error) {
	return o.value, nil
}

// randOp represents `$rand` operator.
type randOp struct{}

// newRand returns `$rand` operator that returns a random double in [0, 1).
//
// It takes an empty object as its argument (`{$rand: {}}`).
func newRand(args ...any) (Operator, error) {
	return &randOp{}, nil
}

// Process implements Operator interface.
func (o *randOp) Process(doc *types.Document) (any, error) {
	return rand.Float64(), nil
}

// check interfaces
var (
	_ Operator = (*literal)(nil)
	_ Operator = (*randOp)(nil)
)
