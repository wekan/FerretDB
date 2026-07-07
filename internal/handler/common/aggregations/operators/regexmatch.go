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

// regexMatch represents `$regexMatch` operator.
type regexMatch struct {
	input   any
	regex   any
	options any
}

// newRegexMatch returns `$regexMatch` operator that reports whether the input
// string matches a regular expression.
//
// It takes a single object argument `{input: <e>, regex: <e>, options?: <e>}`.
// The regex may be a string pattern or a BSON regex value; options is a string
// of the flag letters `i`, `m` and `s` (`x` is accepted by the syntax but not
// implemented by the Go regexp engine, matching FerretDB's `$regex` query
// operator behaviour).
func newRegexMatch(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$regexMatch",
			fmt.Sprintf("Expression $regexMatch takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$regexMatch expects an object of named arguments but found: %s", handlerparams.AliasFromType(args[0])),
			"$regexMatch",
		)
	}

	op := &regexMatch{}

	input, err := spec.Get("input")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$regexMatch requires 'input' parameter",
			"$regexMatch",
		)
	}

	op.input = input

	regex, err := spec.Get("regex")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$regexMatch requires 'regex' parameter",
			"$regexMatch",
		)
	}

	op.regex = regex

	if options, err := spec.Get("options"); err == nil {
		op.options = options
	}

	return op, nil
}

// Process implements Operator interface.
func (o *regexMatch) Process(doc *types.Document) (any, error) {
	inputVal, err := evaluateExpression(o.input, doc)
	if err != nil {
		return nil, err
	}

	input, inputNull, inputString := stringValue(inputVal)
	if inputNull {
		return false, nil
	}

	if !inputString {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$regexMatch needs 'input' to be of type string, found: %s", handlerparams.AliasFromType(inputVal)),
			"$regexMatch",
		)
	}

	pattern, options, isNull, err := o.resolveRegex(doc)
	if err != nil {
		return nil, err
	}

	if isNull {
		return false, nil
	}

	re := types.Regex{Pattern: pattern, Options: options}

	for _, opt := range options {
		switch opt {
		case 'i', 'm', 's':
			// supported by the Go regexp engine
		case 'x':
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrNotImplemented,
				"$regexMatch does not support the 'x' regex option",
				"$regexMatch",
			)
		default:
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrBadValue,
				fmt.Sprintf("$regexMatch invalid flag in regex options: %c", opt),
				"$regexMatch",
			)
		}
	}

	compiled, err := re.Compile()
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrBadValue,
			fmt.Sprintf("$regexMatch failed to compile regex: %s", err.Error()),
			"$regexMatch",
		)
	}

	return compiled.MatchString(input), nil
}

// resolveRegex evaluates the regex and options operands and returns the pattern
// and combined option letters. isNull is true when the regex resolves to null.
func (o *regexMatch) resolveRegex(doc *types.Document) (pattern, options string, isNull bool, err error) {
	regexVal, err := evaluateExpression(o.regex, doc)
	if err != nil {
		return "", "", false, err
	}

	switch r := regexVal.(type) {
	case types.Regex:
		pattern = r.Pattern
		options = r.Options
	case string:
		pattern = r
	case types.NullType:
		return "", "", true, nil
	default:
		return "", "", false, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$regexMatch needs 'regex' to be of type string or regex, found: %s", handlerparams.AliasFromType(regexVal)),
			"$regexMatch",
		)
	}

	if o.options == nil {
		return pattern, options, false, nil
	}

	optionsVal, err := evaluateExpression(o.options, doc)
	if err != nil {
		return "", "", false, err
	}

	switch opt := optionsVal.(type) {
	case types.NullType:
		// no additional options
	case string:
		if options != "" && opt != "" {
			return "", "", false, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrBadValue,
				"$regexMatch found regex option(s) specified in both 'regex' and 'options' fields",
				"$regexMatch",
			)
		}

		options += opt
	default:
		return "", "", false, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$regexMatch needs 'options' to be of type string, found: %s", handlerparams.AliasFromType(optionsVal)),
			"$regexMatch",
		)
	}

	return pattern, options, false, nil
}

// check interfaces
var (
	_ Operator = (*regexMatch)(nil)
)
