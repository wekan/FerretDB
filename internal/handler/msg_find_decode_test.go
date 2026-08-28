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

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
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
