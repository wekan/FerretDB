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
	"container/heap"
	"errors"
	"sort"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// sortLimitInitialCapacity keeps an effectively unlimited wire-protocol limit
// from becoming a multi-gigabyte allocation before the first document is read.
// The slice still grows normally when the result set actually contains more.
const sortLimitInitialCapacity int64 = 1024

// SortIterator returns an iterator of sorted documents.
// It will be added to the given closer.
//
// Since sorting iterator is impossible, this function fully consumes and closes the underlying iterator,
// sorts documents in memory and returns a new iterator over the sorted slice.
func SortIterator(iter types.DocumentsIterator, closer *iterator.MultiCloser, sort *types.Document) (types.DocumentsIterator, error) { //nolint:lll // for readability
	// don't consume all documents if there is no sort
	if sort.Len() == 0 {
		return iter, nil
	}

	docs, err := iterator.ConsumeValues(iter)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if err = SortDocuments(docs, sort); err != nil {
		return nil, lazyerrors.Error(err)
	}

	res := iterator.Values(iterator.ForSlice(docs))
	closer.Add(res)

	return res, nil
}

// SortLimitIterator returns the first keep documents in MongoDB sort order.
// It retains at most keep documents while consuming the filtered stream.
func SortLimitIterator(iter types.DocumentsIterator, closer *iterator.MultiCloser, sortDoc *types.Document, keep int64) (types.DocumentsIterator, error) { //nolint:lll // arguments document the iterator pipeline
	if sortDoc.Len() == 0 || keep <= 0 {
		return SortIterator(iter, closer, sortDoc)
	}

	sorts, err := makeSortFuncs(sortDoc)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	capacity := min(keep, sortLimitInitialCapacity)
	h := &documentsMaxHeap{sorts: sorts, docs: make([]*types.Document, 0, capacity)}
	heap.Init(h)
	for {
		_, doc, nextErr := iter.Next()
		if nextErr != nil {
			if errors.Is(nextErr, iterator.ErrIteratorDone) {
				break
			}
			return nil, lazyerrors.Error(nextErr)
		}
		if int64(h.Len()) < keep {
			heap.Push(h, doc)
		} else if compareSortedDocuments(doc, h.docs[0], sorts) < 0 {
			heap.Pop(h)
			heap.Push(h, doc)
		}
	}

	sort.Sort(&docsSorter{docs: h.docs, sorts: sorts})
	res := iterator.Values(iterator.ForSlice(h.docs))
	closer.Add(res)
	return res, nil
}

type documentsMaxHeap struct {
	docs  []*types.Document
	sorts []sortFunc
}

func (h documentsMaxHeap) Len() int      { return len(h.docs) }
func (h documentsMaxHeap) Swap(i, j int) { h.docs[i], h.docs[j] = h.docs[j], h.docs[i] }
func (h documentsMaxHeap) Less(i, j int) bool {
	return compareSortedDocuments(h.docs[i], h.docs[j], h.sorts) > 0
}
func (h *documentsMaxHeap) Push(value any) { h.docs = append(h.docs, value.(*types.Document)) }
func (h *documentsMaxHeap) Pop() any {
	last := len(h.docs) - 1
	value := h.docs[last]
	h.docs = h.docs[:last]
	return value
}
