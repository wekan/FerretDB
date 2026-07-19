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
	"strings"

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgReplSetGetConfig implements `replSetGetConfig` command.
//
// FerretDB v1 presents a single-node, always-primary replica set of one (see
// MsgHello) so Meteor can tail the OpLog instead of poll-and-diff. This returns a
// minimal but valid config document for that one-member set, which `rs.conf()`
// and some driver/tool code paths query. It errors when no replica-set name is
// configured (FERRETDB_REPL_SET_NAME).
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgReplSetGetConfig(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	if h.ReplSetName == "" {
		return nil, handlererrors.NewCommandErrorMsg(
			handlererrors.ErrIllegalOperation,
			"not running with a replica set (set FERRETDB_REPL_SET_NAME to enable OpLog tailing)",
		)
	}

	host := h.TCPHost
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}

	member := must.NotFail(types.NewDocument(
		"_id", int32(0),
		"host", host,
		"arbiterOnly", false,
		"priority", float64(1),
		"votes", int32(1),
	))

	config := must.NotFail(types.NewDocument(
		"_id", h.ReplSetName,
		"version", int32(1),
		"members", must.NotFail(types.NewArray(member)),
	))

	res := must.NotFail(types.NewDocument(
		"config", config,
		"ok", float64(1),
	))

	return documentOpMsg(res)
}
