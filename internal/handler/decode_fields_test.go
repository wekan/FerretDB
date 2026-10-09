// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

func TestFilterDecodeFields(t *testing.T) {
	t.Parallel()

	filter := must.NotFail(types.NewDocument(
		"boardId", "b1",
		"archived", false,
		"meta.cardId", must.NotFail(types.NewDocument("$in", must.NotFail(types.NewArray("c1")))),
		"$or", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("listId", "l1")),
		)),
	))

	assert.Equal(t, []string{"_id", "archived", "boardId", "listId", "meta"}, filterDecodeFields(filter))
	assert.Equal(t, []string{"_id"}, filterDecodeFields(nil), "an empty filter still needs nothing but _id")
}

func TestFilterDecodeFieldsWholeDocument(t *testing.T) {
	t.Parallel()

	// Each of these reads fields its keys do not name.
	for name, filter := range map[string]*types.Document{
		"Expr": must.NotFail(types.NewDocument("$expr", must.NotFail(types.NewDocument(
			"$eq", must.NotFail(types.NewArray("$a", "$b")),
		)))),
		"Where":      must.NotFail(types.NewDocument("$where", "this.a == 1")),
		"JSONSchema": must.NotFail(types.NewDocument("$jsonSchema", must.NotFail(types.NewDocument()))),
		"NestedExpr": must.NotFail(types.NewDocument("$and", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("a", int32(1))),
			must.NotFail(types.NewDocument("$expr", true)),
		)))),
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, filterNeedsWholeDocument(filter))
			assert.Nil(t, filterDecodeFields(filter))
		})
	}

	assert.False(t, filterNeedsWholeDocument(must.NotFail(types.NewDocument("a", int32(1)))))
}

func TestCountPipelineDecodeFields(t *testing.T) {
	t.Parallel()

	match := must.NotFail(types.NewDocument("$match", must.NotFail(types.NewDocument(
		"boardId", "b1", "archived", false,
	))))
	countDocuments := must.NotFail(types.NewDocument("$group", must.NotFail(types.NewDocument(
		"_id", int32(1), "n", must.NotFail(types.NewDocument("$sum", int32(1))),
	))))

	// The shape drivers send for countDocuments, with and without skip/limit.
	assert.Equal(t, []string{"_id", "archived", "boardId"},
		countPipelineDecodeFields([]any{match, countDocuments}))
	assert.Equal(t, []string{"_id", "archived", "boardId"}, countPipelineDecodeFields([]any{
		match,
		must.NotFail(types.NewDocument("$skip", int64(5))),
		must.NotFail(types.NewDocument("$limit", int64(10))),
		countDocuments,
	}))
	assert.Equal(t, []string{"_id", "archived", "boardId"}, countPipelineDecodeFields([]any{
		match, must.NotFail(types.NewDocument("$count", "n")),
	}))
}

func TestCountPipelineDecodeFieldsWholeDocument(t *testing.T) {
	t.Parallel()

	match := must.NotFail(types.NewDocument("$match", must.NotFail(types.NewDocument("a", int32(1)))))

	for name, pipeline := range map[string][]any{
		"Empty":   {},
		"NoCount": {match},
		"GroupByField": {match, must.NotFail(types.NewDocument("$group", must.NotFail(types.NewDocument(
			"_id", "$listId", "n", must.NotFail(types.NewDocument("$sum", int32(1))),
		))))},
		"SumOfField": {match, must.NotFail(types.NewDocument("$group", must.NotFail(types.NewDocument(
			"_id", types.Null, "n", must.NotFail(types.NewDocument("$sum", "$spentTime")),
		))))},
		"StageAfterCount": {
			match,
			must.NotFail(types.NewDocument("$count", "n")),
			must.NotFail(types.NewDocument("$project", must.NotFail(types.NewDocument("n", int32(1))))),
		},
		"Project": {
			match,
			must.NotFail(types.NewDocument("$project", must.NotFail(types.NewDocument("a", int32(1))))),
			must.NotFail(types.NewDocument("$count", "n")),
		},
		"ExprMatch": {
			must.NotFail(types.NewDocument("$match", must.NotFail(types.NewDocument("$expr", true)))),
			must.NotFail(types.NewDocument("$count", "n")),
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, countPipelineDecodeFields(pipeline))
		})
	}
}

func TestFindDecodeFieldsWholeDocumentFilter(t *testing.T) {
	t.Parallel()

	h := &Handler{NewOpts: &NewOpts{L: testutil.Logger(t)}}
	params := &common.FindParams{
		Filter: must.NotFail(types.NewDocument("$expr", must.NotFail(types.NewDocument(
			"$eq", must.NotFail(types.NewArray("$a", "$b")),
		)))),
		Sort:       must.NotFail(types.NewDocument()),
		Projection: must.NotFail(types.NewDocument("title", int64(1))),
	}
	qp, err := h.makeFindQueryParams(testutil.Ctx(t), params, &backends.CollectionInfo{})
	require.NoError(t, err)
	assert.Nil(t, qp.DecodeFields, "$expr reads fields a projection does not name")
}

func TestFindKeepsDottedKeysForSupersetBackends(t *testing.T) {
	t.Parallel()

	filter := must.NotFail(types.NewDocument("meta.cardId", "c1", "boardId", "b1"))

	for name, tc := range map[string]struct {
		superset bool
		keys     []string
	}{
		"Superset": {superset: true, keys: []string{"meta.cardId", "boardId"}},
		"Other":    {superset: false, keys: []string{"boardId"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := &Handler{NewOpts: &NewOpts{L: testutil.Logger(t), NestedPushdownSuperset: tc.superset}}
			params := &common.FindParams{
				Filter: filter.DeepCopy(),
				Sort:   must.NotFail(types.NewDocument()),
			}
			qp, err := h.makeFindQueryParams(testutil.Ctx(t), params, &backends.CollectionInfo{})
			require.NoError(t, err)
			assert.Equal(t, tc.keys, qp.Filter.Keys())
		})
	}
}
