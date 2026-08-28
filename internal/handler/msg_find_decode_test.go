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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/testutil"
)

func TestCollectDecodeFields(t *testing.T) {
	t.Parallel()

	fields := make(map[string]struct{})
	collectDecodeFields(must.NotFail(types.NewDocument(
		"_id", int64(1),
		"profile.name", int64(1),
		"services.resume.tokens.$", int64(1),
		"$and", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("archived", false)),
			must.NotFail(types.NewDocument("members.userId", "u1")),
		)),
	)), fields)

	assert.Equal(t, map[string]struct{}{
		"_id": {}, "profile": {}, "services": {}, "archived": {}, "members": {},
	}, fields)
}

func TestFindDecodeFieldsAlwaysKeepID(t *testing.T) {
	t.Parallel()

	h := &Handler{NewOpts: &NewOpts{L: testutil.Logger(t)}}
	for name, projection := range map[string]*types.Document{
		"Implicit":          must.NotFail(types.NewDocument("title", int64(1))),
		"ExplicitExclusion": must.NotFail(types.NewDocument("title", int64(1), "_id", int64(0))),
	} {
		t.Run(name, func(t *testing.T) {
			params := &common.FindParams{
				Filter:     must.NotFail(types.NewDocument("archived", false)),
				Sort:       must.NotFail(types.NewDocument()),
				Projection: projection,
			}
			qp, err := h.makeFindQueryParams(testutil.Ctx(t), params, &backends.CollectionInfo{})
			require.NoError(t, err)
			assert.Contains(t, qp.DecodeFields, "_id")
			assert.Contains(t, qp.DecodeFields, "title")
			assert.Contains(t, qp.DecodeFields, "archived")
		})
	}
}

func TestDistinctDecodeFields(t *testing.T) {
	t.Parallel()

	filter := must.NotFail(types.NewDocument(
		"archived", false,
		"$and", must.NotFail(types.NewArray(
			must.NotFail(types.NewDocument("members.userId", "u1")),
		)),
	))
	assert.Equal(t,
		[]string{"archived", "members", "swimlane"},
		distinctDecodeFields("swimlane.id", filter),
	)
}
