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

package session

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/logging"
	"github.com/FerretDB/FerretDB/internal/util/resource"
)

// Parts of Prometheus metric names.
const (
	namespace = "ferretdb"
	subsystem = "sessions"
)

// Registry stores logical sessions.
//
// Cursor-association tracking from FerretDB v2 is intentionally omitted; the
// registry manages only the session lifecycle.
//
//nolint:vet // for readability
type Registry struct {
	rw sync.RWMutex

	// Note that different users can have sessions with the same UUID value,
	// so a UUID is not really unique here.
	sessions map[UserID]map[uuid.UUID]*sessionInfo // userID -> sessionID -> sessionInfo, empty UUID for no lsid

	timeout time.Duration

	l     *slog.Logger
	token *resource.Token

	created  *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewRegistry returns a new registry.
func NewRegistry(timeout time.Duration, l *slog.Logger) *Registry {
	r := &Registry{
		sessions: map[UserID]map[uuid.UUID]*sessionInfo{},
		timeout:  timeout,
		l:        logging.WithName(l, "session"),
		token:    resource.NewToken(),

		created: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "created_total",
				Help:      "Total number of sessions created.",
			},
			[]string{"kind"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "duration_seconds",
				Help:      "Session lifetime in seconds.",
				Buckets: []float64{
					1,
					5,
					10,
					30,
					(1 * time.Minute).Seconds(),
					(5 * time.Minute).Seconds(),
					(10 * time.Minute).Seconds(),
					(30 * time.Minute).Seconds(),
					(1 * time.Hour).Seconds(),
					(4 * time.Hour).Seconds(),
				},
			},
			[]string{"reason"},
		),
	}

	resource.Track(r, r.token)

	return r
}

// NewSession creates a new session with a freshly generated ID and adds it to the registry.
func (r *Registry) NewSession(ctx context.Context) uuid.UUID {
	r.rw.Lock()
	defer r.rw.Unlock()

	sessionID := uuid.New()

	userID := getUserID(ctx)
	s := newSessionInfo()

	if _, ok := r.sessions[userID]; !ok {
		r.sessions[userID] = map[uuid.UUID]*sessionInfo{}
	}

	r.sessions[userID][sessionID] = s
	r.l.DebugContext(ctx,
		"New session created explicitly",
		slog.String("user_id", userID.String()), slog.String("session_id", sessionID.String()),
	)

	r.created.WithLabelValues("explicit").Inc()

	return sessionID
}

// EndSessions marks the given sessions of the current user as ended.
// If a session does not exist, it does nothing.
func (r *Registry) EndSessions(ctx context.Context, sessionIDs []uuid.UUID) {
	r.rw.Lock()
	defer r.rw.Unlock()

	userID := getUserID(ctx)

	for _, sessionID := range sessionIDs {
		if _, ok := r.sessions[userID][sessionID]; !ok {
			continue
		}

		r.sessions[userID][sessionID].ended = true
	}
}

// CreateOrUpdateByLSID fetches the `lsid` field from spec and updates the last
// used time of that session.
// If the `lsid` is not a valid UUID, it returns an error.
// If the session does not exist, a new session is created implicitly.
// If the `lsid` field is not present, a session is created with an empty session ID.
//
// It returns the user ID and the session ID.
func (r *Registry) CreateOrUpdateByLSID(ctx context.Context, spec *types.Document) (UserID, uuid.UUID, error) {
	userID := getUserID(ctx)

	sessionID, err := getSessionUUID(spec)
	if err != nil {
		return UserID{}, uuid.Nil, err
	}

	r.rw.Lock()
	defer r.rw.Unlock()

	r.createOrUpdateSessions(ctx, userID, []uuid.UUID{sessionID})

	return userID, sessionID, nil
}

// CreateOrUpdateSessions updates the last used time of the sessions of the current user.
// If a session does not exist, a new session is created implicitly.
func (r *Registry) CreateOrUpdateSessions(ctx context.Context, sessionIDs []uuid.UUID) {
	userID := getUserID(ctx)

	r.rw.Lock()
	defer r.rw.Unlock()

	r.createOrUpdateSessions(ctx, userID, sessionIDs)
}

// createOrUpdateSessions updates the last used time of the sessions.
// If a session does not exist, a new session is created implicitly.
//
// It does not hold RWMutex, hence the caller should hold RWMutex.
func (r *Registry) createOrUpdateSessions(ctx context.Context, userID UserID, sessionIDs []uuid.UUID) {
	for _, sessionID := range sessionIDs {
		if _, ok := r.sessions[userID][sessionID]; ok {
			r.sessions[userID][sessionID].lastUsed = time.Now()

			r.l.DebugContext(
				ctx,
				"Session refreshed",
				slog.String("user_id", userID.String()), slog.String("session_id", sessionID.String()),
			)

			continue
		}

		if _, ok := r.sessions[userID]; !ok {
			r.sessions[userID] = map[uuid.UUID]*sessionInfo{}
		}

		r.sessions[userID][sessionID] = newSessionInfo()

		r.l.DebugContext(
			ctx,
			"Session created implicitly",
			slog.String("user_id", userID.String()), slog.String("session_id", sessionID.String()),
		)

		r.created.WithLabelValues("implicit").Inc()
	}
}

// DeleteAllSessions removes all sessions of all users.
func (r *Registry) DeleteAllSessions() {
	r.rw.Lock()
	defer r.rw.Unlock()

	for _, userID := range slices.Collect(maps.Keys(r.sessions)) {
		sessionIDs := slices.Collect(maps.Keys(r.sessions[userID]))
		r.deleteSessions(userID, sessionIDs, "killed")
	}

	r.sessions = map[UserID]map[uuid.UUID]*sessionInfo{}
}

// DeleteSessionsByUserIDs removes sessions of the specified user IDs.
// If a user ID does not exist, it does nothing.
func (r *Registry) DeleteSessionsByUserIDs(userIDs []UserID) {
	r.rw.Lock()
	defer r.rw.Unlock()

	for _, userID := range userIDs {
		sessionIDs := slices.Collect(maps.Keys(r.sessions[userID]))
		r.deleteSessions(userID, sessionIDs, "killed")
	}
}

// DeleteSessionsByIDs removes the given sessions of the given user.
// If a session does not exist, it does nothing.
func (r *Registry) DeleteSessionsByIDs(userID UserID, sessionIDs []uuid.UUID) {
	r.rw.Lock()
	defer r.rw.Unlock()

	r.deleteSessions(userID, sessionIDs, "killed")
}

// deleteSessions removes the given sessions of the given user.
// The `reason` parameter is used for the label of the Prometheus metrics.
//
// It does not hold RWMutex, hence the caller should hold RWMutex.
func (r *Registry) deleteSessions(userID UserID, sessionIDs []uuid.UUID, reason string) {
	for _, sessionID := range sessionIDs {
		info := r.sessions[userID][sessionID]
		if info == nil {
			continue
		}

		delete(r.sessions[userID], sessionID)

		info.close()

		r.duration.WithLabelValues(reason).Observe(time.Since(info.created).Seconds())
	}

	if len(r.sessions[userID]) == 0 {
		delete(r.sessions, userID)
	}
}

// DeleteExpired removes ended and expired sessions from the registry.
func (r *Registry) DeleteExpired() {
	r.rw.Lock()
	defer r.rw.Unlock()

	toEnd := map[UserID][]uuid.UUID{}
	toExpire := map[UserID][]uuid.UUID{}

	for userID, sessions := range r.sessions {
		for sessionID, s := range sessions {
			if s.ended {
				toEnd[userID] = append(toEnd[userID], sessionID)

				continue
			}

			if time.Since(s.lastUsed) > r.timeout {
				toExpire[userID] = append(toExpire[userID], sessionID)
			}
		}
	}

	for userID, sessionIDs := range toEnd {
		r.deleteSessions(userID, sessionIDs, "ended")
	}

	for userID, sessionIDs := range toExpire {
		r.deleteSessions(userID, sessionIDs, "expired")
	}
}

// Stop stops the registry and deletes all sessions.
func (r *Registry) Stop() {
	r.DeleteAllSessions()
	r.sessions = nil

	resource.Untrack(r, r.token)
}

// Describe implements [prometheus.Collector].
func (r *Registry) Describe(ch chan<- *prometheus.Desc) {
	r.created.Describe(ch)
	r.duration.Describe(ch)
}

// Collect implements [prometheus.Collector].
func (r *Registry) Collect(ch chan<- prometheus.Metric) {
	r.created.Collect(ch)
	r.duration.Collect(ch)
}

// check interfaces
var (
	_ prometheus.Collector = (*Registry)(nil)
)
