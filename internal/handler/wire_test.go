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
	"encoding/binary"
	"math"
	"testing"

	"github.com/FerretDB/wire"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/bson"
	"github.com/FerretDB/FerretDB/internal/types"
)

func opMsgWithSequence(t *testing.T, command *types.Document, identifier string, docs ...*types.Document) *wire.OpMsg {
	t.Helper()

	wireCommand, err := bson.FromDocument(command)
	require.NoError(t, err)
	msg, err := wire.NewOpMsg(wireCommand)
	require.NoError(t, err)
	body, err := msg.MarshalBinary()
	require.NoError(t, err)

	section := make([]byte, 5, 64)
	section[0] = 1
	section = append(section, identifier...)
	section = append(section, 0)

	for _, doc := range docs {
		wireDoc, docErr := bson.FromDocument(doc)
		require.NoError(t, docErr)
		raw, encodeErr := wireDoc.Encode()
		require.NoError(t, encodeErr)
		section = append(section, raw...)
	}

	binary.LittleEndian.PutUint32(section[1:5], uint32(len(section)-1))
	body = append(body, section...)

	var decoded wire.OpMsg
	require.NoError(t, decoded.UnmarshalBinaryNocopy(body))

	return &decoded
}

func TestOpMsgDocumentSequence(t *testing.T) {
	first, err := types.NewDocument("n", int32(1))
	require.NoError(t, err)
	second, err := types.NewDocument("n", int32(2))
	require.NoError(t, err)
	command, err := types.NewDocument("insert", "values", "$db", "test")
	require.NoError(t, err)

	actual, err := opMsgDocument(opMsgWithSequence(t, command, "documents", first, second))
	require.NoError(t, err)
	value, err := actual.Get("documents")
	require.NoError(t, err)
	docs, ok := value.(*types.Array)
	require.True(t, ok)
	require.Equal(t, 2, docs.Len())

	unsupported, err := types.NewDocument("find", "values", "$db", "test")
	require.NoError(t, err)
	_, err = opMsgDocument(opMsgWithSequence(t, unsupported, "documents", first))
	require.ErrorContains(t, err, `unsupported document sequence for command "find"`)
}

func TestWireAcceptsNaNInDocumentSequence(t *testing.T) {
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
