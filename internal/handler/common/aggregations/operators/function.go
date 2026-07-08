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
	"errors"
	"fmt"

	"github.com/dop251/goja"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// function represents `$function` operator.
type function struct {
	body string
	args []any
}

// newFunction returns `$function` operator.
//
// The specification document has the shape
// `{body: <js function source>, args: [<expr>...], lang: "js"}`.
// `body` is required, `args` is optional (defaults to empty) and `lang`, when
// present, must be "js".
func newFunction(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			fmt.Sprintf("$function takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			"$function only supports an object as its argument",
		)
	}

	bodyVal, err := spec.Get("body")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			"$function requires 'body' to be specified",
		)
	}

	body, ok := bodyVal.(string)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			"$function requires 'body' to be a string or code",
		)
	}

	if langVal, err := spec.Get("lang"); err == nil {
		lang, ok := langVal.(string)
		if !ok || lang != "js" {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$function",
				"$function only supports 'js' as its language",
			)
		}
	}

	var fnArgs []any

	if argsVal, err := spec.Get("args"); err == nil {
		arr, ok := argsVal.(*types.Array)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$function",
				"$function requires 'args' to be an array",
			)
		}

		iter := arr.Iterator()
		defer iter.Close()

		for {
			_, v, err := iter.Next()
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			if err != nil {
				return nil, lazyerrors.Error(err)
			}

			fnArgs = append(fnArgs, v)
		}
	}

	return &function{body: body, args: fnArgs}, nil
}

// Process implements Operator interface.
func (o *function) Process(doc *types.Document) (any, error) {
	vm := goja.New()

	jsArgs := make([]goja.Value, 0, len(o.args))

	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		jsArgs = append(jsArgs, vm.ToValue(TypesToJS(v)))
	}

	val, err := vm.RunString("(" + o.body + ")")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			fmt.Sprintf("$function body failed to compile: %s", err),
		)
	}

	callable, ok := goja.AssertFunction(val)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			"$function body must be a function",
		)
	}

	res, err := callable(goja.Undefined(), jsArgs...)
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			fmt.Sprintf("$function body failed to execute: %s", err),
		)
	}

	result, err := JSToTypes(res)
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$function",
			fmt.Sprintf("$function returned an unsupported value: %s", err),
		)
	}

	return result, nil
}

// check interfaces
var (
	_ Operator = (*function)(nil)
)
