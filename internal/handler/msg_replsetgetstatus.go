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
	"time"

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgReplSetGetStatus implements `replSetGetStatus` command.
//
// FerretDB v1 presents a single-node, always-primary replica set of one (see
// MsgHello) so that Meteor can tail the OpLog instead of poll-and-diff. This
// returns a minimal but valid status document describing that one PRIMARY member,
// which `rs.status()` and some driver/tool code paths query while bringing up
// OpLog tailing. It errors when no replica-set name is configured
// (FERRETDB_REPL_SET_NAME), because there is then no set to report.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgReplSetGetStatus(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
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

	now := time.Now()

	optime := must.NotFail(types.NewDocument(
		"ts", types.NextTimestamp(now),
		"t", int64(1),
	))

	member := must.NotFail(types.NewDocument(
		"_id", int32(0),
		"name", host,
		"health", float64(1),
		"state", int32(1), // PRIMARY
		"stateStr", "PRIMARY",
		"uptime", int32(0),
		"optime", optime,
		"optimeDate", now,
		"self", true,
	))

	res := must.NotFail(types.NewDocument(
		"set", h.ReplSetName,
		"date", now,
		"myState", int32(1), // PRIMARY
		"term", int64(1),
		"members", must.NotFail(types.NewArray(member)),
		"ok", float64(1),
	))

	return documentOpMsg(res)
}
