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
	"strconv"
	"strings"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// errPositionalNoMatch is MongoDB's answer when the query did not match an
// element of the array that the positional operator names.
const errPositionalNoMatch = "The positional operator did not find the match needed from the query."

// resolvePositional returns update with every "<array>.$" path segment of its
// operator documents replaced by the index of the first element of <array>
// that matches the filter's conditions on that array, as MongoDB's positional
// $ operator does: {"members.userId": x} with {$set: {"members.$.isAdmin": true}}
// sets isAdmin on the member whose userId is x. An update without "$" segments
// is returned as it is.
func resolvePositional(doc, filter, update *types.Document) (*types.Document, error) {
	if !hasPositional(update) {
		return update, nil
	}

	out := must.NotFail(types.NewDocument())

	for _, op := range update.Keys() {
		v := must.NotFail(update.Get(op))

		fields, ok := v.(*types.Document)
		if !ok {
			out.Set(op, v)
			continue
		}

		resolved := must.NotFail(types.NewDocument())

		for _, key := range fields.Keys() {
			value := must.NotFail(fields.Get(key))

			if op == "$rename" {
				if s, ok := value.(string); hasPositionalSegment(key) || (ok && hasPositionalSegment(s)) {
					return nil, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue,
						"The source and destination fields for $rename may not be dynamic: "+key)
				}
			}

			k, err := resolvePositionalPath(doc, filter, key)
			if err != nil {
				return nil, err
			}

			resolved.Set(k, value)
		}

		out.Set(op, resolved)
	}

	return out, nil
}

// hasPositional reports whether any operator document of update has a key
// with a "$" path segment.
func hasPositional(update *types.Document) bool {
	for _, op := range update.Keys() {
		fields, ok := must.NotFail(update.Get(op)).(*types.Document)
		if !ok {
			continue
		}

		for _, key := range fields.Keys() {
			if hasPositionalSegment(key) {
				return true
			}

			if s, ok := must.NotFail(fields.Get(key)).(string); ok && op == "$rename" && hasPositionalSegment(s) {
				return true
			}
		}
	}

	return false
}

func hasPositionalSegment(path string) bool {
	for _, s := range strings.Split(path, ".") {
		if s == "$" {
			return true
		}
	}

	return false
}

// resolvePositionalPath replaces the first "$" segment of path; MongoDB
// allows only one positional operator in a path.
func resolvePositionalPath(doc, filter *types.Document, path string) (string, error) {
	segments := strings.Split(path, ".")

	at := -1

	for i, s := range segments {
		if s != "$" {
			continue
		}

		if at >= 0 {
			return "", handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue,
				"Too many positional (i.e. '$') elements found in path '"+path+"'")
		}

		at = i
	}

	if at < 0 {
		return path, nil
	}

	if at == 0 {
		return "", handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
	}

	prefix := strings.Join(segments[:at], ".")

	i, err := positionalIndex(doc, filter, prefix)
	if err != nil {
		return "", err
	}

	segments[at] = strconv.Itoa(i)

	return strings.Join(segments, "."), nil
}

// positionalIndex is the index of the first element of the array at prefix
// that satisfies the filter's conditions on that array: the conditions on
// prefix itself and on paths under it, each element tried as if it were the
// array's only one.
func positionalIndex(doc, filter *types.Document, prefix string) (int, error) {
	if filter == nil {
		return 0, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
	}

	sub := must.NotFail(types.NewDocument())

	for _, key := range filter.Keys() {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			sub.Set(key, must.NotFail(filter.Get(key)))
		}
	}

	if sub.Len() == 0 {
		return 0, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
	}

	path, err := types.NewPathFromString(prefix)
	if err != nil {
		return 0, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
	}

	v, err := doc.GetByPath(path)
	if err != nil {
		return 0, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
	}

	arr, ok := v.(*types.Array)
	if !ok {
		return 0, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
	}

	for i := 0; i < arr.Len(); i++ {
		probe := doc.DeepCopy()

		if err = probe.SetByPath(path, must.NotFail(types.NewArray(must.NotFail(arr.Get(i))))); err != nil {
			return 0, lazyerrors.Error(err)
		}

		matched, err := FilterDocument(probe, sub)
		if err != nil {
			return 0, lazyerrors.Error(err)
		}

		if matched {
			return i, nil
		}
	}

	return 0, handlererrors.NewCommandErrorMsg(handlererrors.ErrBadValue, errPositionalNoMatch)
}
