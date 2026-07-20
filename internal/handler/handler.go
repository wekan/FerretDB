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

// Package handler provides a universal handler implementation for all backends.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/backends/decorators/oplog"
	"github.com/FerretDB/FerretDB/internal/clientconn/conninfo"
	"github.com/FerretDB/FerretDB/internal/clientconn/connmetrics"
	"github.com/FerretDB/FerretDB/internal/clientconn/cursor"
	"github.com/FerretDB/FerretDB/internal/handler/session"
	"github.com/FerretDB/FerretDB/internal/handler/users"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/ctxutil"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/logging"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/password"
	"github.com/FerretDB/FerretDB/internal/util/state"
)

// Parts of Prometheus metric names.
const (
	namespace = "ferretdb"
	subsystem = "handler"

	// Maximum size of a batch for inserting data.
	maxWriteBatchSize = int32(100000)

	// Required by C# driver for `IsMaster` and `hello` op reply, without it `DPANIC` is thrown.
	connectionID = int32(42)

	// Default session timeout in minutes.
	logicalSessionTimeoutMinutes = int32(30)
)

// Handler provides a set of methods to process clients' requests sent over wire protocol.
//
// MsgXXX methods handle OP_MSG commands.
// CmdQuery handles a limited subset of OP_QUERY messages.
//
// Handler instance is shared between all client connections.
type Handler struct {
	*NewOpts

	b backends.Backend

	cursors  *cursor.Registry
	sessions *session.Registry
	commands map[string]*command
	wg       sync.WaitGroup

	cappedCleanupStop             chan struct{}
	cleanupCappedCollectionsDocs  *prometheus.CounterVec
	cleanupCappedCollectionsBytes *prometheus.CounterVec

	ttlCleanupStop            chan struct{}
	cleanupTTLCollectionsDocs *prometheus.CounterVec

	selfRegulateStop chan struct{}
}

// NewOpts represents handler configuration.
//
//nolint:vet // for readability
type NewOpts struct {
	Backend     backends.Backend
	TCPHost     string
	ReplSetName string

	SetupDatabase string
	SetupUsername string
	SetupPassword password.Password
	SetupTimeout  time.Duration

	L             *slog.Logger
	ConnMetrics   *connmetrics.ConnMetrics
	StateProvider *state.Provider

	// test options
	DisablePushdown         bool
	EnableNestedPushdown    bool
	CappedCleanupInterval   time.Duration
	CappedCleanupPercentage uint8
	TTLCleanupInterval      time.Duration
	EnableNewAuth           bool
	BatchSize               int
	MaxBsonObjectSizeBytes  int
}

// New returns a new handler.
func New(opts *NewOpts) (*Handler, error) {
	if opts.CappedCleanupPercentage == 0 {
		opts.CappedCleanupPercentage = 10
	}

	if opts.CappedCleanupPercentage >= 100 || opts.CappedCleanupPercentage <= 0 {
		return nil, fmt.Errorf(
			"percentage of documents to cleanup must be in range (0, 100), but %d given",
			opts.CappedCleanupPercentage,
		)
	}

	if opts.MaxBsonObjectSizeBytes == 0 {
		opts.MaxBsonObjectSizeBytes = types.MaxDocumentLen
	}

	if opts.TTLCleanupInterval == 0 {
		opts.TTLCleanupInterval = 60 * time.Second
	}

	b := oplog.NewBackend(opts.Backend, logging.WithName(opts.L, "oplog"))

	h := &Handler{
		b:        b,
		NewOpts:  opts,
		cursors:  cursor.NewRegistry(logging.WithName(opts.L, "cursors")),
		sessions: session.NewRegistry(time.Duration(logicalSessionTimeoutMinutes)*time.Minute, opts.L),

		cappedCleanupStop: make(chan struct{}),
		cleanupCappedCollectionsDocs: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cleanup_capped_docs",
				Help:      "Total number of documents deleted in capped collections during cleanup.",
			},
			[]string{"db", "collection"},
		),
		cleanupCappedCollectionsBytes: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cleanup_capped_bytes",
				Help:      "Total number of bytes freed in capped collections during cleanup.",
			},
			[]string{"db", "collection"},
		),

		selfRegulateStop: make(chan struct{}),

		ttlCleanupStop: make(chan struct{}),
		cleanupTTLCollectionsDocs: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cleanup_ttl_docs",
				Help:      "Total number of documents deleted in TTL collections during cleanup.",
			},
			[]string{"db", "collection"},
		),
	}

	if err := h.setup(); err != nil {
		h.Close()
		return nil, lazyerrors.Error(err)
	}

	h.ensureOplog()

	h.initCommands()

	h.wg.Add(1)

	go func() {
		defer h.wg.Done()

		h.runCappedCleanup()
	}()

	h.wg.Add(1)

	go func() {
		defer h.wg.Done()

		h.runTTLCleanup()
	}()

	h.wg.Add(1)

	go func() {
		defer h.wg.Done()

		h.runSelfRegulation()
	}()

	return h, nil
}

// Setup creates initial database and user if needed.
func (h *Handler) setup() error {
	if h.SetupDatabase == "" {
		return nil
	}

	ctx, span := otel.Tracer("").Start(context.TODO(), "HandlerSetup")
	defer span.End()

	if h.SetupTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.SetupTimeout)
		defer cancel()
	}

	info := conninfo.New()
	info.SetBypassBackendAuth()

	ctx = conninfo.Ctx(ctx, info)

	l := logging.WithName(h.L, "setup")

	var retry int64

	for ctx.Err() == nil {
		_, err := h.b.Status(ctx, nil)
		if err == nil {
			break
		}

		l.DebugContext(ctx, "Status failed", logging.Error(err))

		retry++
		ctxutil.SleepWithJitter(ctx, time.Second, retry)
	}

	res, err := h.b.ListDatabases(ctx, &backends.ListDatabasesParams{Name: h.SetupDatabase})
	if err != nil {
		return lazyerrors.Error(err)
	}

	if len(res.Databases) > 0 {
		l.DebugContext(ctx, "Database already exists")
		return nil
	}

	l.InfoContext(
		ctx,
		"Setting up database and user",
		slog.String("database", h.SetupDatabase),
		slog.String("username", h.SetupUsername),
	)

	db, err := h.b.Database(h.SetupDatabase)
	if err != nil {
		return lazyerrors.Error(err)
	}

	// that's the only way to create a database
	if err = db.CreateCollection(ctx, &backends.CreateCollectionParams{Name: "setup"}); err != nil {
		return lazyerrors.Error(err)
	}

	if err = db.DropCollection(ctx, &backends.DropCollectionParams{Name: "setup"}); err != nil {
		return lazyerrors.Error(err)
	}

	err = users.CreateUser(ctx, h.b, &users.CreateUserParams{
		Database: h.SetupDatabase,
		Username: h.SetupUsername,
		Password: h.SetupPassword,
	})
	if err != nil {
		return lazyerrors.Error(err)
	}

	return nil
}

// oplogCappedSizeBytes is the size of the auto-created capped `local.oplog.rs`.
// 128 MiB holds a large sliding window of recent mutations for tailing without
// growing unbounded; the capped-collection cleanup trims older entries.
const oplogCappedSizeBytes = int64(128 * 1024 * 1024)

// ensureOplog creates the capped `local.oplog.rs` collection when a replica-set
// name is configured (FERRETDB_REPL_SET_NAME) and it does not already exist.
//
// FerretDB v1's oplog decorator only records mutations once this collection is
// present, and Meteor can only tail it once it exists — previously an operator
// had to create it by hand, which is why clients defaulted to poll-and-diff.
// Auto-creating it makes "run with an oplog" the out-of-the-box behaviour
// whenever a replica-set name is set, so Meteor uses OpLog tailing instead of
// polling. Best-effort: any failure is logged but never blocks startup (the server
// still runs; clients fall back to polling).
func (h *Handler) ensureOplog() {
	if h.ReplSetName == "" {
		return
	}

	ctx, span := otel.Tracer("").Start(context.TODO(), "ensureOplog")
	defer span.End()

	info := conninfo.New()
	info.SetBypassBackendAuth()
	ctx = conninfo.Ctx(ctx, info)

	l := logging.WithName(h.L, "oplog")

	db, err := h.b.Database("local")
	if err != nil {
		l.WarnContext(ctx, "Failed to open local database for oplog", logging.Error(err))
		return
	}

	cList, err := db.ListCollections(ctx, &backends.ListCollectionsParams{Name: "oplog.rs"})
	if err != nil {
		l.WarnContext(ctx, "Failed to list oplog collection", logging.Error(err))
		return
	}

	if len(cList.Collections) > 0 {
		return
	}

	err = db.CreateCollection(ctx, &backends.CreateCollectionParams{
		Name:       "oplog.rs",
		CappedSize: oplogCappedSizeBytes,
	})
	if err != nil && !backends.ErrorCodeIs(err, backends.ErrorCodeCollectionAlreadyExists) {
		l.WarnContext(ctx, "Failed to create capped oplog collection", logging.Error(err))
		return
	}

	l.InfoContext(ctx, "Enabled OpLog tailing (created capped local.oplog.rs)", slog.String("replSet", h.ReplSetName))
}

// runCappedCleanup calls capped collections cleanup function according to the given interval.
func (h *Handler) runCappedCleanup() {
	if h.CappedCleanupInterval <= 0 {
		h.L.Info("Capped collections cleanup disabled.")
		return
	}

	h.L.Info("Capped collections cleanup enabled.", slog.Duration("interval", h.CappedCleanupInterval))

	ticker := time.NewTicker(h.CappedCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := h.cleanupAllCappedCollections(context.Background()); err != nil {
				h.L.Error("Failed to cleanup capped collections.", logging.Error(err))
			}

		case <-h.cappedCleanupStop:
			h.L.Info("Capped collections cleanup stopped.")
			return
		}
	}
}

// runTTLCleanup deletes expired documents from TTL indexes according to the given interval.
func (h *Handler) runTTLCleanup() {
	if h.TTLCleanupInterval <= 0 {
		h.L.Info("TTL indexes cleanup disabled.")
		return
	}

	h.L.Info("TTL indexes cleanup enabled.", slog.Duration("interval", h.TTLCleanupInterval))

	ticker := time.NewTicker(h.TTLCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := h.cleanupAllTTLCollections(context.Background()); err != nil {
				h.L.Error("Failed to cleanup TTL collections.", logging.Error(err))
			}

		case <-h.ttlCleanupStop:
			h.L.Info("TTL indexes cleanup stopped.")
			return
		}
	}
}

// cleanupAllTTLCollections removes expired documents from every TTL index in every collection.
func (h *Handler) cleanupAllTTLCollections(ctx context.Context) error {
	ctx, span := otel.Tracer("").Start(ctx, "HandlerCleanupAllTTLCollections")

	start := time.Now()
	defer func() {
		span.End()
		h.L.DebugContext(ctx, "cleanupAllTTLCollections: finished", slog.Duration("duration", time.Since(start)))
	}()

	connInfo := conninfo.New()
	connInfo.SetBypassBackendAuth()
	ctx = conninfo.Ctx(ctx, connInfo)

	now := time.Now()

	dbList, err := h.b.ListDatabases(ctx, nil)
	if err != nil {
		return lazyerrors.Error(err)
	}

	for _, dbInfo := range dbList.Databases {
		db, err := h.b.Database(dbInfo.Name)
		if err != nil {
			return lazyerrors.Error(err)
		}

		cList, err := db.ListCollections(ctx, nil)
		if err != nil {
			return lazyerrors.Error(err)
		}

		for _, cInfo := range cList.Collections {
			coll, err := db.Collection(cInfo.Name)
			if err != nil {
				return lazyerrors.Error(err)
			}

			indexes, err := coll.ListIndexes(ctx, nil)
			if err != nil {
				if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionDoesNotExist) ||
					backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseDoesNotExist) {
					continue
				}

				return lazyerrors.Error(err)
			}

			for _, index := range indexes.Indexes {
				// TTL indexes are single-field only; skip anything else defensively.
				if index.ExpireAfterSeconds == nil || len(index.Key) != 1 {
					continue
				}

				cutoff := now.Add(-time.Duration(*index.ExpireAfterSeconds) * time.Second)

				deleted, err := h.cleanupTTLCollection(ctx, coll, index.Key[0].Field, cutoff)
				if err != nil {
					if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionDoesNotExist) ||
						backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseDoesNotExist) {
						continue
					}

					return lazyerrors.Error(err)
				}

				if deleted > 0 {
					h.L.InfoContext(
						ctx,
						"TTL collection cleaned up",
						slog.String("db", dbInfo.Name),
						slog.String("collection", cInfo.Name),
						slog.String("index", index.Name),
						slog.Int("deleted", int(deleted)),
					)

					h.cleanupTTLCollectionsDocs.WithLabelValues(dbInfo.Name, cInfo.Name).Add(float64(deleted))
				}
			}
		}
	}

	return nil
}

// cleanupTTLCollection deletes documents whose date field is older than or equal to the cutoff.
//
// Documents where the field is missing or is not a date are skipped, matching MongoDB behavior.
func (h *Handler) cleanupTTLCollection(ctx context.Context, coll backends.Collection, field string, cutoff time.Time) (int32, error) { //nolint:lll // for readability
	path, err := types.NewPathFromString(field)
	if err != nil {
		return 0, lazyerrors.Error(err)
	}

	res, err := coll.Query(ctx, &backends.QueryParams{})
	if err != nil {
		return 0, lazyerrors.Error(err)
	}

	defer res.Iter.Close()

	var ids []any

	for {
		_, doc, err := res.Iter.Next()
		if err != nil {
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			return 0, lazyerrors.Error(err)
		}

		v, err := doc.GetByPath(path)
		if err != nil {
			// field is missing
			continue
		}

		t, ok := v.(time.Time)
		if !ok {
			// non-date values are ignored by the reaper, per MongoDB
			continue
		}

		// delete where field <= cutoff
		if !t.After(cutoff) {
			ids = append(ids, must.NotFail(doc.Get("_id")))
		}
	}

	if len(ids) == 0 {
		return 0, nil
	}

	deleteRes, err := coll.DeleteAll(ctx, &backends.DeleteAllParams{IDs: ids})
	if err != nil {
		return 0, lazyerrors.Error(err)
	}

	return deleteRes.Deleted, nil
}

// Close gracefully shutdowns handler.
// It should be called after listener closes all client connections and stops listening.
func (h *Handler) Close() {
	h.cursors.Close()
	h.sessions.Stop()
	close(h.cappedCleanupStop)
	close(h.ttlCleanupStop)
	close(h.selfRegulateStop)
	h.wg.Wait()
}

// Describe implements [prometheus.Collector].
func (h *Handler) Describe(ch chan<- *prometheus.Desc) {
	h.b.Describe(ch)
	h.cursors.Describe(ch)
	h.sessions.Describe(ch)
	h.cleanupCappedCollectionsDocs.Describe(ch)
	h.cleanupCappedCollectionsBytes.Describe(ch)
	h.cleanupTTLCollectionsDocs.Describe(ch)
}

// Collect implements [prometheus.Collector].
func (h *Handler) Collect(ch chan<- prometheus.Metric) {
	h.b.Collect(ch)
	h.cursors.Collect(ch)
	h.sessions.Collect(ch)
	h.cleanupCappedCollectionsDocs.Collect(ch)
	h.cleanupCappedCollectionsBytes.Collect(ch)
	h.cleanupTTLCollectionsDocs.Collect(ch)
}

// cleanupAllCappedCollections drops the given percent of documents from all capped collections.
func (h *Handler) cleanupAllCappedCollections(ctx context.Context) error {
	ctx, span := otel.Tracer("").Start(ctx, "HandlerCleanupAllCappedCollections")
	h.L.DebugContext(ctx, "cleanupAllCappedCollections: started", slog.Int("percentage", int(h.CappedCleanupPercentage)))

	start := time.Now()
	defer func() {
		span.End()
		h.L.DebugContext(ctx, "cleanupAllCappedCollections: finished", slog.Duration("duration", time.Since(start)))
	}()

	connInfo := conninfo.New()
	connInfo.SetBypassBackendAuth()
	ctx = conninfo.Ctx(ctx, connInfo)

	dbList, err := h.b.ListDatabases(ctx, nil)
	if err != nil {
		return lazyerrors.Error(err)
	}

	for _, dbInfo := range dbList.Databases {
		db, err := h.b.Database(dbInfo.Name)
		if err != nil {
			return lazyerrors.Error(err)
		}

		cList, err := db.ListCollections(ctx, nil)
		if err != nil {
			return lazyerrors.Error(err)
		}

		for _, cInfo := range cList.Collections {
			if !cInfo.Capped() {
				continue
			}

			deleted, bytesFreed, err := h.cleanupCappedCollection(ctx, db, &cInfo, false)
			if err != nil {
				if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionDoesNotExist) ||
					backends.ErrorCodeIs(err, backends.ErrorCodeDatabaseDoesNotExist) {
					continue
				}

				return lazyerrors.Error(err)
			}

			if deleted > 0 || bytesFreed > 0 {
				h.L.InfoContext(
					ctx,
					"Capped collection cleaned up",
					slog.String("db", dbInfo.Name),
					slog.String("collection", cInfo.Name),
					slog.Int("deleted", int(deleted)),
					slog.Int64("bytes_freed", bytesFreed),
				)
			}

			h.cleanupCappedCollectionsDocs.WithLabelValues(dbInfo.Name, cInfo.Name).Add(float64(deleted))
			h.cleanupCappedCollectionsBytes.WithLabelValues(dbInfo.Name, cInfo.Name).Add(float64(bytesFreed))
		}
	}

	return nil
}

// cleanupCappedCollection drops a percent of documents from the given capped collection and compacts it.
func (h *Handler) cleanupCappedCollection(ctx context.Context, db backends.Database, cInfo *backends.CollectionInfo, force bool) (int32, int64, error) { //nolint:lll // for readability
	must.BeTrue(cInfo.Capped())

	var docsDeleted int32
	var bytesFreed int64
	var statsBefore, statsAfter *backends.CollectionStatsResult

	coll, err := db.Collection(cInfo.Name)
	if err != nil {
		return 0, 0, lazyerrors.Error(err)
	}

	statsBefore, err = coll.Stats(ctx, &backends.CollectionStatsParams{Refresh: true})
	if err != nil {
		return 0, 0, lazyerrors.Error(err)
	}

	h.L.DebugContext(
		ctx,
		"cleanupCappedCollection: stats before",
		slog.Int64("size_total", statsBefore.SizeTotal),
		slog.Int64("size_collection", statsBefore.SizeCollection),
		slog.Int64("count_documents", statsBefore.CountDocuments),
	)

	// In order to be more precise w.r.t number of documents getting dropped and to avoid
	// deleting too many documents unnecessarily,
	//
	// - First, drop the surplus documents, if document count exceeds capped configuration.
	// - Collect stats again.
	// - If collection size still exceeds the capped size, then drop the documents based on
	//   CappedCleanupPercentage.

	if count := getDocCleanupCount(cInfo, statsBefore); count > 0 {
		err = deleteFirstNDocuments(ctx, coll, count)
		if err != nil {
			return 0, 0, lazyerrors.Error(err)
		}

		statsAfter, err = coll.Stats(ctx, &backends.CollectionStatsParams{Refresh: true})
		if err != nil {
			return 0, 0, lazyerrors.Error(err)
		}

		h.L.DebugContext(
			ctx,
			"cleanupCappedCollection: stats after document count reduction",
			slog.Int64("size_total", statsAfter.SizeTotal),
			slog.Int64("size_collection", statsAfter.SizeCollection),
			slog.Int64("count_documents", statsAfter.CountDocuments),
		)

		docsDeleted += int32(count)
		bytesFreed += (statsBefore.SizeTotal - statsAfter.SizeTotal)

		statsBefore = statsAfter
	}

	if count := getSizeCleanupCount(cInfo, statsBefore, h.CappedCleanupPercentage); count > 0 {
		err = deleteFirstNDocuments(ctx, coll, count)
		if err != nil {
			return 0, 0, lazyerrors.Error(err)
		}

		docsDeleted += int32(count)
	}

	if _, err = coll.Compact(ctx, &backends.CompactParams{Full: force}); err != nil {
		return 0, 0, lazyerrors.Error(err)
	}

	statsAfter, err = coll.Stats(ctx, &backends.CollectionStatsParams{Refresh: true})
	if err != nil {
		return 0, 0, lazyerrors.Error(err)
	}

	h.L.DebugContext(
		ctx,
		"cleanupCappedCollection: stats after compact",
		slog.Int64("size_total", statsAfter.SizeTotal),
		slog.Int64("size_collection", statsAfter.SizeCollection),
		slog.Int64("count_documents", statsAfter.CountDocuments),
	)

	bytesFreed += (statsBefore.SizeTotal - statsAfter.SizeTotal)

	// There's a possibility that the size of a collection might be greater at the
	// end of a compact operation if the collection is being actively written to at
	// the time of compaction.
	if bytesFreed < 0 {
		bytesFreed = 0
	}

	return docsDeleted, bytesFreed, nil
}

// getDocCleanupCount returns the number of documents to be deleted during capped collection cleanup
// based on document count of the collection and capped configuration.
func getDocCleanupCount(cInfo *backends.CollectionInfo, cStats *backends.CollectionStatsResult) int64 {
	if cInfo.CappedDocuments == 0 || cInfo.CappedDocuments >= cStats.CountDocuments {
		return 0
	}

	return (cStats.CountDocuments - cInfo.CappedDocuments)
}

// getSizeCleanupCount returns the number of documents to be deleted during capped collection cleanup
// based collection size, capped configuration and cleanup percentage.
func getSizeCleanupCount(cInfo *backends.CollectionInfo, cStats *backends.CollectionStatsResult, cleanupPercent uint8) int64 {
	if cInfo.CappedSize >= cStats.SizeCollection {
		return 0
	}

	return int64(float64(cStats.CountDocuments) * float64(cleanupPercent) / 100)
}

// deleteFirstNDocuments drops first n documents (based on order of insertion) from the collection.
func deleteFirstNDocuments(ctx context.Context, coll backends.Collection, n int64) error {
	if n == 0 {
		return nil
	}

	res, err := coll.Query(ctx, &backends.QueryParams{
		Sort:          must.NotFail(types.NewDocument("$natural", int64(1))),
		Limit:         n,
		OnlyRecordIDs: true,
	})
	if err != nil {
		return lazyerrors.Error(err)
	}

	defer res.Iter.Close()

	var recordIDs []int64

	for {
		var doc *types.Document

		_, doc, err = res.Iter.Next()
		if err != nil {
			if errors.Is(err, iterator.ErrIteratorDone) {
				break
			}

			return lazyerrors.Error(err)
		}

		recordIDs = append(recordIDs, doc.RecordID())
	}

	if len(recordIDs) > 0 {
		_, err := coll.DeleteAll(ctx, &backends.DeleteAllParams{RecordIDs: recordIDs})
		if err != nil {
			return lazyerrors.Error(err)
		}
	}

	return nil
}
