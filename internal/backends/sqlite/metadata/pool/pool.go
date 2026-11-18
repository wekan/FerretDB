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

// Package pool provides access to SQLite databases and their connections.
//
// It should be used only by the metadata package.
package pool

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"

	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/logging"
	"github.com/FerretDB/FerretDB/internal/util/resource"
	"github.com/FerretDB/FerretDB/internal/util/state"
)

// defaultFilenameExtension is the historical SQLite database extension.
const defaultFilenameExtension = ".sqlite"

// Parts of Prometheus metric names.
const (
	namespace = "ferretdb"
	subsystem = "sqlite_pool"
)

// Pool provides access to SQLite databases and their connections.
//
//nolint:vet // for readability
type Pool struct {
	uri        url.URL
	ext        string
	shortNames bool
	l          *slog.Logger
	sp         *state.Provider

	rw  sync.RWMutex
	dbs map[string]*fsql.DB

	token *resource.Token
}

// New creates a pool for SQLite databases in the directory specified by SQLite URI.
//
// All databases are opened on creation.
//
// The returned map is the initial set of existing databases.
// It should not be modified.
func New(u string, l *slog.Logger, sp *state.Provider) (*Pool, map[string]*fsql.DB, error) {
	uri, err := parseURI(u)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse SQLite URI %q: %s", u, err)
	}

	extension := defaultFilenameExtension
	values := uri.Query()
	if values.Get("dbext") == "db" {
		extension = ".db"
	}
	shortNames := values.Get("shortnames") == "1"
	values.Del("dbext")
	values.Del("shortnames")
	if err := checkShortSidecarBuild(shortNames); err != nil {
		return nil, nil, err
	}
	uri.RawQuery = values.Encode()

	p := &Pool{
		uri:        *uri,
		ext:        extension,
		shortNames: shortNames,
		l:          l,
		sp:         sp,
		dbs:        map[string]*fsql.DB{},
		token:      resource.NewToken(),
	}

	resource.Track(p, p.token)

	if p.memory() {
		return p, p.dbs, nil
	}

	matches, err := filepath.Glob(filepath.Join(uri.Path, "*"+p.ext))
	if err != nil {
		p.Close()
		return nil, nil, lazyerrors.Error(err)
	}

	for _, f := range matches {
		name := p.databaseName(f)
		if err := p.checkAlternateFiles(name); err != nil {
			p.Close()
			return nil, nil, err
		}
		uri := p.databaseURI(name)

		p.l.Debug("Opening existing database", slog.String("name", name), slog.String("uri", uri))

		db, err := openDB(name, uri, p.memory(), true, p.shortNames, l, p.sp)
		if err != nil {
			p.Close()
			return nil, nil, lazyerrors.Error(err)
		}

		p.dbs[name] = db
	}

	return p, p.dbs, nil
}

// memory returns true if the pool is for the in-memory database.
func (p *Pool) memory() bool {
	return p.uri.Query().Get("mode") == "memory"
}

// databaseName returns database name for given database file path.
func (p *Pool) databaseName(databaseFile string) string {
	if p.memory() {
		panic("should not be called for in-memory database")
	}

	return strings.TrimSuffix(filepath.Base(databaseFile), p.ext)
}

// databaseURI returns SQLite URI for the given database name,
// even for in-memory database.
func (p *Pool) databaseURI(databaseName string) string {
	dbURI := p.uri
	dbURI.Path = path.Join(dbURI.Path, databaseName+p.ext)
	dbURI.Opaque = dbURI.Path

	return dbURI.String()
}

// databaseFile returns database file path for the given database name,
// or empty string for in-memory database.
func (p *Pool) databaseFile(databaseName string) string {
	if p.memory() {
		return ""
	}

	return filepath.Join(p.uri.Path, databaseName+p.ext)
}

// checkAlternateFiles prevents two extension modes from opening different
// databases for the same name, including recovery files left by either mode.
func (p *Pool) checkAlternateFiles(name string) error {
	if p.memory() {
		return nil
	}
	alternate := ".db"
	if p.ext == alternate {
		alternate = defaultFilenameExtension
	}
	base := filepath.Join(p.uri.Path, name)
	suffixes := []string{alternate, alternate + "-wal", alternate + "-shm", alternate + "-journal"}
	if p.shortNames {
		if !shortDatabaseName(name) {
			return fmt.Errorf("SQLite database name %q is not an 8.3 filename stem", name)
		}
		suffixes = append(suffixes, ".db-wal", ".db-shm", ".db-journal")
	} else {
		suffixes = append(suffixes, ".wal", ".shm", ".nal")
	}
	for _, suffix := range suffixes {
		_, err := os.Lstat(base + suffix)
		if err == nil {
			return fmt.Errorf("conflicting SQLite database path %q", base+suffix)
		}
		if !os.IsNotExist(err) {
			return lazyerrors.Error(err)
		}
	}
	return nil
}

func shortDatabaseName(name string) bool {
	if len(name) == 0 || len(name) > 8 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// Close closes all databases in the pool and frees all resources.
func (p *Pool) Close() {
	p.rw.Lock()
	defer p.rw.Unlock()

	for _, db := range p.dbs {
		_ = db.Close()
	}

	p.dbs = nil

	resource.Untrack(p, p.token)
}

// List returns a sorted list of database names in the pool.
func (p *Pool) List(ctx context.Context) []string {
	p.rw.RLock()
	defer p.rw.RUnlock()

	res := maps.Keys(p.dbs)
	sort.Strings(res)

	return res
}

// GetExisting returns an existing database by valid name, or nil.
func (p *Pool) GetExisting(ctx context.Context, name string) *fsql.DB {
	p.rw.RLock()
	defer p.rw.RUnlock()

	return p.dbs[name]
}

// Discover opens a database file committed by another process. The callback
// must verify registration before the connection is published to the pool.
func (p *Pool) Discover(ctx context.Context, name string, registered func(*fsql.DB) bool) *fsql.DB {
	if p.memory() || name == "" || filepath.Base(name) != name {
		return nil
	}
	info, err := os.Lstat(p.databaseFile(name))
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}

	p.rw.Lock()
	defer p.rw.Unlock()
	if db := p.dbs[name]; db != nil {
		return db
	}

	if err := p.checkAlternateFiles(name); err != nil {
		p.l.DebugContext(ctx, "Skipped conflicting database path", logging.Error(err))
		return nil
	}

	uri, err := url.Parse(p.databaseURI(name))
	if err != nil {
		return nil
	}
	query := uri.Query()
	query.Set("mode", "rw")
	uri.RawQuery = query.Encode()
	db, err := openDB(name, uri.String(), false, false, p.shortNames, p.l, p.sp)
	if err != nil {
		p.l.DebugContext(ctx, "Failed to open external database", logging.Error(err))
		return nil
	}
	if !registered(db) {
		_ = db.Close()
		return nil
	}
	p.dbs[name] = db
	return db
}

// FileNames returns candidate database names that may have appeared since startup.
func (p *Pool) FileNames() []string {
	if p.memory() {
		return nil
	}
	entries, err := os.ReadDir(p.uri.Path)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), p.ext) {
			names = append(names, p.databaseName(entry.Name()))
		}
	}
	return names
}

// GetOrCreate returns an existing database by valid name, or creates a new one.
//
// Returned boolean value indicates whether the database was created.
func (p *Pool) GetOrCreate(ctx context.Context, name string, initialize func(*fsql.DB) error) (*fsql.DB, bool, error) {
	db := p.GetExisting(ctx, name)
	if db != nil {
		return db, false, nil
	}

	p.rw.Lock()
	defer p.rw.Unlock()

	// it might have been created by a concurrent call
	if db := p.dbs[name]; db != nil {
		return db, false, nil
	}

	if err := p.checkAlternateFiles(name); err != nil {
		return nil, false, err
	}
	if file := p.databaseFile(name); file != "" {
		info, err := os.Lstat(file)
		if err != nil && !os.IsNotExist(err) {
			return nil, false, lazyerrors.Error(err)
		}
		if err == nil && !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("SQLite database path %q is not a regular file", file)
		}
	}

	uri := p.databaseURI(name)
	db, err := openDB(name, uri, p.memory(), false, p.shortNames, p.l, p.sp)
	if err != nil {
		return nil, false, lazyerrors.Errorf("%s: %w", uri, err)
	}
	if initialize != nil {
		if err = initialize(db); err != nil {
			_ = db.Close()
			return nil, false, lazyerrors.Error(err)
		}
	}

	p.l.DebugContext(ctx, "Database created", slog.String("name", name), slog.String("uri", uri))

	p.dbs[name] = db

	return db, true, nil
}

// Drop closes and removes a database by valid name.
//
// It does nothing if the database does not exist.
//
// Returned boolean value indicates whether the database was removed.
func (p *Pool) Drop(ctx context.Context, name string) bool {
	p.rw.Lock()
	defer p.rw.Unlock()

	db, ok := p.dbs[name]
	if !ok {
		return false
	}

	if err := db.Close(); err != nil {
		p.l.WarnContext(ctx, "Failed to close database connection", slog.String("name", name), logging.Error(err))
	}

	delete(p.dbs, name)

	if f := p.databaseFile(name); f != "" {
		if err := os.Remove(f); err != nil {
			p.l.WarnContext(
				ctx,
				"Failed to remove database file",
				slog.String("file", f),
				slog.String("name", name),
				logging.Error(err),
			)
		}
	}

	p.l.DebugContext(ctx, "Database dropped", slog.String("name", name))

	return true
}

// Describe implements prometheus.Collector.
func (p *Pool) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(p, ch)
}

// Collect implements prometheus.Collector.
func (p *Pool) Collect(ch chan<- prometheus.Metric) {
	p.rw.RLock()
	defer p.rw.RUnlock()

	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "databases"),
			"The current number of database in the pool.",
			nil, nil,
		),
		prometheus.GaugeValue,
		float64(len(p.dbs)),
	)

	for _, db := range p.dbs {
		db.Collect(ch)
	}
}

// check interfaces
var (
	_ prometheus.Collector = (*Pool)(nil)
)
