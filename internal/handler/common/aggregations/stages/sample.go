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
	"math/rand"

	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/common/aggregations"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// sample represents the $sample stage.
//
//	{ $sample: { size: <positive integer> } }
//
// It selects size documents pseudo-randomly from the input. If size is greater
// than or equal to the number of input documents, all of them are returned.
type sample struct {
	size int64
}

// newSample creates a new $sample stage.
func newSample(stage *types.Document) (aggregations.Stage, error) {
	fields, err := common.GetRequiredParam[*types.Document](stage, "$sample")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"the $sample stage specification must be an object",
			"$sample (stage)",
		)
	}

	sizeVal, err := fields.Get("size")
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrFailedToParse,
			"$sample stage must specify a size",
			"$sample (stage)",
		)
	}

	size, err := handlerparams.GetWholeNumberParam(sizeVal)
	if err != nil {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			"invalid argument to $sample stage: Expected an integer for the size argument",
			"$sample (stage)",
		)
	}

	if size < 0 {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrValueNegative,
			"invalid argument to $sample stage: size argument to $sample must not be negative",
			"$sample (stage)",
		)
	}

	return &sample{
		size: size,
	}, nil
}

// Process implements Stage interface.
func (s *sample) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) { //nolint:lll // for readability
	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	n := int(s.size)

	if n >= len(docs) {
		// return all documents in a shuffled order
		rand.Shuffle(len(docs), func(i, j int) {
			docs[i], docs[j] = docs[j], docs[i]
		})

		iter = iterator.Values(iterator.ForSlice(docs))
		closer.Add(iter)

		return iter, nil
	}

	// pick n documents pseudo-randomly without replacement using a partial
	// Fisher-Yates shuffle over the first n positions.
	for i := 0; i < n; i++ {
		j := i + rand.Intn(len(docs)-i)
		docs[i], docs[j] = docs[j], docs[i]
	}

	res := docs[:n]

	iter = iterator.Values(iterator.ForSlice(res))
	closer.Add(iter)

	return iter, nil
}

// check interfaces
var (
	_ aggregations.Stage = (*sample)(nil)
)
