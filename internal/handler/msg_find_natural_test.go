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
	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNaturalFindRemainsLazy(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "Pushed", true: "NotPushed"}[disabled], func(t *testing.T) {
			docs := []*types.Document{
				must.NotFail(types.NewDocument("_id", int32(3), "matches", false)),
				must.NotFail(types.NewDocument("_id", int32(2), "matches", true)),
				must.NotFail(types.NewDocument("_id", int32(1), "matches", true)),
			}
			for i, doc := range docs {
				doc.SetRecordID(int64(3 - i))
			}
			input := &countedFindIterator{DocumentsIterator: iterator.Values(iterator.ForSlice(docs))}
			closer := iterator.NewMultiCloser()
			defer closer.Close()
			h := &Handler{NewOpts: &NewOpts{DisablePushdown: disabled}}
			result, err := h.makeFindIter(input, closer, &common.FindParams{
				Filter: must.NotFail(types.NewDocument("matches", true)),
				Sort:   must.NotFail(types.NewDocument("$natural", int64(-1))), Limit: 1,
			})
			require.NoError(t, err)
			got, err := iterator.ConsumeValues(result)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, int32(2), must.NotFail(got[0].Get("_id")))
			if !disabled {
				assert.Equal(t, 2, input.reads, "do not read older entries after finding the newest match")
			} else {
				assert.Greater(t, input.reads, 2)
			}
		})
	}
}

type countedFindIterator struct {
	types.DocumentsIterator
	reads int
}

func (i *countedFindIterator) Next() (struct{}, *types.Document, error) {
	i.reads++
	return i.DocumentsIterator.Next()
}

func TestNaturalFindLimitPushdown(t *testing.T) {
	h := &Handler{NewOpts: &NewOpts{L: testutil.Logger(t)}}
	for _, tc := range []struct {
		name         string
		sort, filter *types.Document
		want         int64
	}{
		{"Natural", must.NotFail(types.NewDocument("$natural", int64(-1))), nil, 1},
		{"Filtered", must.NotFail(types.NewDocument("$natural", int64(-1))), must.NotFail(types.NewDocument("v", int32(1))), 0},
		{"FieldSort", must.NotFail(types.NewDocument("v", int64(-1))), nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qp, err := h.makeFindQueryParams(testutil.Ctx(t), &common.FindParams{Sort: tc.sort, Filter: tc.filter, Limit: 1}, &backends.CollectionInfo{CappedSize: 8192})
			require.NoError(t, err)
			assert.Equal(t, tc.want, qp.Limit)
		})
	}
}
