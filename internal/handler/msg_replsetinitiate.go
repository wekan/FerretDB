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

	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgReplSetInitiate implements `replSetInitiate` command.
//
// FerretDB v1 does not implement real replication. Its oplog is tailing-only and
// must be configured manually by creating a capped `local.oplog.rs` collection and
// setting `FERRETDB_REPL_SET_NAME`. This command exists as a compatibility no-op so
// that tools and drivers that bootstrap a replica set do not hard-fail. It accepts
// the call (with or without a configuration document) and returns success, but it
// does NOT create an oplog, elect a primary, or change the server topology in any way.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgReplSetInitiate(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	document, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	reply := must.NotFail(types.NewDocument())

	// If a configuration document with an `_id` (the replica set name) is provided,
	// echo it back for compatibility. This is purely informational; no real replica
	// set is created.
	if config, err := common.GetOptionalParam(document, "replSetInitiate", (*types.Document)(nil)); err == nil && config != nil {
		if setName, err := common.GetOptionalParam(config, "_id", ""); err == nil && setName != "" {
			reply.Set("setName", setName)
		}
	}

	reply.Set("ok", float64(1))

	return documentOpMsg(reply)
}
