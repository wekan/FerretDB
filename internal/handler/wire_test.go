// Copyright 2026 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handler

import (
	"math"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/bson"
	"github.com/FerretDB/FerretDB/internal/types"
)

func TestWireAcceptsNaNInDocumentSequence(t *testing.T) {
	require.False(t, wire.CheckNaNs)

	inserted, err := types.NewDocument("value", math.NaN())
	require.NoError(t, err)
	docs, err := types.NewArray(inserted)
	require.NoError(t, err)
	command, err := types.NewDocument("insert", "values", "documents", docs, "$db", "test")
	require.NoError(t, err)
	wireDoc, err := bson.FromDocument(command)
	require.NoError(t, err)

	msg, err := wire.NewOpMsg(wireDoc)
	require.NoError(t, err)
	b, err := msg.MarshalBinary()
	require.NoError(t, err)

	var decoded wire.OpMsg
	require.NoError(t, decoded.UnmarshalBinaryNocopy(b))
	actual, err := opMsgDocument(&decoded)
	require.NoError(t, err)

	actualDocsValue, err := actual.Get("documents")
	require.NoError(t, err)
	actualDocs, ok := actualDocsValue.(*types.Array)
	require.True(t, ok)
	actualDocValue, err := actualDocs.Get(0)
	require.NoError(t, err)
	actualDoc, ok := actualDocValue.(*types.Document)
	require.True(t, ok)
	valueRaw, err := actualDoc.Get("value")
	require.NoError(t, err)
	value, ok := valueRaw.(float64)
	require.True(t, ok)
	require.True(t, math.IsNaN(value))
}
