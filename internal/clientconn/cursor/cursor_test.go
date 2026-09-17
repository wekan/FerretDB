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

package cursor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/iterator/testiterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

func TestCursor(t *testing.T) {
	t.Parallel()

	r := NewRegistry(testutil.Logger(t))
	t.Cleanup(r.Close)

	ctx := testutil.Ctx(t)

	doc1 := must.NotFail(types.NewDocument("v", int32(1)))
	doc2 := must.NotFail(types.NewDocument("v", int32(2)))
	doc3 := must.NotFail(types.NewDocument("v", int32(3)))

	doc1.SetRecordID(101)
	doc2.SetRecordID(102)
	doc3.SetRecordID(103)

	two := []*types.Document{doc1, doc2}
	all := []*types.Document{doc1, doc2, doc3}

	t.Run("Normal", func(t *testing.T) {
		t.Parallel()

		params := &NewParams{
			Type: Normal,
		}

		testiterator.TestIterator(t, func() iterator.Interface[struct{}, *types.Document] {
			return r.NewCursor(ctx, iterator.Values(iterator.ForSlice(all)), params)
		})

		t.Run("Consume", func(t *testing.T) {
			t.Parallel()

			c := r.NewCursor(ctx, iterator.Values(iterator.ForSlice(all)), params)

			actual, err := iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Equal(t, all, actual)

			_, _, err = c.Next()
			assert.ErrorIs(t, err, iterator.ErrIteratorDone)

			assert.Nil(t, r.Get(c.ID), "cursor should be removed")
		})

		t.Run("Context", func(t *testing.T) {
			t.Parallel()

			testCtx, cancel := context.WithCancel(ctx)

			c := r.NewCursor(testCtx, iterator.Values(iterator.ForSlice(all)), params)

			cancel()
			<-testCtx.Done()
			time.Sleep(time.Second)

			_, _, err := c.Next()
			assert.ErrorIs(t, err, iterator.ErrIteratorDone)

			assert.Nil(t, r.Get(c.ID), "cursor should be removed")
		})

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			c := r.NewCursor(ctx, iterator.Values(iterator.ForSlice(two)), params)

			actual, err := iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Equal(t, two, actual)

			assert.PanicsWithValue(t, "Reset called on non-tailable cursor", func() {
				iter := iterator.Values(iterator.ForSlice(all))
				t.Cleanup(iter.Close)

				_ = c.Reset(iter)
			})
		})
	})

	t.Run("Tailable", func(t *testing.T) {
		t.Parallel()

		params := &NewParams{
			Type: Tailable,
		}

		t.Run("Consume", func(t *testing.T) {
			t.Parallel()

			c := r.NewCursor(ctx, iterator.Values(iterator.ForSlice(all)), params)

			actual, err := iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Equal(t, all, actual)

			_, _, err = c.Next()
			assert.ErrorIs(t, err, iterator.ErrIteratorDone)

			assert.Same(t, c, r.Get(c.ID), "cursor should not be removed")
		})

		t.Run("Context", func(t *testing.T) {
			t.Parallel()

			testCtx, cancel := context.WithCancel(ctx)

			c := r.NewCursor(testCtx, iterator.Values(iterator.ForSlice(all)), params)

			cancel()
			<-testCtx.Done()
			time.Sleep(time.Second)

			_, _, err := c.Next()
			assert.ErrorIs(t, err, iterator.ErrIteratorDone)

			assert.Nil(t, r.Get(c.ID), "cursor should be removed")
		})

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			c := r.NewCursor(ctx, iterator.Values(iterator.ForSlice(two)), params)

			actual, err := iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Equal(t, two, actual)

			err = c.Reset(iterator.Values(iterator.ForSlice(all)))
			require.NoError(t, err)

			assert.Same(t, c, r.Get(c.ID), "cursor should not be replaced")

			actual, err = iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Equal(t, []*types.Document{doc3}, actual)
		})

		t.Run("ResetFromEmpty", func(t *testing.T) {
			t.Parallel()

			// A tailable cursor whose first batch was empty (an idle tail): nothing was
			// consumed, so lastRecordID is 0. Reset must NOT error and must iterate the new
			// data from the beginning. Previously it scanned for record id 0, never found
			// it, exhausted the iterator and errored — which is why such a cursor could not
			// be kept open and resumed with getMore.
			c := r.NewCursor(ctx, iterator.Values(iterator.ForSlice([]*types.Document{})), params)

			actual, err := iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Empty(t, actual)

			err = c.Reset(iterator.Values(iterator.ForSlice(all)))
			require.NoError(t, err)

			assert.Same(t, c, r.Get(c.ID), "cursor should not be removed")

			actual, err = iterator.ConsumeValues(c)
			require.NoError(t, err)
			assert.Equal(t, all, actual)
		})
	})
}

// A retry that times out while skipping old records must not rewind the cursor.
func TestResetPreservesCheckpoint(t *testing.T) {
	for _, typ := range []Type{Tailable, TailableAwait} {
		for _, scanError := range []error{iterator.ErrIteratorDone, context.DeadlineExceeded} {
			t.Run(typ.String()+"/"+scanError.Error(), func(t *testing.T) {
				r := NewRegistry(testutil.Logger(t))
				t.Cleanup(r.Close)
				docs := make([]*types.Document, 3)
				for i := range docs {
					docs[i] = must.NotFail(types.NewDocument("v", int32(i)))
					docs[i].SetRecordID(int64(i + 101))
				}
				c := r.NewCursor(testutil.Ctx(t), iterator.Values(iterator.ForSlice(docs[:2])), &NewParams{Type: typ})
				_, err := iterator.ConsumeValues(c)
				require.NoError(t, err)
				failed := &interruptedScan{doc: docs[0], err: scanError}
				require.ErrorIs(t, c.Reset(failed), scanError)
				assert.True(t, failed.closed, "failed scan releases its iterator")
				_, _, err = c.Next()
				require.ErrorIs(t, err, iterator.ErrIteratorDone, "failed scan exposes no old records")
				assert.Equal(t, int64(102), c.lastRecordID, "skipped records were already delivered")
				require.NoError(t, c.Reset(iterator.Values(iterator.ForSlice(docs))))
				actual, err := iterator.ConsumeValues(c)
				require.NoError(t, err)
				assert.Equal(t, docs[2:], actual, "retry must deliver only new records")
			})
		}
	}
}

type interruptedScan struct {
	doc    *types.Document
	err    error
	closed bool
}

func (i *interruptedScan) Next() (struct{}, *types.Document, error) {
	if i.doc != nil {
		doc := i.doc
		i.doc = nil
		return struct{}{}, doc, nil
	}
	return struct{}{}, nil, i.err
}

func (i *interruptedScan) Close() { i.doc = nil; i.closed = true }

func TestFailedBatchPreservesCheckpoint(t *testing.T) {
	r := NewRegistry(testutil.Logger(t))
	t.Cleanup(r.Close)
	doc := must.NotFail(types.NewDocument("v", int32(1)))
	doc.SetRecordID(101)
	c := r.NewCursor(testutil.Ctx(t), &interruptedScan{doc: doc, err: context.DeadlineExceeded}, &NewParams{Type: TailableAwait})
	_, err := c.ReadBatch(2)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, c.LastRecordID(), "an unsent batch must be retried")
	require.NoError(t, c.Reset(iterator.Values(iterator.ForSlice([]*types.Document{doc}))))
	got, err := c.ReadBatch(2)
	require.NoError(t, err)
	assert.Equal(t, []*types.Document{doc}, got)
}
