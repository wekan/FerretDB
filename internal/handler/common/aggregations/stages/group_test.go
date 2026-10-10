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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// runGroup runs one $group stage over docs and returns its output.
func runGroup(t *testing.T, stage *types.Document, docs ...*types.Document) []*types.Document {
	t.Helper()

	s, err := newGroup(stage)
	require.NoError(t, err)

	closer := iterator.NewMultiCloser()
	defer closer.Close()

	out, err := s.Process(context.Background(), iterator.Values(iterator.ForSlice(docs)), closer)
	require.NoError(t, err)

	res, err := iterator.ConsumeValues(iterator.Interface[struct{}, *types.Document](out))
	require.NoError(t, err)

	return res
}

// Every accumulator of a $group sees every document of the group. They shared
// one iterator, so only the FIRST did: $min and $max after $avg were null, and
// $last and $push after $first were null and []. Found by comparing the
// conformance catalogue with MongoDB 8 and 9.
func TestGroupEveryAccumulatorSeesTheWholeGroup(t *testing.T) {
	t.Parallel()

	docs := []*types.Document{
		must.NotFail(types.NewDocument("_id", int32(1), "name", "alpha", "n", int32(5))),
		must.NotFail(types.NewDocument("_id", int32(2), "name", "Beta", "n", int32(-3))),
		must.NotFail(types.NewDocument("_id", int32(3), "name", "gamma", "n", int32(0))),
		must.NotFail(types.NewDocument("_id", int32(4), "name", "delta", "n", int32(42))),
		must.NotFail(types.NewDocument("_id", int32(5), "name", "epsilon", "n", types.Null)),
	}

	stage := must.NotFail(types.NewDocument("$group", must.NotFail(types.NewDocument(
		"_id", types.Null,
		"first", must.NotFail(types.NewDocument("$first", "$name")),
		"last", must.NotFail(types.NewDocument("$last", "$name")),
		"all", must.NotFail(types.NewDocument("$push", "$name")),
		"min", must.NotFail(types.NewDocument("$min", "$n")),
		"max", must.NotFail(types.NewDocument("$max", "$n")),
		"count", must.NotFail(types.NewDocument("$sum", int32(1))),
	))))

	res := runGroup(t, stage, docs...)
	require.Len(t, res, 1)

	got := res[0]
	assert.Equal(t, "alpha", must.NotFail(got.Get("first")))
	assert.Equal(t, "epsilon", must.NotFail(got.Get("last")))
	assert.Equal(t,
		must.NotFail(types.NewArray("alpha", "Beta", "gamma", "delta", "epsilon")),
		must.NotFail(got.Get("all")))
	// $min and $max skip null as well as missing, as MongoDB does.
	assert.Equal(t, int32(-3), must.NotFail(got.Get("min")))
	assert.Equal(t, int32(42), must.NotFail(got.Get("max")))
	assert.Equal(t, int32(5), must.NotFail(got.Get("count")))
}

// Negative: a group whose values are all null or missing still answers null.
func TestGroupMinMaxOfOnlyNullIsNull(t *testing.T) {
	t.Parallel()

	stage := must.NotFail(types.NewDocument("$group", must.NotFail(types.NewDocument(
		"_id", types.Null,
		"min", must.NotFail(types.NewDocument("$min", "$n")),
		"max", must.NotFail(types.NewDocument("$max", "$n")),
	))))

	res := runGroup(t, stage,
		must.NotFail(types.NewDocument("_id", int32(1), "n", types.Null)),
		must.NotFail(types.NewDocument("_id", int32(2))),
	)
	require.Len(t, res, 1)
	assert.Equal(t, types.Null, must.NotFail(res[0].Get("min")))
	assert.Equal(t, types.Null, must.NotFail(res[0].Get("max")))
}
