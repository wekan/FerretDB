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
	"github.com/google/uuid"

	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/handler/session"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
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
// Logical sessions are tracked in the handler's session registry (adapted from
// FerretDB v2). Ending a session marks it as ended so that it is pruned later;
// transactions themselves remain no-ops in v1. It always returns `{ok: 1}`.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgEndSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	doc, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if _, _, err = h.sessions.CreateOrUpdateByLSID(connCtx, doc); err != nil {
		return nil, err
	}

	ids, err := getSessionIDsParam(doc, "endSessions")
	if err != nil {
		return nil, err
	}

	h.sessions.EndSessions(connCtx, ids)

	return sessionOKResponse()
}

// MsgRefreshSessions implements `refreshSessions` command.
//
// It marks the referenced sessions as recently used in the session registry,
// creating them implicitly if needed, and always returns `{ok: 1}`.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgRefreshSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	doc, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if _, _, err = h.sessions.CreateOrUpdateByLSID(connCtx, doc); err != nil {
		return nil, err
	}

	ids, err := getSessionIDsParam(doc, "refreshSessions")
	if err != nil {
		return nil, err
	}

	h.sessions.CreateOrUpdateSessions(connCtx, ids)

	return sessionOKResponse()
}

// MsgKillSessions implements `killSessions` command.
//
// It removes the referenced sessions from the session registry and returns
// `{ok: 1}`. When no session IDs are given, all sessions of the current user
// are removed.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgKillSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	doc, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	userID, _, err := h.sessions.CreateOrUpdateByLSID(connCtx, doc)
	if err != nil {
		return nil, err
	}

	ids, err := getSessionIDsParam(doc, "killSessions")
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		// With access control enabled, sessions of other users would also be killed.
		h.sessions.DeleteSessionsByUserIDs([]session.UserID{userID})

		return sessionOKResponse()
	}

	h.sessions.DeleteSessionsByIDs(userID, ids)

	return sessionOKResponse()
}

// MsgKillAllSessions implements `killAllSessions` command.
//
// It removes sessions of the given users from the session registry, or all
// sessions when no users are given, and returns `{ok: 1}`.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgKillAllSessions(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	doc, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if _, _, err = h.sessions.CreateOrUpdateByLSID(connCtx, doc); err != nil {
		return nil, err
	}

	command := "killAllSessions"
	field := "KillAllSessionsCmd.killAllSessions"

	v, _ := doc.Get(command)

	userIDs, err := getSessionUsersParam(v, command, field)
	if err != nil {
		return nil, err
	}

	if len(userIDs) == 0 {
		h.sessions.DeleteAllSessions()

		return sessionOKResponse()
	}

	h.sessions.DeleteSessionsByUserIDs(userIDs)

	return sessionOKResponse()
}

// MsgKillAllSessionsByPattern implements `killAllSessionsByPattern` command.
//
// It removes the sessions matching the given patterns from the session registry
// and returns `{ok: 1}`.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgKillAllSessionsByPattern(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	doc, err := opMsgDocument(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if _, _, err = h.sessions.CreateOrUpdateByLSID(connCtx, doc); err != nil {
		return nil, err
	}

	command := "killAllSessionsByPattern"
	field := "KillAllSessionsByPatternCmd.killAllSessionsByPattern"

	v, _ := doc.Get(command)

	patternArr, ok := v.(*types.Array)
	if !ok {
		m := fmt.Sprintf("BSON field '%s' is the wrong type '%T', expected type 'array'", field, v)
		return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, m, command)
	}

	var allSessions bool
	var userIDs []session.UserID
	lsids := map[session.UserID][]uuid.UUID{}

	if patternArr.Len() == 0 {
		allSessions = true
	}

	for i := 0; i < patternArr.Len(); i++ {
		el := must.NotFail(patternArr.Get(i))

		pattern, ok := el.(*types.Document)
		if !ok {
			m := fmt.Sprintf("BSON field '%s.0' is the wrong type '%T', expected type 'object'", field, el)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, m, command)
		}

		for _, k := range pattern.Keys() {
			pv := must.NotFail(pattern.Get(k))

			switch k {
			case "lsid":
				userID, sessionID, err := getLSIDParam(pv, command, field)
				if err != nil {
					return nil, err
				}

				lsids[userID] = append(lsids[userID], sessionID)

			case "uid":
				userID, err := getUserIDParam(pv, command, field)
				if err != nil {
					return nil, err
				}

				userIDs = append(userIDs, userID)

			case "users":
				if _, err := getSessionUsersParam(pv, command, fmt.Sprintf("%s.users", field)); err != nil {
					return nil, err
				}

				// For compatibility, all sessions of all users are deleted regardless of the pattern.
				allSessions = true

			default:
				msg := fmt.Sprintf("BSON field '%s.%s' is an unknown field.", field, k)
				return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrBadValue, msg, command)
			}
		}
	}

	if allSessions {
		h.sessions.DeleteAllSessions()
	}

	if len(userIDs) > 0 {
		h.sessions.DeleteSessionsByUserIDs(userIDs)
	}

	for userID, sessionIDs := range lsids {
		h.sessions.DeleteSessionsByIDs(userID, sessionIDs)
	}

	return sessionOKResponse()
}

// getSessionIDsParam extracts the array of session IDs stored under key in doc.
// Each element must be a document with an `id` field holding a UUID binary.
func getSessionIDsParam(doc *types.Document, key string) ([]uuid.UUID, error) {
	v, _ := doc.Get(key)

	sessionsArray, ok := v.(*types.Array)
	if !ok {
		return nil, handlererrors.NewCommandErrorMsgWithArgument(
			handlererrors.ErrTypeMismatch,
			fmt.Sprintf("BSON field '%[1]s.%[1]s' is the wrong type '%[2]T', expected type 'array'", key, v),
			key,
		)
	}

	ids := make([]uuid.UUID, sessionsArray.Len())

	for i := 0; i < sessionsArray.Len(); i++ {
		el := must.NotFail(sessionsArray.Get(i))

		sessionDoc, ok := el.(*types.Document)
		if !ok {
			m := fmt.Sprintf(
				"BSON field '%[1]s.%[1]sFromClient.%[2]d' is the wrong type '%[3]T', expected type 'object'",
				key, i, el,
			)

			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, m, key)
		}

		idV, _ := sessionDoc.Get("id")
		if idV == nil {
			m := fmt.Sprintf("BSON field '%[1]s.%[1]sFromClient.id' is missing but a required field", key)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrMissingField, m, key)
		}

		id, ok := idV.(types.Binary)
		if !ok {
			m := fmt.Sprintf(
				"BSON field '%[1]s.%[1]sFromClient.id' is the wrong type '%[2]T', expected type 'binData'",
				key, idV,
			)

			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, m, key)
		}

		if id.Subtype != types.BinaryUUID {
			m := fmt.Sprintf(
				"BSON field '%[1]s.%[1]sFromClient.id' is the wrong binData type '%[2]s', expected type 'UUID'",
				key, id.Subtype.String(),
			)

			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, m, key)
		}

		sessionID, err := uuid.FromBytes(id.B)
		if err != nil {
			m := "uuid must be a 16-byte binary field with UUID (4) subtype"
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrBadValue, m, key)
		}

		ids[i] = sessionID
	}

	return ids, nil
}

// getSessionUsersParam parses the array of `{user, db}` documents from v and
// returns the corresponding user IDs.
func getSessionUsersParam(v any, command, field string) ([]session.UserID, error) {
	usersArr, ok := v.(*types.Array)
	if !ok {
		msg := fmt.Sprintf("BSON field '%s' is the wrong type '%T', expected type 'array'", field, v)
		return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
	}

	userIDs := make([]session.UserID, usersArr.Len())

	for i := 0; i < usersArr.Len(); i++ {
		el := must.NotFail(usersArr.Get(i))

		user, ok := el.(*types.Document)
		if !ok {
			msg := fmt.Sprintf("BSON field '%s.%d' is the wrong type '%T', expected type 'object'", field, i, el)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
		}

		dbV, _ := user.Get("db")
		if dbV == nil {
			msg := fmt.Sprintf("BSON field '%s.db' is missing but a required field", field)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrMissingField, msg, command)
		}

		dbName, ok := dbV.(string)
		if !ok {
			msg := fmt.Sprintf("BSON field '%s.db' is the wrong type '%T', expected type 'string'", field, dbV)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
		}

		userV, _ := user.Get("user")
		if userV == nil {
			msg := fmt.Sprintf("BSON field '%s.user' is missing but a required field", field)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrMissingField, msg, command)
		}

		username, ok := userV.(string)
		if !ok {
			msg := fmt.Sprintf("BSON field '%s.user' is the wrong type '%T', expected type 'string'", field, userV)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
		}

		userIDs[i] = session.GetUIDFromUsername(dbName, username)
	}

	return userIDs, nil
}

// getLSIDParam returns the user ID and session ID from the given `{id, uid}` document v.
func getLSIDParam(v any, command, field string) (session.UserID, uuid.UUID, error) {
	lsid, ok := v.(*types.Document)
	if !ok {
		msg := fmt.Sprintf("BSON field '%s.lsid' is the wrong type '%T', expected type 'object'", field, v)
		return session.UserID{}, uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
	}

	idV, _ := lsid.Get("id")
	if idV == nil {
		msg := fmt.Sprintf("BSON field '%s.lsid.id' is missing but a required field", field)
		return session.UserID{}, uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrMissingField, msg, command)
	}

	binaryID, ok := idV.(types.Binary)
	if !ok {
		msg := fmt.Sprintf("BSON field '%s.lsid.id' is the wrong type '%T', expected type 'binData'", field, idV)
		return session.UserID{}, uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
	}

	if binaryID.Subtype != types.BinaryUUID {
		msg := fmt.Sprintf(
			"BSON field '%s.lsid.id' is the wrong binData type '%s', expected type 'UUID'",
			field, binaryID.Subtype.String(),
		)

		return session.UserID{}, uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
	}

	sessionID, err := uuid.FromBytes(binaryID.B)
	if err != nil {
		msg := "uuid must be a 16-byte binary field with UUID (4) subtype"
		return session.UserID{}, uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrBadValue, msg, command)
	}

	uidV, _ := lsid.Get("uid")
	if uidV == nil {
		msg := fmt.Sprintf("BSON field '%s.lsid.uid' is missing but a required field", field)
		return session.UserID{}, uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrMissingField, msg, command)
	}

	userID, err := getUserIDParam(uidV, command, fmt.Sprintf("%s.lsid", field))
	if err != nil {
		return session.UserID{}, uuid.Nil, err
	}

	return userID, sessionID, nil
}

// getUserIDParam parses a generic binary from v and returns the user ID.
func getUserIDParam(v any, command, field string) (session.UserID, error) {
	binaryUserID, ok := v.(types.Binary)
	if !ok {
		msg := fmt.Sprintf("BSON field '%s.uid' is the wrong type '%T', expected type 'binData'", field, v)
		return session.UserID{}, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
	}

	if binaryUserID.Subtype != types.BinaryGeneric {
		msg := fmt.Sprintf(
			"BSON field '%s.uid' is the wrong binData type '%s', expected type 'general'",
			field, binaryUserID.Subtype.String(),
		)

		return session.UserID{}, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, command)
	}

	var userID session.UserID

	if len(binaryUserID.B) != len(userID) {
		msg := fmt.Sprintf("Unsupported SHA256Block hash length: %d", len(binaryUserID.B))
		return session.UserID{}, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrBadValue, msg, command)
	}

	copy(userID[:], binaryUserID.B)

	return userID, nil
}
