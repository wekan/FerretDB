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

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// The two operators a projection takes as the VALUE of a field, as opposed to
// the operators that take a field's path. Every other document value in a
// projection is still rejected by projectionOperator.
const (
	projectionSlice     = "$slice"
	projectionElemMatch = "$elemMatch"
)

// projectionOperator reports which of the supported operators the projection
// value is, together with its argument.
//
// A projection value that is a document is one operator and nothing else:
// `{$slice: 2}` and `{$elemMatch: {...}}` are the two this handler implements,
// and anything else keeps the "is not supported" error it had before, so an
// operator that is still missing fails the same way it always did rather than
// being silently ignored.
func projectionOperator(value *types.Document) (string, any, error) {
	notSupported := handlererrors.NewCommandErrorMsg(
		handlererrors.ErrNotImplemented,
		fmt.Sprintf("projection expression %s is not supported", types.FormatAnyValue(value)),
	)

	if value.Len() != 1 {
		return "", nil, notSupported
	}

	key := value.Keys()[0]
	if key != projectionSlice && key != projectionElemMatch {
		return "", nil, notSupported
	}

	return key, must.NotFail(value.Get(key)), nil
}

// validateProjectionOperator checks the operator's argument, so a bad one is
// refused when the projection is parsed rather than per document.
func validateProjectionOperator(operator string, arg any) error {
	switch operator {
	case projectionSlice:
		_, _, err := sliceProjectionBounds(arg, 0)

		return err

	case projectionElemMatch:
		if _, ok := arg.(*types.Document); !ok {
			return handlererrors.NewCommandErrorMsgWithArgument(
				handlererrors.ErrBadValue,
				fmt.Sprintf(
					"elemMatch: Invalid argument, object required, but got %s",
					handlerparams.AliasFromType(arg),
				),
				"projection",
			)
		}

		return nil

	default:
		panic(fmt.Sprintf("unhandled projection operator %q", operator))
	}
}

// sliceProjectionBounds resolves the `$slice` argument against an array of
// `length` elements and returns the half-open range [start, end) to keep.
//
// The argument is either a single number or an array of exactly two:
//
//	{$slice: n}           n >= 0: the first n elements; n < 0: the last -n
//	{$slice: [skip, n]}   skip >= 0: from the front; skip < 0: from the back;
//	                      n must be positive and is the count to keep
//
// It is also the validator: called with any length it returns the same error
// for an argument MongoDB refuses, which is why ValidateProjection can use it
// with a length of zero before any document has been read.
func sliceProjectionBounds(arg any, length int64) (int64, int64, error) {
	badValue := func(msg string) error {
		return handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrBadValue, msg, "projection")
	}

	whole := func(v any) (int64, error) {
		n, err := handlerparams.GetWholeNumberParam(v)
		if err != nil {
			if errors.Is(err, handlerparams.ErrNotWholeNumber) || errors.Is(err, handlerparams.ErrUnexpectedType) {
				return 0, badValue(
					"Invalid $slice value in projection; must be an integer or array of two integers",
				)
			}

			return 0, badValue(err.Error())
		}

		return n, nil
	}

	var skip, limit int64

	switch arg := arg.(type) {
	case *types.Array:
		if arg.Len() != 2 {
			return 0, 0, badValue(
				"Invalid $slice value in projection; must be an integer or array of two integers",
			)
		}

		var err error

		if skip, err = whole(must.NotFail(arg.Get(0))); err != nil {
			return 0, 0, err
		}

		if limit, err = whole(must.NotFail(arg.Get(1))); err != nil {
			return 0, 0, err
		}

		// Unlike the single-number form, where a negative number means "from
		// the end", the count of a two-element $slice must be positive.
		if limit <= 0 {
			return 0, 0, badValue("Invalid $slice value in projection; must be positive")
		}

	default:
		n, err := whole(arg)
		if err != nil {
			return 0, 0, err
		}

		if n < 0 {
			// The last -n elements.
			skip, limit = n, -n
			break
		}

		skip, limit = 0, n
	}

	start := skip
	if start < 0 {
		// Counted from the end, and an offset further back than the array is
		// long starts at the front rather than before it.
		start = length + skip
		if start < 0 {
			start = 0
		}
	}

	if start > length {
		start = length
	}

	end := start + limit
	if end > length {
		end = length
	}

	return start, end, nil
}

// sliceProjection returns the elements of arr that `$slice: arg` keeps.
func sliceProjection(arr *types.Array, arg any) (*types.Array, error) {
	start, end, err := sliceProjectionBounds(arg, int64(arr.Len()))
	if err != nil {
		return nil, err
	}

	res := types.MakeArray(int(end - start))

	for i := start; i < end; i++ {
		res.Append(must.NotFail(arr.Get(int(i))))
	}

	return res, nil
}

// elemMatchProjection returns a one-element array holding the FIRST element of
// arr that matches the condition, and false when nothing matches - in which
// case MongoDB leaves the field out of the result entirely rather than
// returning it empty.
//
// Only documents can match: `$elemMatch` in a projection takes a query on the
// fields of an element, so a scalar element has no field to match against.
func elemMatchProjection(arr *types.Array, cond *types.Document) (*types.Array, bool, error) {
	for i := range arr.Len() {
		elem, ok := must.NotFail(arr.Get(i)).(*types.Document)
		if !ok {
			continue
		}

		matches, err := FilterDocument(elem, cond)
		if err != nil {
			return nil, false, err
		}

		if !matches {
			continue
		}

		res := types.MakeArray(1)
		res.Append(elem)

		return res, true, nil
	}

	return nil, false, nil
}

// applyProjectionOperator applies `$slice` or `$elemMatch` at path, reading the
// field from source and writing what is left of it to projected.
//
// A field the operator cannot apply to is left as the projection's inclusion or
// exclusion nature already decided, which is what MongoDB does: `$slice` on a
// field that is not an array returns the field unchanged, and both operators on
// a field that is not there return nothing.
func applyProjectionOperator(operator string, arg any, path types.Path, source, projected *types.Document,
	inclusion bool,
) error {
	value, err := source.GetByPath(path)
	if err != nil {
		// The field is not on this document. An exclusion projection has
		// already copied everything else, and an inclusion one has nothing to
		// copy, so either way there is nothing to do.
		return nil
	}

	arr, isArray := value.(*types.Array)

	switch operator {
	case projectionSlice:
		if !isArray {
			// Not an array: $slice keeps the field as it is. It is already
			// there in an exclusion projection; an inclusion one has to add it.
			if inclusion {
				return projected.SetByPath(path, value)
			}

			return nil
		}

		sliced, err := sliceProjection(arr, arg)
		if err != nil {
			return err
		}

		return projected.SetByPath(path, sliced)

	case projectionElemMatch:
		if !isArray {
			// $elemMatch matches elements, and a field that is not an array has
			// none - so the field is not in the result, whichever kind of
			// projection this is.
			removeByPath(projected, path)

			return nil
		}

		matched, ok, err := elemMatchProjection(arr, arg.(*types.Document))
		if err != nil {
			return err
		}

		if !ok {
			removeByPath(projected, path)

			return nil
		}

		return projected.SetByPath(path, matched)

	default:
		panic(fmt.Sprintf("unhandled projection operator %q", operator))
	}
}

// removeByPath removes the field at path when it is there, and does nothing
// when it is not - `RemoveByPath` on a path a document does not have would
// otherwise have to be guarded at every call.
func removeByPath(doc *types.Document, path types.Path) {
	if doc.HasByPath(path) {
		doc.RemoveByPath(path)
	}
}
