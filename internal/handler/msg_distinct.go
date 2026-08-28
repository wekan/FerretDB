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
	"slices"
	"strings"

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgDistinct implements `distinct` command.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgDistinct(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	document, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	params, err := common.GetDistinctParams(document, h.L)
	if err != nil {
		return nil, err
	}

	db, err := h.b.Database(params.DB)
	if err != nil {
		if backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseNameIsInvalid) {
			msg := fmt.Sprintf("Invalid namespace specified '%s.%s'", params.DB, params.Collection)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrInvalidNamespace, msg, document.Command())
		}

		return nil, lazyerrors.Error(err)
	}

	c, err := db.Collection(params.Collection)
	if err != nil {
		if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionNameIsInvalid) {
			msg := fmt.Sprintf("Invalid collection name: %s", params.Collection)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrInvalidNamespace, msg, document.Command())
		}

		return nil, lazyerrors.Error(err)
	}

	closer := iterator.NewMultiCloser()
	defer closer.Close()

	var qp backends.QueryParams
	if !h.DisablePushdown {
		qp.Filter = params.Filter
	}

	// Distinct consumes only its key and the fields observed by its filter. The
	// SQLite backend stores whole SJSON documents, so telling it that bounded
	// field set avoids recursively decoding unrelated large arrays/objects from
	// every candidate. Keep _id as the document invariant expected by handler
	// iterators even though distinct does not return it.
	qp.DecodeFields = distinctDecodeFields(params.Key, params.Filter)
	if !strings.ContainsRune(params.Key, '.') {
		qp.DistinctField = params.Key
	}

	// TODO https://github.com/FerretDB/FerretDB/issues/3235
	queryRes, err := c.Query(connCtx, &qp)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	closer.Add(queryRes.Iter)

	iter := common.FilterIterator(queryRes.Iter, closer, params.Filter)

	distinct, err := common.FilterDistinctValues(iter, params.Key)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return documentOpMsg(
		must.NotFail(types.NewDocument(
			"values", distinct,
			"ok", float64(1),
		)),
	)
}

func distinctDecodeFields(key string, filter *types.Document) []string {
	fields := make(map[string]struct{})
	collectDecodeFields(filter, fields)
	root, _, _ := strings.Cut(key, ".")
	if root != "" {
		fields[root] = struct{}{}
	}
	res := make([]string, 0, len(fields))
	for field := range fields {
		res = append(res, field)
	}
	slices.Sort(res)
	return res
}
