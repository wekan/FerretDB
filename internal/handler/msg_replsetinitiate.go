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
// FerretDB v1 does not implement real (multi-node) replication or leader
// election. It presents a single-node, always-primary replica set of one, whose
// only purpose is to let Meteor tail the OpLog instead of poll-and-diff. When a
// replica-set name is configured (`FERRETDB_REPL_SET_NAME`) this command ensures
// the capped `local.oplog.rs` collection exists (the same collection auto-created
// at startup), so `rs.initiate()` from a driver/tool leaves the server ready for
// OpLog tailing. It accepts the call with or without a configuration document.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgReplSetInitiate(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	document, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	reply := must.NotFail(types.NewDocument())

	// If a configuration document with an `_id` (the replica set name) is provided,
	// echo it back for compatibility.
	if config, err := common.GetOptionalParam(document, "replSetInitiate", (*types.Document)(nil)); err == nil && config != nil {
		if setName, err := common.GetOptionalParam(config, "_id", ""); err == nil && setName != "" {
			reply.Set("setName", setName)
		}
	}

	// Ensure the oplog exists so tailing works after an explicit initiate.
	// No-op unless FERRETDB_REPL_SET_NAME is set.
	h.ensureOplog()

	reply.Set("ok", float64(1))

	return documentOpMsg(reply)
}
