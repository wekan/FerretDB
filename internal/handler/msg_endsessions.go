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

// sessionOKResponse builds the standard `{ok: 1}` reply shared by the session
// lifecycle commands below.
func sessionOKResponse() (*wire.OpMsg, error) {
	return documentOpMsg(
		must.NotFail(types.NewDocument(
			"ok", float64(1),
		)),
	)
}

// MsgEndSessions implements `endSessions` command.
//
// Logical sessions in FerretDB v1 carry no server-side state, so ending them is a
// compatibility no-op that always succeeds so MongoDB drivers do not error.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgEndSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	return sessionOKResponse()
}

// MsgRefreshSessions implements `refreshSessions` command.
//
// Logical sessions in FerretDB v1 carry no server-side state, so refreshing them is
// a compatibility no-op that always succeeds so MongoDB drivers do not error.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgRefreshSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	return sessionOKResponse()
}

// MsgKillSessions implements `killSessions` command.
//
// Logical sessions in FerretDB v1 carry no server-side state, so killing them is a
// compatibility no-op that always succeeds so MongoDB drivers do not error.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgKillSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	return sessionOKResponse()
}

// MsgKillAllSessions implements `killAllSessions` command.
//
// Logical sessions in FerretDB v1 carry no server-side state, so killing them is a
// compatibility no-op that always succeeds so MongoDB drivers do not error.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgKillAllSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	return sessionOKResponse()
}

// MsgKillAllSessionsByPattern implements `killAllSessionsByPattern` command.
//
// Logical sessions in FerretDB v1 carry no server-side state, so killing them is a
// compatibility no-op that always succeeds so MongoDB drivers do not error.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgKillAllSessionsByPattern(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	return sessionOKResponse()
}
