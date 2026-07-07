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

package stages

import (
	"context"
	"fmt"

	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// Lookup represents the basic equality-join form of the $lookup stage:
//
//	{ $lookup: { from: <string>, localField: <string>, foreignField: <string>, as: <string> } }
//
// The `pipeline`/`let` sub-form is not implemented.
//
// Because an aggregation stage only receives the incoming document iterator and has no access to
// the database/backend handle, the documents of the `from` collection cannot be fetched by the
// stage itself. They are pre-fetched in msg_aggregate.go (which does have the database handle)
// and injected via SetFromDocuments before Process runs.
type Lookup struct {
	from         string
	localField   string
	foreignField string
	as           string

	// fromDocs holds all documents of the `from` collection, injected via SetFromDocuments.
	fromDocs []*types.Document
}

// newLookup creates a new $lookup stage for the basic equality-join form.
func newLookup(stage *types.Document) (aggregations.Stage, error) {
	spec, err := stage.Get("$lookup")
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	specDoc, ok := spec.(*types.Document)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("the $lookup specification must be an object, got %s", types.FormatAnyValue(spec)),
			"$lookup (stage)",
		)
	}

	// The pipeline/let sub-form of $lookup is not implemented.
	if specDoc.Has("pipeline") || specDoc.Has("let") {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrNotImplemented,
			"$lookup with 'pipeline' or 'let' is not implemented yet",
			"$lookup (stage)",
		)
	}

	from, err := getRequiredStringField(specDoc, "from")
	if err != nil {
		return nil, err
	}

	localField, err := getRequiredStringField(specDoc, "localField")
	if err != nil {
		return nil, err
	}

	foreignField, err := getRequiredStringField(specDoc, "foreignField")
	if err != nil {
		return nil, err
	}

	as, err := getRequiredStringField(specDoc, "as")
	if err != nil {
		return nil, err
	}

	return &Lookup{
		from:         from,
		localField:   localField,
		foreignField: foreignField,
		as:           as,
	}, nil
}

// getRequiredStringField returns the string value of the given field in the $lookup specification,
// returning a MongoDB-compatible error if it is missing or not a string.
func getRequiredStringField(specDoc *types.Document, field string) (string, error) {
	v, err := specDoc.Get(field)
	if err != nil {
		return "", handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("missing '%s' option to $lookup stage specification", field),
			"$lookup (stage)",
		)
	}

	s, ok := v.(string)
	if !ok {
		return "", handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			fmt.Sprintf("argument '%s' to $lookup stage must be a string, is type %s", field, types.FormatAnyValue(v)),
			"$lookup (stage)",
		)
	}

	return s, nil
}

// From returns the name of the `from` collection to join against.
func (l *Lookup) From() string {
	return l.from
}

// SetFromDocuments injects the pre-fetched documents of the `from` collection.
func (l *Lookup) SetFromDocuments(docs []*types.Document) {
	l.fromDocs = docs
}

// Process implements Stage interface.
func (l *Lookup) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	out := make([]*types.Document, 0, len(docs))

	for _, doc := range docs {
		matched := types.MakeArray(0)

		localValue, hasLocal := getFieldValue(doc, l.localField)

		if hasLocal {
			for _, fromDoc := range l.fromDocs {
				foreignValue, hasForeign := getFieldValue(fromDoc, l.foreignField)
				if !hasForeign {
					continue
				}

				if valuesMatch(localValue, foreignValue) {
					matched.Append(fromDoc)
				}
			}
		}

		doc.Set(l.as, matched)
		out = append(out, doc)
	}

	resIter := iterator.Values(iterator.ForSlice(out))
	closer.Add(resIter)

	return resIter, nil
}

// getFieldValue returns the value of the given field (supporting dot notation) in the document,
// and whether the field exists.
func getFieldValue(doc *types.Document, field string) (any, bool) {
	path, err := types.NewPathFromString(field)
	if err != nil {
		return nil, false
	}

	v, err := doc.GetByPath(path)
	if err != nil {
		return nil, false
	}

	return v, true
}

// valuesMatch reports whether the local and foreign values should be joined, following the
// MongoDB rules: if either value is an array, the join matches when any element of the local
// value equals any element (or the scalar) of the foreign value. Scalar equality is the common
// case (e.g. WeKan joins on a scalar boardId).
func valuesMatch(local, foreign any) bool {
	localArr, localIsArr := local.(*types.Array)
	foreignArr, foreignIsArr := foreign.(*types.Array)

	switch {
	case localIsArr && foreignIsArr:
		for i := 0; i < localArr.Len(); i++ {
			lv := must.NotFail(localArr.Get(i))
			for j := 0; j < foreignArr.Len(); j++ {
				if types.Compare(lv, must.NotFail(foreignArr.Get(j))) == types.Equal {
					return true
				}
			}
		}

		return false
	case localIsArr:
		for i := 0; i < localArr.Len(); i++ {
			if types.Compare(must.NotFail(localArr.Get(i)), foreign) == types.Equal {
				return true
			}
		}

		return false
	case foreignIsArr:
		for j := 0; j < foreignArr.Len(); j++ {
			if types.Compare(local, must.NotFail(foreignArr.Get(j))) == types.Equal {
				return true
			}
		}

		return false
	default:
		return types.Compare(local, foreign) == types.Equal
	}
}

// check interfaces
var (
	_ aggregations.Stage = (*Lookup)(nil)
)
