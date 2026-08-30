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

package common

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// processPopArrayUpdateExpression changes document according to $pop operator.
// If the document was changed it returns true.
func processPopArrayUpdateExpression(command string, doc *types.Document, key string, value any) (bool, error) {
	popValue, err := handlerparams.GetWholeNumberParam(value)
	if err != nil {
		return false, NewUpdateError(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf(`Expected a number in: %s: "%v"`, key, value),
			command,
		)
	}

	if popValue != 1 && popValue != -1 {
		return false, NewUpdateError(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("$pop expects 1 or -1, found: %d", popValue),
			command,
		)
	}

	path, err := types.NewPathFromString(key)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	oldValue, err := doc.GetByPath(path)
	if err != nil {
		// If any sub path exists in the doc, $pop returns ErrUnsuitableValueType.
		if err = checkUnsuitableValueError(command, doc, key, path); err != nil {
			return false, err
		}

		return false, nil
	}

	array, ok := oldValue.(*types.Array)
	if !ok {
		return false, NewUpdateError(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("Path '%s' contains an element of non-array type '%s'", key, handlerparams.AliasFromType(oldValue)),
			command,
		)
	}

	if array.Len() == 0 {
		return false, nil
	}

	if popValue == -1 {
		array.Remove(0)
	} else {
		array.Remove(array.Len() - 1)
	}

	err = doc.SetByPath(path, array)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	return true, nil
}

// checkUnsuitableValueError returns ErrUnsuitableValueType if path contains
// a non-document value. If no element exists on path, it returns nil.
// For example, if the path is "v.foo" and:
//   - doc is {v: 42}, it returns ErrUnsuitableValueType, v is used by unsuitable value type;
//   - doc is {c: 10}, it returns no error since the path does not exist.
func checkUnsuitableValueError(command string, doc *types.Document, fullPath string, path types.Path) error {
	// return no error if path is suffix or key.
	if path.Len() == 1 {
		return nil
	}

	prefix := path.Prefix()

	// check if part of the path exists in the document.
	if doc.Has(prefix) {
		val := must.NotFail(doc.Get(prefix))
		switch val := val.(type) {
		case *types.Document:
			// recursively check if document contains the remaining part.
			return checkUnsuitableValueError(command, val, fullPath, path.TrimPrefix())
		case *types.Array:
			return checkUnsuitableValueInArray(command, val, fullPath, prefix, path.TrimPrefix())
		default:
			// ErrUnsuitableValueType is returned if the document contains prefix.
			return NewUpdateError(
				handlererrors.ErrUnsuitableValueType,
				fmt.Sprintf(
					"Cannot use the part (%s) of (%s) to traverse the element ({%s: %v})",
					path.Slice()[1],
					fullPath,
					prefix,
					types.FormatAnyValue(val),
				),
				command,
			)
		}
	}

	// no part of the path exists in the doc.
	return nil
}

// checkUnsuitableValueInArray returns ErrUnsuitableValueType if path contains
// non traversable part. If no element exists on path, it returns nil.
// For example, if the path is "0.foo" and:
//   - array is [], it returns no error since index-0 does not exist.
//   - array is [{bar: 10}], it returns no error since the document at index-0 does not contain 'foo'.
//   - array is [42, 43], it returns ErrUnsuitableValueType, since element at index-0 is not a document.
func checkUnsuitableValueInArray(command string, array *types.Array, fullPath, parentKey string, path types.Path) error {
	prefix := path.Prefix()

	index, err := strconv.Atoi(prefix)
	if err != nil || index < 0 {
		return NewUpdateError(
			handlererrors.ErrUnsuitableValueType,
			fmt.Sprintf(
				"Cannot use the part (%s) of (%s) to traverse the element ({%s: %v})",
				prefix,
				fullPath,
				parentKey,
				types.FormatAnyValue(array),
			),
			command,
		)
	}

	// return no error if path just contain the index.
	if path.Len() == 1 {
		return nil
	}

	var elem any

	if elem, err = array.Get(index); err == nil {
		switch elem := elem.(type) {
		case *types.Document:
			return checkUnsuitableValueError(command, elem, fullPath, path.TrimPrefix())
		case *types.Array:
			return checkUnsuitableValueInArray(command, elem, fullPath, prefix, path.TrimPrefix())
		default:
			return NewUpdateError(
				handlererrors.ErrUnsuitableValueType,
				fmt.Sprintf(
					"Cannot use the part (%s) of (%s) to traverse the element ({%d: %v})",
					path.Slice()[1],
					fullPath,
					index,
					types.FormatAnyValue(elem),
				),
				command,
			)
		}
	}

	return nil
}

// processPushArrayUpdateExpression changes document according to $push array update operator.
// If the document was changed it returns true.
//
// It supports the $each modifier together with the $position, $sort and $slice modifiers.
func processPushArrayUpdateExpression(command string, doc *types.Document, key string, pushVal any) (bool, error) {
	var (
		each *types.Array

		hasPosition bool
		positionVal any

		hasSort bool
		sortVal any

		hasSlice bool
		sliceVal any
	)

	if pushDoc, ok := pushVal.(*types.Document); ok {
		if pushDoc.Has("$each") {
			eachRaw := must.NotFail(pushDoc.Get("$each"))

			each, ok = eachRaw.(*types.Array)
			if !ok {
				return false, NewUpdateError(
					handlererrors.ErrBadValue,
					fmt.Sprintf(
						"The argument to $each in $push must be an array but it was of type: %s",
						handlerparams.AliasFromType(eachRaw),
					),
					command,
				)
			}

			// The $each modifier may be accompanied by the $position, $sort and $slice modifiers.
			// Any other clause is rejected by MongoDB.
			for _, modifier := range pushDoc.Keys() {
				switch modifier {
				case "$each":
					// already handled above
				case "$position":
					hasPosition = true
					positionVal = must.NotFail(pushDoc.Get("$position"))
				case "$sort":
					hasSort = true
					sortVal = must.NotFail(pushDoc.Get("$sort"))
				case "$slice":
					hasSlice = true
					sliceVal = must.NotFail(pushDoc.Get("$slice"))
				default:
					return false, NewUpdateError(
						handlererrors.ErrFailedToParse,
						fmt.Sprintf("Unrecognized clause in $push: %s", modifier),
						command,
					)
				}
			}
		}
	}

	path, err := types.NewPathFromString(key)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	// If the path does not exist, create a new array and set it.
	if !doc.HasByPath(path) {
		if err = doc.SetByPath(path, types.MakeArray(1)); err != nil {
			return false, NewUpdateError(
				handlererrors.ErrUnsuitableValueType,
				err.Error(),
				command,
			)
		}
	}

	value, err := doc.GetByPath(path)
	if err != nil {
		return false, err
	}

	array, ok := value.(*types.Array)
	if !ok {
		return false, NewUpdateError(
			handlererrors.ErrBadValue,
			fmt.Sprintf(
				"The field '%s' must be an array but is of type '%s' in document {_id: %s}",
				key, handlerparams.AliasFromType(value), types.FormatAnyValue(must.NotFail(doc.Get("_id"))),
			),
			command,
		)
	}

	if each == nil {
		each = types.MakeArray(1)
		each.Append(pushVal)
	}

	// Keep the original elements to detect whether the array was actually changed.
	original := make([]any, array.Len())
	for i := range array.Len() {
		original[i] = must.NotFail(array.Get(i))
	}

	toInsert := make([]any, each.Len())
	for i := range each.Len() {
		toInsert[i] = must.NotFail(each.Get(i))
	}

	// Step 1: insert the $each values at the requested $position (append by default).
	insertAt := len(original)

	if hasPosition {
		if insertAt, err = pushPositionIndex(command, positionVal, len(original)); err != nil {
			return false, err
		}
	}

	combined := make([]any, 0, len(original)+len(toInsert))
	combined = append(combined, original[:insertAt]...)
	combined = append(combined, toInsert...)
	combined = append(combined, original[insertAt:]...)

	result := types.MakeArray(len(combined))
	result.Append(combined...)

	// Step 2: sort the whole array with the $sort modifier.
	if hasSort {
		if err = sortPushArray(command, result, sortVal); err != nil {
			return false, err
		}
	}

	// Step 3: limit the array with the $slice modifier.
	if hasSlice {
		if result, err = slicePushArray(command, result, sliceVal); err != nil {
			return false, err
		}
	}

	if err = doc.SetByPath(path, result); err != nil {
		return false, lazyerrors.Error(err)
	}

	// Determine whether the resulting array differs from the original one.
	changed := result.Len() != len(original)
	if !changed {
		for i := range original {
			if types.Compare(original[i], must.NotFail(result.Get(i))) != types.Equal {
				changed = true
				break
			}
		}
	}

	return changed, nil
}

// pushPositionIndex validates the $position modifier value and returns the index at which
// the $each values should be inserted into an array of the given length.
//
// A negative position counts from the end of the array; if it points before the beginning
// the values are inserted at the start, and a position past the end appends the values.
func pushPositionIndex(command string, positionVal any, length int) (int, error) {
	position, err := handlerparams.GetWholeNumberParam(positionVal)
	if err != nil {
		return 0, NewUpdateError(
			handlererrors.ErrBadValue,
			fmt.Sprintf(
				"The value for $position must be an integer value, not of type: %s",
				handlerparams.AliasFromType(positionVal),
			),
			command,
		)
	}

	idx := int(position)

	if idx < 0 {
		idx += length
		if idx < 0 {
			idx = 0
		}
	}

	if idx > length {
		idx = length
	}

	return idx, nil
}

// sortPushArray sorts arr in place according to the $sort modifier value.
//
// A scalar value of 1 or -1 sorts the whole elements, while a document like {field: 1}
// sorts arrays of sub-documents by the given field(s).
func sortPushArray(command string, arr *types.Array, sortVal any) error {
	sortDoc, ok := sortVal.(*types.Document)
	if !ok {
		// Sort the whole elements by the scalar 1/-1 ordering.
		sortType, err := GetSortType("$sort", sortVal)
		if err != nil {
			return NewUpdateError(
				handlererrors.ErrBadValue,
				"The $sort element value must be either 1 or -1",
				command,
			)
		}

		SortArray(arr, sortType)

		return nil
	}

	// Sort arrays of sub-documents by the given field(s).
	paths := make([]types.Path, sortDoc.Len())
	sortTypes := make([]types.SortType, sortDoc.Len())

	for i, sortKey := range sortDoc.Keys() {
		sortType, err := GetSortType(sortKey, must.NotFail(sortDoc.Get(sortKey)))
		if err != nil {
			return NewUpdateError(
				handlererrors.ErrBadValue,
				"The $sort element value must be either 1 or -1",
				command,
			)
		}

		sortPath, err := types.NewPathFromString(sortKey)
		if err != nil {
			return lazyerrors.Error(err)
		}

		paths[i] = sortPath
		sortTypes[i] = sortType
	}

	elems := make([]any, arr.Len())
	for i := range arr.Len() {
		elems[i] = must.NotFail(arr.Get(i))
	}

	sort.SliceStable(elems, func(i, j int) bool {
		for k := range paths {
			a := pushSortField(elems[i], paths[k])
			b := pushSortField(elems[j], paths[k])

			switch types.CompareOrderForSort(a, b, sortTypes[k]) {
			case types.Less:
				return true
			case types.Greater:
				return false
			case types.Equal:
				// try the next sort key
			}
		}

		return false
	})

	for i, elem := range elems {
		must.NoError(arr.Set(i, elem))
	}

	return nil
}

// pushSortField returns the value of the given path in elem for $sort comparison.
// If elem is not a document or the path does not exist, null is used, matching sort semantics.
func pushSortField(elem any, path types.Path) any {
	doc, ok := elem.(*types.Document)
	if !ok {
		return types.Null
	}

	value, err := doc.GetByPath(path)
	if err != nil {
		return types.Null
	}

	return value
}

// slicePushArray returns a new array limited according to the $slice modifier value.
//
// A positive N keeps the first N elements, a negative N keeps the last N elements,
// and 0 empties the array.
func slicePushArray(command string, arr *types.Array, sliceVal any) (*types.Array, error) {
	slice, err := handlerparams.GetWholeNumberParam(sliceVal)
	if err != nil {
		msg := fmt.Sprintf(
			"The value for $slice must be a numeric value but was given type: %s",
			handlerparams.AliasFromType(sliceVal),
		)

		if errors.Is(err, handlerparams.ErrNotWholeNumber) {
			msg = fmt.Sprintf(
				"The value for $slice must be an integer value but was given type: %s",
				handlerparams.AliasFromType(sliceVal),
			)
		}

		return nil, NewUpdateError(handlererrors.ErrBadValue, msg, command)
	}

	length := int64(arr.Len())

	var start, end int64

	switch {
	case slice >= 0:
		start = 0

		end = slice
		if end > length {
			end = length
		}
	default:
		// keep the last -slice elements
		start = length + slice
		if start < 0 {
			start = 0
		}

		end = length
	}

	result := types.MakeArray(int(end - start))

	for i := start; i < end; i++ {
		result.Append(must.NotFail(arr.Get(int(i))))
	}

	return result, nil
}

// processAddToSetArrayUpdateExpression changes document according to $addToSet array update operator.
// If the document was changed it returns true.
func processAddToSetArrayUpdateExpression(command string, doc *types.Document, key string, setVal any) (bool, error) {
	var each *types.Array

	if addToSetDoc, ok := setVal.(*types.Document); ok {
		if addToSetDoc.Has("$each") {
			eachRaw := must.NotFail(addToSetDoc.Get("$each"))

			each, ok = eachRaw.(*types.Array)
			if !ok {
				return false, NewUpdateError(
					handlererrors.ErrTypeMismatch,
					fmt.Sprintf(
						"The argument to $each in $addToSet must be an array but it was of type %s",
						handlerparams.AliasFromType(eachRaw),
					),
					command,
				)
			}
		}
	}

	path, err := types.NewPathFromString(key)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	// If the path does not exist, create a new array and set it.
	if !doc.HasByPath(path) {
		if err = doc.SetByPath(path, types.MakeArray(1)); err != nil {
			return false, NewUpdateError(
				handlererrors.ErrUnsuitableValueType,
				err.Error(),
				command,
			)
		}
	}

	value, err := doc.GetByPath(path)
	if err != nil {
		return false, err
	}

	array, ok := value.(*types.Array)
	if !ok {
		return false, NewUpdateError(
			handlererrors.ErrBadValue,
			fmt.Sprintf(
				"The field '%s' must be an array but is of type '%s' in document {_id: %s}",
				key, handlerparams.AliasFromType(value), types.FormatAnyValue(must.NotFail(doc.Get("_id"))),
			),
			command,
		)
	}

	if each == nil {
		each = types.MakeArray(1)
		each.Append(setVal)
	}

	var changed bool

	for i := range each.Len() {
		elem := must.NotFail(each.Get(i))

		if array.Contains(elem) {
			continue
		}

		changed = true

		array.Append(elem)
	}

	if err = doc.SetByPath(path, array); err != nil {
		return false, lazyerrors.Error(err)
	}

	return changed, nil
}

// processPullAllArrayUpdateExpression changes document according to $pullAll array update operator.
// If the document was changed it returns true.
func processPullAllArrayUpdateExpression(command string, doc *types.Document, key string, pullVal any) (bool, error) {
	pullArray, ok := pullVal.(*types.Array)
	if !ok {
		return false, NewUpdateError(
			handlererrors.ErrBadValue,
			fmt.Sprintf(
				"The field '%s' must be an array but is of type '%s'",
				key, handlerparams.AliasFromType(pullVal),
			),
			command,
		)
	}

	path, err := types.NewPathFromString(key)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	if !doc.HasByPath(path) {
		if err = checkUnsuitableValueError(command, doc, key, path); err != nil {
			return false, err
		}

		return false, nil
	}

	value, err := doc.GetByPath(path)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	array, ok := value.(*types.Array)
	if !ok {
		return false, NewUpdateError(
			handlererrors.ErrBadValue,
			fmt.Sprintf(
				"The field '%s' must be an array but is of type '%s' in document {_id: %s}",
				key, handlerparams.AliasFromType(value), types.FormatAnyValue(must.NotFail(doc.Get("_id"))),
			),
			command,
		)
	}

	var changed bool

	for j := range pullArray.Len() {
		pullElem := must.NotFail(pullArray.Get(j))

		// we remove all instances of pullElem in array
		for i := array.Len() - 1; i >= 0; i-- {
			arrayElem := must.NotFail(array.Get(i))

			if types.Compare(arrayElem, pullElem) == types.Equal {
				array.Remove(i)
				changed = true
			}
		}
	}

	if err = doc.SetByPath(path, array); err != nil {
		return false, lazyerrors.Error(err)
	}

	return changed, nil
}

// processPullArrayUpdateExpression changes document according to $pull array update operator.
// If the document was changed it returns true.
func processPullArrayUpdateExpression(command string, doc *types.Document, key string, pullVal any) (bool, error) {
	path, err := types.NewPathFromString(key)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	if !doc.HasByPath(path) {
		if err = checkUnsuitableValueError(command, doc, key, path); err != nil {
			return false, err
		}

		return false, nil
	}

	value, err := doc.GetByPath(path)
	if err != nil {
		return false, lazyerrors.Error(err)
	}

	array, ok := value.(*types.Array)
	if !ok {
		return false, NewUpdateError(
			handlererrors.ErrBadValue,
			"Cannot apply $pull to a non-array value",
			command,
		)
	}

	var changed bool

	for i := array.Len() - 1; i >= 0; i-- {
		elem := must.NotFail(array.Get(i))

		matches := types.Compare(elem, pullVal) == types.Equal
		if elemDoc, ok := elem.(*types.Document); ok {
			if pullDoc, ok := pullVal.(*types.Document); ok {
				matches, err = FilterDocument(elemDoc, pullDoc)
				if err != nil {
					return false, lazyerrors.Error(err)
				}
			}
		}

		if matches {
			array.Remove(i)
			changed = true
		}
	}

	if err = doc.SetByPath(path, array); err != nil {
		return false, lazyerrors.Error(err)
	}

	return changed, nil
}
