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
	"math"
	"strconv"

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// size represents `$size` operator.
type size struct {
	arg any
}

// newSize returns `$size` operator.
//
// It takes a single argument which must evaluate to an array; it returns the
// number of elements as an int32.
func newSize(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$size",
			fmt.Sprintf("Expression $size takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &size{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *size) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	// null is tolerated during the $project dry-run validation.
	if isNullValue(v) {
		return types.Null, nil
	}

	arr, ok := v.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$size",
			fmt.Sprintf("The argument to $size must be an array, but was of type: %s", handlerparams.AliasFromType(v)),
		)
	}

	return int32(arr.Len()), nil
}

// arrayElemAt represents `$arrayElemAt` operator.
type arrayElemAt struct {
	args []any
}

// newArrayElemAt returns `$arrayElemAt` operator.
//
// It takes two arguments: an array and an index. A negative index counts from
// the end of the array. An out-of-range index resolves to Null (MongoDB omits
// the field; the projection layer here has no missing-field channel).
func newArrayElemAt(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$arrayElemAt",
			fmt.Sprintf("Expression $arrayElemAt takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &arrayElemAt{args: args}, nil
}

// Process implements Operator interface.
func (o *arrayElemAt) Process(doc *types.Document) (any, error) {
	arrValue, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	idxValue, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(arrValue) || isNullValue(idxValue) {
		return types.Null, nil
	}

	arr, ok := arrValue.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$arrayElemAt",
			fmt.Sprintf("$arrayElemAt's first argument must be an array, but is %s", handlerparams.AliasFromType(arrValue)),
		)
	}

	idx, err := handlerparams.GetWholeNumberParam(idxValue)
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$arrayElemAt",
			fmt.Sprintf("$arrayElemAt's second argument must be a numeric value, but is %s", handlerparams.AliasFromType(idxValue)),
		)
	}

	length := int64(arr.Len())
	if idx < 0 {
		if idx < -length {
			return types.Null, nil
		}

		idx += length
	}

	if idx < 0 || idx >= length {
		// out of range resolves to a missing value.
		return types.Null, nil
	}

	i, err := strconv.Atoi(strconv.FormatInt(idx, 10))
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return must.NotFail(arr.Get(i)), nil
}

// concatArrays represents `$concatArrays` operator.
type concatArrays struct {
	args []any
}

// newConcatArrays returns `$concatArrays` operator.
//
// It concatenates its array arguments into a single array. If any argument
// resolves to Null, the result is Null.
func newConcatArrays(args ...any) (Operator, error) {
	return &concatArrays{args: args}, nil
}

// Process implements Operator interface.
func (o *concatArrays) Process(doc *types.Document) (any, error) {
	res := types.MakeArray(0)

	for _, arg := range o.args {
		v, err := evaluateExpression(arg, doc)
		if err != nil {
			return nil, err
		}

		if isNullValue(v) {
			return types.Null, nil
		}

		arr, ok := v.(*types.Array)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$concatArrays",
				fmt.Sprintf("$concatArrays only supports arrays, but got %s", handlerparams.AliasFromType(v)),
			)
		}

		if err = appendAll(res, arr); err != nil {
			return nil, err
		}
	}

	return res, nil
}

// isArray represents `$isArray` operator.
type isArray struct {
	arg any
}

// newIsArray returns `$isArray` operator.
//
// It takes a single argument and returns a boolean reporting whether it
// resolves to an array.
func newIsArray(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$isArray",
			fmt.Sprintf("Expression $isArray takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &isArray{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *isArray) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	_, ok := v.(*types.Array)

	return ok, nil
}

// inArray represents the aggregation `$in` operator.
//
// Note: this is the aggregation-expression form `{$in: [value, arrayExpr]}`,
// which is different from the query `$in` operator.
type inArray struct {
	args []any
}

// newIn returns the aggregation `$in` operator.
func newIn(args ...any) (Operator, error) {
	if len(args) != 2 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$in",
			fmt.Sprintf("Expression $in takes exactly 2 arguments. %d were passed in.", len(args)),
		)
	}

	return &inArray{args: args}, nil
}

// Process implements Operator interface.
func (o *inArray) Process(doc *types.Document) (any, error) {
	value, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	arrValue, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	// null is tolerated during the $project dry-run validation.
	if isNullValue(arrValue) {
		return false, nil
	}

	arr, ok := arrValue.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$in",
			fmt.Sprintf("$in requires an array as a second argument, found: %s", handlerparams.AliasFromType(arrValue)),
		)
	}

	iter := arr.Iterator()
	defer iter.Close()

	for {
		_, elem, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		if types.Compare(value, elem) == types.Equal {
			return true, nil
		}
	}

	return false, nil
}

// reverseArray represents `$reverseArray` operator.
type reverseArray struct {
	arg any
}

// newReverseArray returns `$reverseArray` operator.
func newReverseArray(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reverseArray",
			fmt.Sprintf("Expression $reverseArray takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &reverseArray{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *reverseArray) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(v) {
		return types.Null, nil
	}

	arr, ok := v.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$reverseArray",
			fmt.Sprintf("The argument to $reverseArray must be an array, but was of type: %s", handlerparams.AliasFromType(v)),
		)
	}

	res := types.MakeArray(arr.Len())

	for i := arr.Len() - 1; i >= 0; i-- {
		res.Append(must.NotFail(arr.Get(i)))
	}

	return res, nil
}

// sliceArray represents `$slice` operator.
type sliceArray struct {
	args []any
}

// newSlice returns `$slice` operator.
//
// The specification is `[array, n]` or `[array, position, n]`.
func newSlice(args ...any) (Operator, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$slice",
			fmt.Sprintf("Expression $slice takes at least 2 arguments, and at most 3, but %d were passed in.", len(args)),
		)
	}

	return &sliceArray{args: args}, nil
}

// Process implements Operator interface.
func (o *sliceArray) Process(doc *types.Document) (any, error) {
	arrValue, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(arrValue) {
		return types.Null, nil
	}

	arr, ok := arrValue.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$slice",
			fmt.Sprintf("First argument to $slice must be an array, but is of type: %s", handlerparams.AliasFromType(arrValue)),
		)
	}

	l := int64(arr.Len())

	var start, count int64

	if len(o.args) == 2 {
		n, err := o.wholeArg(o.args[1], doc)
		if err != nil {
			return nil, err
		}

		if n >= 0 {
			start = 0
			count = n
		} else {
			// negative n returns the last |n| elements.
			if n <= -l {
				start = 0
				count = l
			} else {
				start = l + n
				count = -n
			}
		}
	} else {
		position, err := o.wholeArg(o.args[1], doc)
		if err != nil {
			return nil, err
		}

		n, err := o.wholeArg(o.args[2], doc)
		if err != nil {
			return nil, err
		}

		if n < 0 {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$slice",
				"Third argument to $slice must be positive: "+fmt.Sprintf("%d", n),
			)
		}

		if position >= 0 {
			start = position
		} else {
			start = l + position
			if start < 0 {
				start = 0
			}
		}

		count = n
	}

	if start >= l || count == 0 {
		return types.MakeArray(0), nil
	}

	end := l
	if count < l-start {
		end = start + count
	}

	startInt, err := strconv.Atoi(strconv.FormatInt(start, 10))
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	endInt, err := strconv.Atoi(strconv.FormatInt(end, 10))
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	res := types.MakeArray(0)

	for i := startInt; i < endInt; i++ {
		res.Append(must.NotFail(arr.Get(i)))
	}

	return res, nil
}

// wholeArg evaluates arg and converts it to an int64, erroring when it is not a
// whole number.
func (o *sliceArray) wholeArg(arg any, doc *types.Document) (int64, error) {
	v, err := evaluateExpression(arg, doc)
	if err != nil {
		return 0, err
	}

	n, err := handlerparams.GetWholeNumberParam(v)
	if err != nil {
		return 0, newOperatorError(
			ErrArgsInvalidLen,
			"$slice",
			fmt.Sprintf("$slice requires numeric arguments, but got %s", handlerparams.AliasFromType(v)),
		)
	}

	return n, nil
}

// rangeOp represents `$range` operator.
type rangeOp struct {
	args []any
}

// newRange returns `$range` operator.
//
// The specification is `[start, end, step?]`; it returns an array of int32
// values. The default step is 1, and a step of 0 is an error.
func newRange(args ...any) (Operator, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$range",
			fmt.Sprintf("Expression $range takes at least 2 arguments, and at most 3, but %d were passed in.", len(args)),
		)
	}

	return &rangeOp{args: args}, nil
}

// Process implements Operator interface.
func (o *rangeOp) Process(doc *types.Document) (any, error) {
	start, err := o.intArg(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	end, err := o.intArg(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	step := int64(1)

	if len(o.args) == 3 {
		step, err = o.intArg(o.args[2], doc)
		if err != nil {
			return nil, err
		}
	}

	if step == 0 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$range",
			"$range requires a non-zero step value",
		)
	}

	for _, v := range []int64{start, end, step} {
		if v > math.MaxInt32 || v < math.MinInt32 {
			return nil, newOperatorError(ErrArgsInvalidLen, "$range", "$range arguments must fit in a 32-bit integer")
		}
	}

	res := types.MakeArray(0)

	if step > 0 {
		for i := start; i < end; i += step {
			v, err := checkedInt32(i)
			if err != nil {
				return nil, err
			}
			res.Append(v)
		}
	} else {
		for i := start; i > end; i += step {
			v, err := checkedInt32(i)
			if err != nil {
				return nil, err
			}
			res.Append(v)
		}
	}

	return res, nil
}

// checkedInt32 narrows a query-provided integer only after checking the exact
// BSON int32 range. Keeping the check beside the conversion also makes the
// trust boundary explicit to static analysis.
func checkedInt32(v int64) (int32, error) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, newOperatorError(ErrArgsInvalidLen, "$range", "$range result is outside the 32-bit integer range")
	}

	return int32(v), nil
}

// intArg evaluates arg and converts it to an int64, erroring when it is not a
// whole number.
func (o *rangeOp) intArg(arg any, doc *types.Document) (int64, error) {
	v, err := evaluateExpression(arg, doc)
	if err != nil {
		return 0, err
	}

	n, err := handlerparams.GetWholeNumberParam(v)
	if err != nil {
		return 0, newOperatorError(
			ErrArgsInvalidLen,
			"$range",
			fmt.Sprintf("$range requires a numeric starting value, found value of type: %s", handlerparams.AliasFromType(v)),
		)
	}

	return n, nil
}

// indexOfArray represents `$indexOfArray` operator.
type indexOfArray struct {
	args []any
}

// newIndexOfArray returns `$indexOfArray` operator.
//
// The specification is `[array, search, start?, end?]`; it returns the index of
// the first matching element, or -1.
func newIndexOfArray(args ...any) (Operator, error) {
	if len(args) < 2 || len(args) > 4 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$indexOfArray",
			fmt.Sprintf("Expression $indexOfArray takes at least 2 arguments, and at most 4, but %d were passed in.", len(args)),
		)
	}

	return &indexOfArray{args: args}, nil
}

// Process implements Operator interface.
func (o *indexOfArray) Process(doc *types.Document) (any, error) {
	arrValue, err := evaluateExpression(o.args[0], doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(arrValue) {
		return types.Null, nil
	}

	arr, ok := arrValue.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$indexOfArray",
			fmt.Sprintf(
				"$indexOfArray requires an array as a first argument, found: %s",
				handlerparams.AliasFromType(arrValue),
			),
		)
	}

	search, err := evaluateExpression(o.args[1], doc)
	if err != nil {
		return nil, err
	}

	l := arr.Len()

	start := int64(0)
	if len(o.args) >= 3 {
		start, err = o.intArg(o.args[2], doc)
		if err != nil {
			return nil, err
		}

		if start < 0 {
			start = 0
		}
	}

	end := int64(l)
	if len(o.args) == 4 {
		end, err = o.intArg(o.args[3], doc)
		if err != nil {
			return nil, err
		}

		if end > int64(l) {
			end = int64(l)
		}
	}

	if start > int64(l) || end <= start {
		return int32(-1), nil
	}

	startInt, err := strconv.Atoi(strconv.FormatInt(start, 10))
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	endInt, err := strconv.Atoi(strconv.FormatInt(end, 10))
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	for i := startInt; i < endInt; i++ {
		if types.Compare(must.NotFail(arr.Get(i)), search) == types.Equal {
			return aggregationIndex(i), nil
		}
	}

	return int32(-1), nil
}

// intArg evaluates arg and converts it to an int64, erroring when it is not a
// whole number.
func (o *indexOfArray) intArg(arg any, doc *types.Document) (int64, error) {
	v, err := evaluateExpression(arg, doc)
	if err != nil {
		return 0, err
	}

	n, err := handlerparams.GetWholeNumberParam(v)
	if err != nil {
		return 0, newOperatorError(
			ErrArgsInvalidLen,
			"$indexOfArray",
			fmt.Sprintf("$indexOfArray requires numeric bounds, but got %s", handlerparams.AliasFromType(v)),
		)
	}

	return n, nil
}

// arrayToObject represents `$arrayToObject` operator.
type arrayToObject struct {
	arg any
}

// newArrayToObject returns `$arrayToObject` operator.
//
// It takes an array of `{k, v}` documents OR an array of two-element `[k, v]`
// arrays and returns the corresponding document. It is the inverse of
// `$objectToArray`.
func newArrayToObject(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$arrayToObject",
			fmt.Sprintf("Expression $arrayToObject takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	return &arrayToObject{arg: args[0]}, nil
}

// Process implements Operator interface.
func (o *arrayToObject) Process(doc *types.Document) (any, error) {
	v, err := evaluateExpression(o.arg, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(v) {
		return types.Null, nil
	}

	arr, ok := v.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$arrayToObject",
			fmt.Sprintf("$arrayToObject requires an array input, but got: %s", handlerparams.AliasFromType(v)),
		)
	}

	res := new(types.Document)

	iter := arr.Iterator()
	defer iter.Close()

	for {
		_, elem, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		key, value, err := o.keyValue(elem)
		if err != nil {
			return nil, err
		}

		res.Set(key, value)
	}

	return res, nil
}

// keyValue extracts the key and value from a single `$arrayToObject` element,
// which is either a `{k, v}` document or a two-element `[k, v]` array.
func (o *arrayToObject) keyValue(elem any) (string, any, error) {
	switch elem := elem.(type) {
	case *types.Document:
		if elem.Len() != 2 || !elem.Has("k") || !elem.Has("v") {
			return "", nil, newOperatorError(
				ErrArgsInvalidLen,
				"$arrayToObject",
				"$arrayToObject requires an object with keys 'k' and 'v'.",
			)
		}

		key, ok := must.NotFail(elem.Get("k")).(string)
		if !ok {
			return "", nil, newOperatorError(
				ErrArgsInvalidLen,
				"$arrayToObject",
				"$arrayToObject requires an object with keys 'k' and 'v', where the value of 'k' must be of type string.",
			)
		}

		return key, must.NotFail(elem.Get("v")), nil
	case *types.Array:
		if elem.Len() != 2 {
			return "", nil, newOperatorError(
				ErrArgsInvalidLen,
				"$arrayToObject",
				"$arrayToObject requires an array of size 2 arrays,found array of size: "+fmt.Sprintf("%d", elem.Len()),
			)
		}

		key, ok := must.NotFail(elem.Get(0)).(string)
		if !ok {
			return "", nil, newOperatorError(
				ErrArgsInvalidLen,
				"$arrayToObject",
				"$arrayToObject requires an array of key-value pairs, where the key must be of type string.",
			)
		}

		return key, must.NotFail(elem.Get(1)), nil
	default:
		return "", nil, newOperatorError(
			ErrArgsInvalidLen,
			"$arrayToObject",
			fmt.Sprintf(
				"Unrecognised input type format for $arrayToObject: %s",
				handlerparams.AliasFromType(elem),
			),
		)
	}
}

// zip represents `$zip` operator.
type zip struct {
	inputs        any
	defaults      any
	useLongest    any
	hasUseLongest bool
	hasDefaults   bool
}

// newZip returns `$zip` operator.
//
// The specification document has the shape
// `{inputs: [<arrayExpr>, ...], useLongestLength: <bool>, defaults: [<expr>, ...]}`.
func newZip(args ...any) (Operator, error) {
	if len(args) != 1 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			fmt.Sprintf("Expression $zip takes exactly 1 arguments. %d were passed in.", len(args)),
		)
	}

	spec, ok := args[0].(*types.Document)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			fmt.Sprintf("$zip only supports an object as its argument, but got %s", handlerparams.AliasFromType(args[0])),
		)
	}

	inputs, err := spec.Get("inputs")
	if err != nil {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			"$zip requires at least one input array",
		)
	}

	z := &zip{inputs: inputs}

	if spec.Has("useLongestLength") {
		z.useLongest = must.NotFail(spec.Get("useLongestLength"))
		z.hasUseLongest = true
	}

	if spec.Has("defaults") {
		z.defaults = must.NotFail(spec.Get("defaults"))
		z.hasDefaults = true
	}

	return z, nil
}

// Process implements Operator interface.
func (o *zip) Process(doc *types.Document) (any, error) {
	inputsValue, err := evaluateExpression(o.inputs, doc)
	if err != nil {
		return nil, err
	}

	if isNullValue(inputsValue) {
		return types.Null, nil
	}

	inputsArr, ok := inputsValue.(*types.Array)
	if !ok {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			fmt.Sprintf("$zip found a non-array expression in input: %s", handlerparams.AliasFromType(inputsValue)),
		)
	}

	arrays := make([]*types.Array, 0, inputsArr.Len())

	minLen, maxLen := -1, 0

	inputsIter := inputsArr.Iterator()
	defer inputsIter.Close()

	for {
		_, elem, err := inputsIter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return nil, lazyerrors.Error(err)
		}

		if isNullValue(elem) {
			return types.Null, nil
		}

		arr, ok := elem.(*types.Array)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$zip",
				fmt.Sprintf("$zip found a non-array expression in input: %s", handlerparams.AliasFromType(elem)),
			)
		}

		arrays = append(arrays, arr)

		if minLen == -1 || arr.Len() < minLen {
			minLen = arr.Len()
		}

		if arr.Len() > maxLen {
			maxLen = arr.Len()
		}
	}

	if len(arrays) == 0 {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			"$zip requires at least one input array",
		)
	}

	useLongest := false

	if o.hasUseLongest {
		b, ok := o.useLongest.(bool)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$zip",
				fmt.Sprintf("useLongestLength must be a bool, but got %s", handlerparams.AliasFromType(o.useLongest)),
			)
		}

		useLongest = b
	}

	var defaults *types.Array

	if o.hasDefaults && !isNullValue(o.defaults) {
		d, ok := o.defaults.(*types.Array)
		if !ok {
			return nil, newOperatorError(
				ErrArgsInvalidLen,
				"$zip",
				fmt.Sprintf("defaults must be an array, but got %s", handlerparams.AliasFromType(o.defaults)),
			)
		}

		defaults = d
	}

	if defaults != nil && defaults.Len() > 0 && !useLongest {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			"cannot specify defaults unless useLongestLength is true",
		)
	}

	if defaults != nil && defaults.Len() > 0 && defaults.Len() != len(arrays) {
		return nil, newOperatorError(
			ErrArgsInvalidLen,
			"$zip",
			"defaults and inputs must have the same length",
		)
	}

	outLen := minLen
	if useLongest {
		outLen = maxLen
	}

	res := types.MakeArray(outLen)

	for i := 0; i < outLen; i++ {
		row := types.MakeArray(len(arrays))

		for j, arr := range arrays {
			switch {
			case i < arr.Len():
				row.Append(must.NotFail(arr.Get(i)))
			case defaults != nil && defaults.Len() > 0:
				row.Append(must.NotFail(defaults.Get(j)))
			default:
				row.Append(types.Null)
			}
		}

		res.Append(row)
	}

	return res, nil
}

// appendAll appends every element of src to dst.
func appendAll(dst, src *types.Array) error {
	iter := src.Iterator()
	defer iter.Close()

	for {
		_, elem, err := iter.Next()
		if errors.Is(err, iterator.ErrIteratorDone) {
			break
		}

		if err != nil {
			return lazyerrors.Error(err)
		}

		dst.Append(elem)
	}

	return nil
}

// check interfaces
var (
	_ Operator = (*size)(nil)
	_ Operator = (*arrayElemAt)(nil)
	_ Operator = (*concatArrays)(nil)
	_ Operator = (*isArray)(nil)
	_ Operator = (*inArray)(nil)
	_ Operator = (*reverseArray)(nil)
	_ Operator = (*sliceArray)(nil)
	_ Operator = (*rangeOp)(nil)
	_ Operator = (*indexOfArray)(nil)
	_ Operator = (*arrayToObject)(nil)
	_ Operator = (*zip)(nil)
)
