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

package handler

import (
	"context"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// A count of a whole collection - `count` with no query, or the aggregation a
// driver sends for countDocuments({}) - used to fetch and decode every document
// only to count them: seconds on a collection of a few hundred thousand, on
// every call of a paged list that shows its total.
//
// The database counts them instead (Collection.CountAll), and the count is fed
// to the handler's unchanged skip, limit, count and $group stages as that many
// empty documents. Nothing is fetched or decoded, and every stage after the
// match behaves exactly as before - including an empty collection, for which a
// $group returns no document at all.

// matchesAll reports whether a filter selects every document.
func matchesAll(filter *types.Document) bool {
	return filter == nil || filter.Len() == 0
}

// countPipelineMatchesAll reports whether pipeline only counts (see
// countPipelineDecodeFields) and all its $match stages are empty.
func countPipelineMatchesAll(pipeline []any) bool {
	if countPipelineDecodeFields(pipeline) == nil {
		return false
	}

	for _, v := range pipeline {
		stage := v.(*types.Document)
		if stage.Keys()[0] != "$match" {
			continue
		}

		if !matchesAll(must.NotFail(stage.Get("$match")).(*types.Document)) {
			return false
		}
	}

	return true
}

// countedDocuments returns as many empty documents as the collection holds.
func countedDocuments(ctx context.Context, c backends.Collection) (types.DocumentsIterator, error) {
	res, err := c.CountAll(ctx, new(backends.CountAllParams))
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	var n int64

	return iterator.ForFunc(func() (struct{}, *types.Document, error) {
		if n >= res.Count {
			return struct{}{}, nil, iterator.ErrIteratorDone
		}

		n++

		return struct{}{}, types.MakeDocument(0), nil
	}), nil
}
