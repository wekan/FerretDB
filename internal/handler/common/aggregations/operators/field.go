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

// fieldName resolves the `field` argument of the field operators into a string.
//
// A plain string is used verbatim as a literal field name (field paths are not
// resolved); any other expression is evaluated and must yield a string.
func fieldName(name string, field any, doc *types.Document) (string, error) {
	if s, ok := field.(string); ok {
		return s, nil
	}

	v, err := evaluateExpression(field, doc)
	if err != nil {
		return "", err
	}

	s, ok := v.(string)
	if !ok {
		return "", handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s requires 'field' to evaluate to a string, but got %s", name, handlerparams.AliasFromType(v)),
			name,
		)
	}

	return s, nil
}

// getField represents `$getField` operator.
type getField struct {
	field any
	input any
	// hasInput reports whether an explicit `input` was provided; otherwise the
	// current (root) document is used.
	hasInput bool
}

// newGetField returns `$getField` operator.
//
// It accepts either the shorthand `{$getField: <string>}` or the full
// `{$getField: {field: <string>, input?: <expr>}}` form. It returns the value of
// `field` from `input` (defaulting to the current document), or null when absent.
func newGetField(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$getField",
			fmt.Sprintf("Expression $getField takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		// shorthand form: the argument is the field name itself.
		return &getField{field: args[0]}, nil
	}

	if !spec.Has("field") {
		// a plain document without a `field` key is treated as a field name only
		// when it is an operator expression; otherwise it is invalid.
		if IsOperator(spec) {
			return &getField{field: spec}, nil
		}

		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$getField requires 'field' to be specified",
			"$getField",
		)
	}

	field, _ := spec.Get("field")

	op := &getField{field: field}

	if spec.Has("input") {
		op.input, _ = spec.Get("input")
		op.hasInput = true
	}

	return op, nil
}

// Process implements Operator interface.
func (o *getField) Process(doc *types.Document) (any, error) {
	name, err := fieldName("$getField", o.field, doc)
	if err != nil {
		return nil, err
	}

	input := doc

	if o.hasInput {
		v, err := evaluateExpression(o.input, doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(v) {
			return types.Null, nil
		}

		d, ok := v.(*types.Document)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				fmt.Sprintf("$getField requires 'input' to evaluate to an object, but got %s", handlerparams.AliasFromType(v)),
				"$getField",
			)
		}

		input = d
	}

	res, err := input.Get(name)
	if err != nil {
		// a missing field resolves to null.
		return types.Null, nil
	}

	return res, nil
}

// setField represents the `$setField` and `$unsetField` operators.
type setField struct {
	name  string
	field any
	input any
	value any
	// unset selects `$unsetField` behaviour (remove the field) over `$setField`
	// (set the field to `value`).
	unset bool
}

// newSetFieldOp returns a constructor for a set/unset field operator.
func newSetFieldOp(name string, unset bool) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 arguments. %d were passed in.", name, len(args)),
			)
		}

		spec, ok := args[0].(*types.Document)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("%s only supports an object as its argument, but got %s", name, handlerparams.AliasFromType(args[0])),
				name,
			)
		}

		field, err := spec.Get("field")
		if err != nil {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("%s requires 'field' to be specified", name),
				name,
			)
		}

		input, err := spec.Get("input")
		if err != nil {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("%s requires 'input' to be specified", name),
				name,
			)
		}

		op := &setField{name: name, field: field, input: input, unset: unset}

		if !unset {
			value, err := spec.Get("value")
			if err != nil {
				return nil, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrFailedToParse,
					"$setField requires 'value' to be specified",
					name,
				)
			}

			op.value = value
		}

		return op, nil
	}
}

// newSetField returns `$setField` operator.
func newSetField(args ...any) (Operator, error) {
	return newSetFieldOp("$setField", false)(args...)
}

// newUnsetField returns `$unsetField` operator.
func newUnsetField(args ...any) (Operator, error) {
	return newSetFieldOp("$unsetField", true)(args...)
}

// Process implements Operator interface.
func (o *setField) Process(doc *types.Document) (any, error) {
	name, err := fieldName(o.name, o.field, doc)
	if err != nil {
		return nil, err
	}

	inputVal, err := evaluateExpression(o.input, doc)
	if err != nil {
		return nil, err
	}

	// null and missing input yield null, which also tolerates the $project dry-run.
	if isNullValue(inputVal) {
		return types.Null, nil
	}

	input, ok := inputVal.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s requires 'input' to evaluate to an object, but got %s", o.name, handlerparams.AliasFromType(inputVal)),
			o.name,
		)
	}

	res := input.DeepCopy()

	if o.unset {
		res.Remove(name)

		return res, nil
	}

	value, err := evaluateExpression(o.value, doc)
	if err != nil {
		return nil, err
	}

	res.Set(name, value)

	return res, nil
}

// check interfaces
var (
	_ Operator = (*getField)(nil)
	_ Operator = (*setField)(nil)
)
