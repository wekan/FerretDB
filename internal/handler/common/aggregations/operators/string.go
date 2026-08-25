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
	"strings"
	"unicode/utf8"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
)

// stringValue reports the string form of a BSON value evaluated by an operator.
//
// isNull is true for Null (and missing values resolved to Null); isString is
// true when v is a string. When isString is true, s holds the string.
func stringValue(v any) (s string, isNull bool, isString bool) {
	switch v := v.(type) {
	case string:
		return v, false, true
	case types.NullType:
		return "", true, false
	default:
		return "", false, false
	}
}

// stringTypeError returns a MongoDB-style type mismatch error for an operator
// that requires a string operand.
func stringTypeError(name string, v any) error {
	return handlererrors.NewCommandErrorMsgWithArgument(
		handlererrors.ErrTypeMismatch,
		fmt.Sprintf("%s requires a string argument, found: %s", name, handlerparams.AliasFromType(v)),
		name,
	)
}

// concat represents `$concat` operator.
type concat struct {
	args []any
}

// newConcat returns `$concat` operator that concatenates its string operands.
//
// If any operand resolves to null or missing the result is null; a non-string,
// non-null operand is an error.
func newConcat(args ...any) (Operator, error) {
	return &concat{args: args}, nil
}

// Process implements Operator interface.
func (o *concat) Process(doc *types.Document) (any, error) {
	var sb strings.Builder

	hasNull := false

	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		s, isNull, isString := stringValue(v)

		switch {
		case isNull:
			hasNull = true
		case isString:
			sb.WriteString(s)
		default:
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrTypeMismatch,
				fmt.Sprintf("$concat only supports strings, not %s", handlerparams.AliasFromType(v)),
				"$concat",
			)
		}
	}

	if hasNull {
		return types.Null, nil
	}

	return sb.String(), nil
}

// caseConv represents the `$toUpper` and `$toLower` operators.
type caseConv struct {
	name  string
	arg   any
	upper bool
}

// newToUpper returns `$toUpper` operator.
func newToUpper(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$toUpper",
			fmt.Sprintf("Expression $toUpper takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	return &caseConv{name: "$toUpper", arg: args[0], upper: true}, nil
}

// newToLower returns `$toLower` operator.
func newToLower(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$toLower",
			fmt.Sprintf("Expression $toLower takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	return &caseConv{name: "$toLower", arg: args[0], upper: false}, nil
}

// Process implements Operator interface.
func (o *caseConv) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	s, isNull, isString := stringValue(v)
	if isNull {
		return "", nil
	}

	if !isString {
		return nil, stringTypeError(o.name, v)
	}

	if o.upper {
		return strings.ToUpper(s), nil
	}

	return strings.ToLower(s), nil
}

// strLen represents the `$strLenCP` and `$strLenBytes` operators.
type strLen struct {
	name  string
	arg   any
	bytes bool
}

// newStrLenCP returns `$strLenCP` operator returning the number of UTF-8 code points.
func newStrLenCP(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$strLenCP",
			fmt.Sprintf("Expression $strLenCP takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	return &strLen{name: "$strLenCP", arg: args[0], bytes: false}, nil
}

// newStrLenBytes returns `$strLenBytes` operator returning the number of bytes.
func newStrLenBytes(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$strLenBytes",
			fmt.Sprintf("Expression $strLenBytes takes exactly 1 argument. %d were passed in.", len(args)),
		)
	}

	return &strLen{name: "$strLenBytes", arg: args[0], bytes: true}, nil
}

// Process implements Operator interface.
func (o *strLen) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	s, isNull, isString := stringValue(v)
	if isNull {
		return int32(0), nil
	}

	if !isString {
		return nil, stringTypeError(o.name, v)
	}

	if o.bytes {
		return int32(len(s)), nil
	}

	return int32(utf8.RuneCountInString(s)), nil
}

// strcasecmp represents `$strcasecmp` operator.
type strcasecmp struct {
	args []any
}

// newStrcasecmp returns `$strcasecmp` operator that compares two strings
// case-insensitively, returning -1, 0 or 1.
func newStrcasecmp(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$strcasecmp",
			fmt.Sprintf("Expression $strcasecmp takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &strcasecmp{args: args}, nil
}

// Process implements Operator interface.
func (o *strcasecmp) Process(doc *types.Document) (any, error) {
	first, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	second, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	a, aNull, aString := stringValue(first)
	if !aNull && !aString {
		return nil, stringTypeError("$strcasecmp", first)
	}

	b, bNull, bString := stringValue(second)
	if !bNull && !bString {
		return nil, stringTypeError("$strcasecmp", second)
	}

	switch res := strings.Compare(strings.ToUpper(a), strings.ToUpper(b)); {
	case res < 0:
		return int32(-1), nil
	case res > 0:
		return int32(1), nil
	default:
		return int32(0), nil
	}
}

// substr represents the `$substr`, `$substrBytes` and `$substrCP` operators.
type substr struct {
	name string
	args []any
	// codePoints selects code-point offsets (`$substrCP`) rather than byte
	// offsets (`$substr`, `$substrBytes`).
	codePoints bool
}

// newSubstrBytes returns `$substrBytes` operator (also used for `$substr`).
func newSubstrBytes(name string) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 3 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 3 arguments. %d were passed in.", name, len(args)),
			)
		}

		return &substr{name: name, args: args, codePoints: false}, nil
	}
}

// newSubstr returns `$substr` operator, an alias of `$substrBytes`.
func newSubstr(args ...any) (Operator, error) {
	return newSubstrBytes("$substr")(args...)
}

// newSubstrBytesOp returns `$substrBytes` operator.
func newSubstrBytesOp(args ...any) (Operator, error) {
	return newSubstrBytes("$substrBytes")(args...)
}

// newSubstrCP returns `$substrCP` operator working on code points.
func newSubstrCP(args ...any) (Operator, error) {
	if len(args) != 3 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$substrCP",
			fmt.Sprintf("Expression $substrCP takes exactly 3 arguments. %d were passed in.", len(args)),
		)
	}

	return &substr{name: "$substrCP", args: args, codePoints: true}, nil
}

// Process implements Operator interface.
func (o *substr) Process(doc *types.Document) (any, error) {
	strVal, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	s, isNull, isString := stringValue(strVal)
	if isNull {
		return "", nil
	}

	if !isString {
		return nil, stringTypeError(o.name, strVal)
	}

	start, err := o.intArg(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	length, err := o.intArg(o.args[2], doc)
	if err != nil {
		return nil, err
	}

	if start < 0 {
		return "", nil
	}

	if start > int64(len(s)) {
		return "", nil
	}

	if o.codePoints {
		return substrRunes(s, start, length), nil
	}

	return substrBytes(s, start, length), nil
}

// intArg evaluates arg and returns it as an int64, requiring a whole numeric value.
func (o *substr) intArg(arg any, doc *types.Document) (int64, error) {
	v, err := evaluateExpression(arg, doc)
	if err != nil {
		return 0, err
	}

	if isNullValue(v) {
		return 0, nil
	}

	n, err := handlerparams.GetWholeNumberParam(v)
	if err != nil {
		return 0, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s requires numeric starting and length arguments, found: %s", o.name, handlerparams.AliasFromType(v)),
			o.name,
		)
	}

	return n, nil
}

// substrBytes returns the byte-offset substring of s starting at start with the
// given length; a negative length means the rest of the string.
func substrBytes(s string, start, length int64) string {
	if start >= int64(len(s)) {
		return ""
	}

	end := len(s)
	startInt := int(start)
	if length >= 0 && length < int64(end-startInt) {
		end = startInt + int(length)
	}

	return s[startInt:end]
}

// substrRunes returns the code-point-offset substring of s starting at start
// with the given length; a negative length means the rest of the string.
func substrRunes(s string, start, length int64) string {
	runes := []rune(s)

	if start >= int64(len(runes)) {
		return ""
	}

	end := len(runes)
	startInt := int(start)
	if length >= 0 && length < int64(end-startInt) {
		end = startInt + int(length)
	}

	return string(runes[startInt:end])
}

// split represents `$split` operator.
type split struct {
	args []any
}

// newSplit returns `$split` operator that splits a string on a non-empty delimiter.
func newSplit(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$split",
			fmt.Sprintf("Expression $split takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &split{args: args}, nil
}

// Process implements Operator interface.
func (o *split) Process(doc *types.Document) (any, error) {
	strVal, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	s, isNull, isString := stringValue(strVal)
	if isNull {
		return types.Null, nil
	}

	if !isString {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$split requires an expression that evaluates to a string as a first argument, found: %s",
				handlerparams.AliasFromType(strVal)),
			"$split",
		)
	}

	delimVal, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(delimVal) {
		return types.Null, nil
	}

	delim, _, isString := stringValue(delimVal)
	if !isString {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("$split requires an expression that evaluates to a string as a second argument, found: %s",
				handlerparams.AliasFromType(delimVal)),
			"$split",
		)
	}

	if delim == "" {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrBadValue,
			"$split requires a non-empty separator",
			"$split",
		)
	}

	parts := strings.Split(s, delim)

	res := types.MakeArray(len(parts))
	for _, p := range parts {
		res.Append(p)
	}

	return res, nil
}

// trim represents the `$trim`, `$ltrim` and `$rtrim` operators.
type trim struct {
	name  string
	input any
	chars any
	left  bool
	right bool
}

// newTrimOp returns a constructor for a trim operator with the given name that
// trims from the left and/or right.
func newTrimOp(name string, left, right bool) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 argument. %d were passed in.", name, len(args)),
			)
		}

		spec, ok := args[0].(*types.Document)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("%s only supports an object as an argument, found %s", name, handlerparams.AliasFromType(args[0])),
				name,
			)
		}

		input, err := spec.Get("input")
		if err != nil {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("%s requires an 'input' field", name),
				name,
			)
		}

		op := &trim{name: name, input: input, left: left, right: right}

		if chars, err := spec.Get("chars"); err == nil {
			op.chars = chars
		}

		return op, nil
	}
}

// newTrim returns `$trim` operator.
func newTrim(args ...any) (Operator, error) {
	return newTrimOp("$trim", true, true)(args...)
}

// newLtrim returns `$ltrim` operator.
func newLtrim(args ...any) (Operator, error) {
	return newTrimOp("$ltrim", true, false)(args...)
}

// newRtrim returns `$rtrim` operator.
func newRtrim(args ...any) (Operator, error) {
	return newTrimOp("$rtrim", false, true)(args...)
}

// asciiWhitespace is the default set of characters trimmed by the trim operators.
const asciiWhitespace = " \t\n\v\f\r"

// Process implements Operator interface.
func (o *trim) Process(doc *types.Document) (any, error) {
	inputVal, err := evaluateExpression(o.input, doc)
	if err != nil {
		return nil, err
	}

	s, isNull, isString := stringValue(inputVal)
	if isNull {
		return types.Null, nil
	}

	if !isString {
		return nil, stringTypeError(o.name, inputVal)
	}

	cutset := asciiWhitespace

	if o.chars != nil {
		charsVal, err := evaluateExpression(o.chars, doc)
		if err != nil {
			return nil, err
		}

		c, charsNull, charsString := stringValue(charsVal)

		switch {
		case charsNull:
			return types.Null, nil
		case charsString:
			cutset = c
		default:
			return nil, stringTypeError(o.name, charsVal)
		}
	}

	switch {
	case o.left && o.right:
		return strings.Trim(s, cutset), nil
	case o.left:
		return strings.TrimLeft(s, cutset), nil
	default:
		return strings.TrimRight(s, cutset), nil
	}
}

// indexOf represents the `$indexOfCP` and `$indexOfBytes` operators.
type indexOf struct {
	name string
	args []any
	// codePoints selects code-point offsets (`$indexOfCP`) rather than byte
	// offsets (`$indexOfBytes`).
	codePoints bool
}

// newIndexOfCP returns `$indexOfCP` operator.
func newIndexOfCP(args ...any) (Operator, error) {
	return newIndexOf("$indexOfCP", true)(args...)
}

// newIndexOfBytes returns `$indexOfBytes` operator.
func newIndexOfBytes(args ...any) (Operator, error) {
	return newIndexOf("$indexOfBytes", false)(args...)
}

// newIndexOf returns a constructor for an index-of operator with the given name.
func newIndexOf(name string, codePoints bool) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) < 2 || len(args) > 4 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes at least 2 arguments, and at most 4 arguments.", name),
			)
		}

		return &indexOf{name: name, args: args, codePoints: codePoints}, nil
	}
}

// Process implements Operator interface.
func (o *indexOf) Process(doc *types.Document) (any, error) {
	strVal, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	s, isNull, isString := stringValue(strVal)
	if isNull {
		return types.Null, nil
	}

	if !isString {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s requires a string as the first argument, found: %s", o.name, handlerparams.AliasFromType(strVal)),
			o.name,
		)
	}

	subVal, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	sub, subNull, subString := stringValue(subVal)
	if subNull {
		return int32(-1), nil
	}

	if !subString {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("%s requires a string as the second argument, found: %s", o.name, handlerparams.AliasFromType(subVal)),
			o.name,
		)
	}

	if o.codePoints {
		return o.indexCP([]rune(s), []rune(sub), doc)
	}

	return o.indexBytes(s, sub, doc)
}

// bounds evaluates the optional start and end arguments, returning them clamped
// to [0, length].
func (o *indexOf) bounds(length int, doc *types.Document) (start, end int, err error) {
	start = 0
	end = length

	if len(o.args) >= 3 {
		v, err := evaluateExpression(o.args[2], doc)
		if err != nil {
			return 0, 0, err
		}

		if !isNullValue(v) {
			n, convErr := handlerparams.GetWholeNumberParam(v)
			if convErr != nil {
				return 0, 0, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrTypeMismatch,
					fmt.Sprintf("%s requires an integral starting index", o.name),
					o.name,
				)
			}

			switch {
			case n < 0:
				start = 0
			case n > int64(length):
				start = length
			default:
				start = int(n)
			}
		}
	}

	if len(o.args) >= 4 {
		v, err := evaluateExpression(o.args[3], doc)
		if err != nil {
			return 0, 0, err
		}

		if !isNullValue(v) {
			n, convErr := handlerparams.GetWholeNumberParam(v)
			if convErr != nil {
				return 0, 0, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrTypeMismatch,
					fmt.Sprintf("%s requires an integral ending index", o.name),
					o.name,
				)
			}

			switch {
			case n < 0:
				end = 0
			case n > int64(length):
				end = length
			default:
				end = int(n)
			}
		}
	}

	if start < 0 {
		start = 0
	}

	if start > length {
		start = length
	}

	if end > length {
		end = length
	}

	if end < start {
		end = start
	}

	return start, end, nil
}

// indexBytes returns the byte index of the first occurrence of sub in s within
// the requested bounds, or -1.
func (o *indexOf) indexBytes(s, sub string, doc *types.Document) (any, error) {
	start, end, err := o.bounds(len(s), doc)
	if err != nil {
		return nil, err
	}

	idx := strings.Index(s[start:end], sub)
	if idx < 0 {
		return int32(-1), nil
	}

	return int32(start + idx), nil
}

// indexCP returns the code-point index of the first occurrence of sub in s
// within the requested bounds, or -1.
func (o *indexOf) indexCP(s, sub []rune, doc *types.Document) (any, error) {
	start, end, err := o.bounds(len(s), doc)
	if err != nil {
		return nil, err
	}

	if len(sub) == 0 {
		return int32(start), nil
	}

	for i := start; i+len(sub) <= end; i++ {
		if string(s[i:i+len(sub)]) == string(sub) {
			return int32(i), nil
		}
	}

	return int32(-1), nil
}

// replace represents the `$replaceOne` and `$replaceAll` operators.
type replace struct {
	name        string
	input       any
	find        any
	replacement any
	all         bool
}

// newReplaceOp returns a constructor for a replace operator with the given name.
func newReplaceOp(name string, all bool) newOperatorFunc {
	return func(args ...any) (Operator, error) {
		if len(args) != 1 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				name,
				fmt.Sprintf("Expression %s takes exactly 1 argument. %d were passed in.", name, len(args)),
			)
		}

		spec, ok := args[0].(*types.Document)
		if !ok {
			return nil, handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrFailedToParse,
				fmt.Sprintf("%s requires an object as an argument, found: %s", name, handlerparams.AliasFromType(args[0])),
				name,
			)
		}

		op := &replace{name: name, all: all}

		for _, field := range []struct {
			key string
			dst *any
		}{
			{"input", &op.input},
			{"find", &op.find},
			{"replacement", &op.replacement},
		} {
			v, err := spec.Get(field.key)
			if err != nil {
				return nil, handlererrors.NewCommandErrorMsgWithArgument(
					handlererrors.ErrFailedToParse,
					fmt.Sprintf("%s requires '%s' to be specified", name, field.key),
					name,
				)
			}

			*field.dst = v
		}

		return op, nil
	}
}

// newReplaceOne returns `$replaceOne` operator.
func newReplaceOne(args ...any) (Operator, error) {
	return newReplaceOp("$replaceOne", false)(args...)
}

// newReplaceAll returns `$replaceAll` operator.
func newReplaceAll(args ...any) (Operator, error) {
	return newReplaceOp("$replaceAll", true)(args...)
}

// Process implements Operator interface.
func (o *replace) Process(doc *types.Document) (any, error) {
	inputVal, err := evaluateExpression(o.input, doc)
	if err != nil {
		return nil, err
	}

	input, inputNull, inputString := stringValue(inputVal)
	if !inputNull && !inputString {
		return nil, stringTypeError(o.name, inputVal)
	}

	findVal, err := evaluateExpression(o.find, doc)
	if err != nil {
		return nil, err
	}

	find, findNull, findString := stringValue(findVal)
	if !findNull && !findString {
		return nil, stringTypeError(o.name, findVal)
	}

	replacementVal, err := evaluateExpression(o.replacement, doc)
	if err != nil {
		return nil, err
	}

	replacement, replacementNull, replacementString := stringValue(replacementVal)
	if !replacementNull && !replacementString {
		return nil, stringTypeError(o.name, replacementVal)
	}

	if inputNull || findNull {
		return types.Null, nil
	}

	if o.all {
		return strings.ReplaceAll(input, find, replacement), nil
	}

	return strings.Replace(input, find, replacement, 1), nil
}

// check interfaces
var (
	_ Operator = (*concat)(nil)
	_ Operator = (*caseConv)(nil)
	_ Operator = (*strLen)(nil)
	_ Operator = (*strcasecmp)(nil)
	_ Operator = (*substr)(nil)
	_ Operator = (*split)(nil)
	_ Operator = (*trim)(nil)
	_ Operator = (*indexOf)(nil)
	_ Operator = (*replace)(nil)
)
