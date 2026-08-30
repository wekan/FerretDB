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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestProcessPullArrayUpdateExpressionDocumentCondition(t *testing.T) {
	t.Parallel()

	kept := must.NotFail(types.NewDocument("_id", "kept", "value", true))
	removed := must.NotFail(types.NewDocument("_id", "removed", "value", false))
	array := must.NotFail(types.NewArray(kept, removed))
	doc := must.NotFail(types.NewDocument("customFields", array))
	condition := must.NotFail(types.NewDocument("_id", "removed"))

	changed, err := processPullArrayUpdateExpression("update", doc, "customFields", condition)
	require.NoError(t, err)
	require.True(t, changed)

	actual := must.NotFail(doc.Get("customFields")).(*types.Array)
	require.Equal(t, 1, actual.Len())
	require.Equal(t, types.Equal, types.Compare(kept, must.NotFail(actual.Get(0))))
}

func TestProcessPullArrayUpdateExpressionDocumentConditionNoMatch(t *testing.T) {
	t.Parallel()

	field := must.NotFail(types.NewDocument("_id", "kept", "value", true))
	array := must.NotFail(types.NewArray(field))
	doc := must.NotFail(types.NewDocument("customFields", array))
	condition := must.NotFail(types.NewDocument("_id", "missing"))

	changed, err := processPullArrayUpdateExpression("update", doc, "customFields", condition)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 1, array.Len())
}
