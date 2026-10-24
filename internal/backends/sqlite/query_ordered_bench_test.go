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

package sqlite

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/state"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

// BenchmarkOrderedTopTen is the Admin Panel's People page: the ten newest of
// 30,000 users of about 1.5 kB each, sorted by the database or in Go.
func BenchmarkOrderedTopTen(b *testing.B) {
	ctx := testutil.Ctx(b)
	sp, err := state.NewProvider("")
	require.NoError(b, err)
	be, err := NewBackend(&NewBackendParams{URI: testutil.TestSQLiteURI(b, ""), L: testutil.Logger(b), P: sp, BatchSize: 100})
	require.NoError(b, err)
	b.Cleanup(be.Close)

	db, err := be.Database(testutil.DatabaseName(b))
	require.NoError(b, err)
	coll, err := db.Collection(testutil.CollectionName(b))
	require.NoError(b, err)

	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	pad := strings.Repeat("x", 1200)

	for chunk := 0; chunk < 30; chunk++ {
		docs := make([]*types.Document, 1000)
		for i := range docs {
			n := chunk*1000 + i
			docs[i] = must.NotFail(types.NewDocument(
				"_id", fmt.Sprintf("user%05d", n),
				"username", fmt.Sprintf("u%d", n),
				"createdAt", start.Add(time.Duration((n*7919)%30000)*time.Minute),
				"profile", must.NotFail(types.NewDocument("fullname", pad, "boardView", "board-view-swimlanes")),
			))
		}
		_, err = coll.InsertAll(ctx, &backends.InsertAllParams{Docs: docs})
		require.NoError(b, err)
	}

	sortDoc := must.NotFail(types.NewDocument("createdAt", int64(-1)))

	b.Run("database", func(b *testing.B) {
		for range b.N {
			res, err := coll.Query(ctx, &backends.QueryParams{OrderBy: sortDoc})
			require.NoError(b, err)
			closer := iterator.NewMultiCloser()
			iter, err := common.OrderedSortIterator(res.Iter, closer, sortDoc, 10)
			require.NoError(b, err)
			docs, err := iterator.ConsumeValuesN(iter, 10)
			require.NoError(b, err)
			require.Len(b, docs, 10)
			closer.Close()
		}
	})

	b.Run("go", func(b *testing.B) {
		for range b.N {
			res, err := coll.Query(ctx, nil)
			require.NoError(b, err)
			closer := iterator.NewMultiCloser()
			iter, err := common.SortLimitIterator(res.Iter, closer, sortDoc, 10)
			require.NoError(b, err)
			docs, err := iterator.ConsumeValuesN(iter, 10)
			require.NoError(b, err)
			require.Len(b, docs, 10)
			closer.Close()
		}
	})

	// No limit: the first batch of a cursor (101 documents) of a sort that keeps
	// everything - what a client waits for before it sees anything.
	b.Run("database-first-batch", func(b *testing.B) {
		for range b.N {
			res, err := coll.Query(ctx, &backends.QueryParams{OrderBy: sortDoc})
			require.NoError(b, err)
			closer := iterator.NewMultiCloser()
			iter, err := common.OrderedSortIterator(res.Iter, closer, sortDoc, 0)
			require.NoError(b, err)
			docs, err := iterator.ConsumeValuesN(iter, 101)
			require.NoError(b, err)
			require.Len(b, docs, 101)
			closer.Close()
		}
	})

	b.Run("go-first-batch", func(b *testing.B) {
		for range b.N {
			res, err := coll.Query(ctx, nil)
			require.NoError(b, err)
			closer := iterator.NewMultiCloser()
			iter, err := common.SortIterator(res.Iter, closer, sortDoc)
			require.NoError(b, err)
			docs, err := iterator.ConsumeValuesN(iter, 101)
			require.NoError(b, err)
			require.Len(b, docs, 101)
			closer.Close()
		}
	})
}
