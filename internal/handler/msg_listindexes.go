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
	"context"
	"fmt"

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgListIndexes implements `listIndexes` command.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgListIndexes(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	document, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	command := document.Command()

	dbName, err := common.GetRequiredParam[string](document, "$db")
	if err != nil {
		return nil, err
	}

	collection, err := common.GetRequiredParam[string](document, command)
	if err != nil {
		return nil, err
	}

	db, err := h.b.Database(dbName)
	if err != nil {
		if backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseNameIsInvalid) {
			msg := fmt.Sprintf("Invalid database specified '%s'", dbName)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrInvalidNamespace, msg, document.Command())
		}

		return nil, lazyerrors.Error(err)
	}

	c, err := db.Collection(collection)
	if err != nil {
		if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionNameIsInvalid) {
			msg := fmt.Sprintf("Invalid collection name: %s", collection)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrInvalidNamespace, msg, document.Command())
		}

		return nil, lazyerrors.Error(err)
	}

	res, err := c.ListIndexes(connCtx, nil)
	if err != nil {
		if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionDoesNotExist) {
			msg := fmt.Sprintf("ns does not exist: %s.%s", dbName, collection)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrNamespaceNotFound, msg, document.Command())
		}

		return nil, lazyerrors.Error(err)
	}

	firstBatch := types.MakeArray(len(res.Indexes))

	for _, index := range res.Indexes {
		indexKey := must.NotFail(types.NewDocument())

		var isText bool

		var isSphere2D bool

		for _, key := range index.Key {
			if key.Text {
				isText = true

				// Text index fields are reported with the string value "text".
				indexKey.Set(key.Field, "text")

				continue
			}

			if key.Sphere2D {
				isSphere2D = true

				// 2dsphere index fields are reported with the string value "2dsphere".
				indexKey.Set(key.Field, "2dsphere")

				continue
			}

			order := int32(1)
			if key.Descending {
				order = -1
			}

			indexKey.Set(key.Field, order)
		}

		indexDoc := must.NotFail(types.NewDocument(
			"v", int32(2), // for compatibility, the meaning of this field is not documented
			"key", indexKey,
			"name", index.Name,
		))

		// only non-default unique indexes should have unique field in the response
		if index.Unique && index.Name != backends.DefaultIndexName {
			indexDoc.Set("unique", index.Unique)
		}

		if index.ExpireAfterSeconds != nil {
			indexDoc.Set("expireAfterSeconds", *index.ExpireAfterSeconds)
		}

		if isText {
			// Report the text index options, defaulting like MongoDB does
			// (weight 1 per field, english, "language", textIndexVersion 3).
			weights := must.NotFail(types.NewDocument())

			for _, key := range index.Key {
				if !key.Text {
					continue
				}

				w := int32(1)
				if index.TextOptions != nil {
					if wv, ok := index.TextOptions.Weights[key.Field]; ok {
						w = wv
					}
				}

				weights.Set(key.Field, w)
			}

			defaultLanguage := "english"
			languageOverride := "language"
			textIndexVersion := int32(3)

			if index.TextOptions != nil {
				if index.TextOptions.DefaultLanguage != "" {
					defaultLanguage = index.TextOptions.DefaultLanguage
				}

				if index.TextOptions.LanguageOverride != "" {
					languageOverride = index.TextOptions.LanguageOverride
				}

				if index.TextOptions.TextIndexVersion != 0 {
					textIndexVersion = index.TextOptions.TextIndexVersion
				}
			}

			indexDoc.Set("weights", weights)
			indexDoc.Set("default_language", defaultLanguage)
			indexDoc.Set("language_override", languageOverride)
			indexDoc.Set("textIndexVersion", textIndexVersion)
		}

		if isSphere2D {
			// 2dsphere indexes report their version, defaulting like MongoDB does.
			sphereVersion := int32(3)
			if index.Sphere2DIndexVersion != 0 {
				sphereVersion = index.Sphere2DIndexVersion
			}

			indexDoc.Set("2dsphereIndexVersion", sphereVersion)
		}

		// Hidden, collation and partialFilterExpression are stored and reported but
		// not enforced by FerretDB.
		if index.Hidden {
			indexDoc.Set("hidden", true)
		}

		if index.Collation != nil {
			indexDoc.Set("collation", index.Collation)
		}

		if index.PartialFilterExpression != nil {
			indexDoc.Set("partialFilterExpression", index.PartialFilterExpression)
		}

		firstBatch.Append(indexDoc)
	}

	return documentOpMsg(
		must.NotFail(types.NewDocument(
			"cursor", must.NotFail(types.NewDocument(
				"id", int64(0),
				"ns", fmt.Sprintf("%s.%s", dbName, collection),
				"firstBatch", firstBatch,
			)),
			"ok", float64(1),
		)),
	)
}
