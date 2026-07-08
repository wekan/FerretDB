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

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgAbortTransaction implements `abortTransaction` command.
//
// FerretDB v1 with the SQLite backend has no real multi-document transactions;
// every write auto-commits on its own and cannot be rolled back as a group. This
// command is a compatibility no-op that always succeeds so MongoDB drivers do not
// error. Writes that already happened are NOT undone.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgAbortTransaction(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	return documentOpMsg(
		must.NotFail(types.NewDocument(
			"ok", float64(1),
		)),
	)
}
