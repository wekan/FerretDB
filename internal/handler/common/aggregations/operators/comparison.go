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

// comparison represents the `$gt`, `$gte`, `$lt` and `$lte` operators.
//
// They all compare two evaluated operands and return a boolean; they differ
// only by the set of comparison results that are considered a match.
type comparison struct {
	name  string
	args  []any
	match map[types.CompareResult]struct{}
}

// newComparison returns a constructor for the comparison operator with the
// given name that returns true when the result of comparing its two operands is
// one of match.
func newComparison(name string, match map[types.CompareResult]struct{}) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 2 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 2 arguments. %d were passed in.", name, len(args)),
			)
		}

		return &comparison{name: name, args: args, match: match}, nil
	}
}

// newGt returns `$gt` operator.
func newGt(args ...any) (Operator, error) {
	return newComparison("$gt", map[types.CompareResult]struct{}{types.Greater: {}})(args...)
}

// newGte returns `$gte` operator.
func newGte(args ...any) (Operator, error) {
	return newComparison("$gte", map[types.CompareResult]struct{}{types.Greater: {}, types.Equal: {}})(args...)
}

// newLt returns `$lt` operator.
func newLt(args ...any) (Operator, error) {
	return newComparison("$lt", map[types.CompareResult]struct{}{types.Less: {}})(args...)
}

// newLte returns `$lte` operator.
func newLte(args ...any) (Operator, error) {
	return newComparison("$lte", map[types.CompareResult]struct{}{types.Less: {}, types.Equal: {}})(args...)
}

// Process implements Operator interface.
func (o *comparison) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	_, ok := o.match[types.Compare(first, second)]

	return ok, nil
}

// check interfaces
var (
	_ Operator = (*comparison)(nil)
)
