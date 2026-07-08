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

// Package session provides access to the logical session registry.
//
// It is adapted from FerretDB v2's internal/handler/session package. Unlike v2,
// this v1 port tracks only the session lifecycle (creation, last-used time,
// ended state, per-user grouping and expiry). Cursor-association tracking from
// v2 is intentionally omitted because v1's cursor registry differs; sessions
// therefore do not carry cursor IDs here.
package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/FerretDB/FerretDB/internal/clientconn/conninfo"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/resource"
)

// LogicalSessionTimeoutMinutes is the session timeout in minutes.
const LogicalSessionTimeoutMinutes = int32(30)

// UserID is the output computed by the SHA256 function.
type UserID [sha256.Size]byte

// String returns the base64 encoded string.
func (s UserID) String() string {
	return base64.StdEncoding.EncodeToString(s[:])
}

// sessionInfo contains information about a single session.
type sessionInfo struct {
	created  time.Time
	lastUsed time.Time
	ended    bool

	token *resource.Token
}

// newSessionInfo returns new session information.
func newSessionInfo() *sessionInfo {
	now := time.Now()

	s := &sessionInfo{
		created:  now,
		lastUsed: now,
		token:    resource.NewToken(),
	}

	resource.Track(s, s.token)

	return s
}

// close untracks the session information.
func (s *sessionInfo) close() {
	resource.Untrack(s, s.token)
}

// getSessionUUID extracts the session ID from the `lsid` field of spec.
// If the `lsid` field does not exist (or spec is nil), it returns an empty uuid.
func getSessionUUID(spec *types.Document) (uuid.UUID, error) {
	if spec == nil {
		return uuid.Nil, nil
	}

	v, _ := spec.Get("lsid")
	if v == nil {
		return uuid.Nil, nil
	}

	lsid, ok := v.(*types.Document)
	if !ok {
		msg := fmt.Sprintf("BSON field 'OperationSessionInfo.lsid' is the wrong type '%T', expected type 'object'", v)
		return uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, "lsid")
	}

	idV, _ := lsid.Get("id")
	if idV == nil {
		msg := "BSON field 'OperationSessionInfo.lsid.id' is missing but a required field"
		return uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrMissingField, msg, "lsid")
	}

	binaryID, ok := idV.(types.Binary)
	if !ok {
		msg := fmt.Sprintf("BSON field 'OperationSessionInfo.lsid.id' is the wrong type '%T', expected type 'binData'", idV)
		return uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, "lsid")
	}

	if binaryID.Subtype != types.BinaryUUID {
		msg := fmt.Sprintf(
			"BSON field 'OperationSessionInfo.lsid.id' is the wrong binData type '%s', expected type 'UUID'",
			binaryID.Subtype.String(),
		)

		return uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrTypeMismatch, msg, "lsid")
	}

	sessionID, err := uuid.FromBytes(binaryID.B)
	if err != nil {
		msg := "uuid must be a 16-byte binary field with UUID (4) subtype"
		return uuid.Nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrBadValue, msg, "lsid")
	}

	return sessionID, nil
}

// getUserID gets the username from conninfo and returns the hash of <username>@admin.
// If there is no logged-in user, it returns a hash of an empty string.
func getUserID(ctx context.Context) UserID {
	username := conninfo.Get(ctx).Username()

	return GetUIDFromUsername("admin", username)
}

// GetUIDFromUsername returns the hash of <username>@<database>.
// If the username is empty, it returns a hash of an empty string.
func GetUIDFromUsername(db, username string) UserID {
	var userAtDB string

	if username != "" {
		userAtDB = username + "@" + db
	}

	return sha256.Sum256([]byte(userAtDB))
}
