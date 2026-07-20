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

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgWekanThrottle implements the custom `wekanThrottle` command.
//
// WeKan calls this when it detects that the host CPU is high (WeKan and FerretDB
// share the machine). The command (1) reports what FerretDB is doing — a running
// count of commands processed, the activity signal — and (2) asks FerretDB to slow
// down: for the next `durationMs`, pause `slowDownMs` before each command, lowering
// FerretDB's CPU use and yielding to other software. The throttle self-expires.
//
// Parameters (all optional):
//   - slowDownMs: pause per command while active (default 5, clamped to [0,1000]).
//   - durationMs: how long the throttle lasts (default 2000, clamped to [0,300000]).
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgWekanThrottle(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	document, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	slowDownMs := int64(5)
	if v, _ := document.Get("slowDownMs"); v != nil {
		if n, e := handlerparams.GetWholeNumberParam(v); e == nil {
			slowDownMs = n
		}
	}

	durationMs := int64(2000)
	if v, _ := document.Get("durationMs"); v != nil {
		if n, e := handlerparams.GetWholeNumberParam(v); e == nil {
			durationMs = n
		}
	}

	until := wekanThrottleSet(slowDownMs, durationMs)
	active, sleepMs, _, commands := wekanThrottleStatus()

	return documentOpMsg(
		must.NotFail(types.NewDocument(
			"ok", float64(1),
			// What FerretDB is doing: a running count of processed commands (higher =
			// busier). WeKan reads this to describe FerretDB's activity in the log.
			"commandsProcessed", commands,
			// What FerretDB was asked to do / is doing about it.
			"throttled", active,
			"slowDownMs", sleepMs,
			"durationMs", durationMs,
			"untilUnixNano", until,
			"note", "FerretDB pauses slowDownMs before each command until the deadline to yield CPU",
		)),
	)
}
