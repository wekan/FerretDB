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
	"github.com/FerretDB/wire"
	"github.com/FerretDB/wire/wirebson"

	"github.com/FerretDB/FerretDB/internal/bson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// opMsgDocument gets a raw document from section 0 and converts to [*types.Document].
// Then it decodes raw documents from sections 1, if any, and appends them
// under the sequence field defined by the write command.
func opMsgDocument(msg *wire.OpMsg) (*types.Document, error) {
	doc, _, sequence, err := msg.Sections()
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	res, err := bson.ToDocument(doc)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if len(sequence) == 0 {
		return res, nil
	}

	var identifier string
	switch res.Command() {
	case "insert":
		identifier = "documents"
	case "update":
		identifier = "updates"
	case "delete":
		identifier = "deletes"
	default:
		return nil, lazyerrors.Errorf("unsupported document sequence for command %q", res.Command())
	}

	a := types.MakeArray(0)
	for len(sequence) > 0 {
		length, findErr := wirebson.FindRaw(sequence)
		if findErr != nil {
			return nil, lazyerrors.Error(findErr)
		}

		var sequenceDoc *types.Document
		if sequenceDoc, err = bson.ToDocument(wirebson.RawDocument(sequence[:length])); err != nil {
			return nil, lazyerrors.Error(err)
		}

		a.Append(sequenceDoc)
		sequence = sequence[length:]
	}

	res.Set(identifier, a)

	return res, nil
}

// documentOpMsg converts the document to [*wirebson.Document].
func documentOpMsg(doc *types.Document) (*wire.OpMsg, error) {
	return wire.NewOpMsg(must.NotFail(bson.FromDocument(doc)))
}
