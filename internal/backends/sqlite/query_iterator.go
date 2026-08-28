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

package sqlite

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/FerretDB/FerretDB/internal/backends/sqlite/metadata"
	"github.com/FerretDB/FerretDB/internal/handler/sjson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/fsql"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
	"github.com/FerretDB/FerretDB/internal/util/resource"
)

// queryIterator implements iterator.Interface to fetch documents from the database.
type queryIterator struct {
	// the order of fields is weird to make the struct smaller due to alignment

	ctx           context.Context
	rows          *fsql.Rows // protected by m
	token         *resource.Token
	m             sync.Mutex
	onlyRecordIDs bool
	speed         *querySpeed
}

// querySpeed contains only query SHAPE and timing data. In particular it must
// never contain filter values: DEBUGSPEED output is intended to be shareable.
type querySpeed struct {
	logger         *slog.Logger
	database       string
	collection     string
	filterFields   string
	sortFields     string
	index          string
	limit          int64
	queryDuration  time.Duration
	decodeDuration time.Duration
	candidateRows  int64
	logged         bool
}

func shouldLogQuerySpeed(speed *querySpeed) bool {
	return speed != nil && (speed.candidateRows >= 100 || speed.queryDuration >= 10*time.Millisecond || speed.decodeDuration >= 10*time.Millisecond)
}

// newSpeedQueryIterator attaches optional measurements without changing the
// ordinary iterator constructor used throughout tests and non-debug operation.
func newSpeedQueryIterator(iter *queryIterator, speed *querySpeed) *queryIterator {
	iter.speed = speed
	return iter
}

// newQueryIterator returns a new queryIterator for the given *sql.Rows.
//
// Iterator's Close method closes rows.
// They are also closed by the Next method on any error, including context cancellation,
// to make sure that the database connection is released as early as possible.
// In that case, the iterator's Close method should still be called.
//
// Nil rows are possible and return already done iterator.
// It still should be Close'd.
func newQueryIterator(ctx context.Context, rows *fsql.Rows, onlyRecordIDs bool) *queryIterator {
	iter := &queryIterator{
		ctx:           ctx,
		rows:          rows,
		onlyRecordIDs: onlyRecordIDs,
		token:         resource.NewToken(),
	}
	resource.Track(iter, iter.token)

	return iter
}

// Next implements iterator.Interface.
func (iter *queryIterator) Next() (struct{}, *types.Document, error) {
	iter.m.Lock()
	defer iter.m.Unlock()

	var unused struct{}

	// ignore context error, if any, if iterator is already closed
	if iter.rows == nil {
		return unused, nil, iterator.ErrIteratorDone
	}

	if err := context.Cause(iter.ctx); err != nil {
		iter.close()
		return unused, nil, lazyerrors.Error(err)
	}

	if !iter.rows.Next() {
		err := iter.rows.Err()

		iter.close()

		if err == nil {
			err = iterator.ErrIteratorDone
		}

		return unused, nil, lazyerrors.Error(err)
	}

	columns, err := iter.rows.Columns()
	if err != nil {
		iter.close()
		return unused, nil, lazyerrors.Error(err)
	}

	var recordID int64
	var b []byte
	var dest []any

	switch {
	case slices.Equal(columns, []string{metadata.RecordIDColumn, metadata.DefaultColumn}):
		dest = []any{&recordID, &b}
	case slices.Equal(columns, []string{metadata.RecordIDColumn}):
		dest = []any{&recordID}
	case slices.Equal(columns, []string{metadata.DefaultColumn}):
		dest = []any{&b}
	default:
		panic(fmt.Sprintf("cannot scan unknown columns: %v", columns))
	}

	if err = iter.rows.Scan(dest...); err != nil {
		iter.close()
		return unused, nil, lazyerrors.Error(err)
	}
	if iter.speed != nil {
		iter.speed.candidateRows++
	}

	doc := must.NotFail(types.NewDocument())

	if !iter.onlyRecordIDs {
		decodeStarted := time.Now()
		if doc, err = sjson.Unmarshal(b); err != nil {
			iter.close()
			return unused, nil, lazyerrors.Error(err)
		}
		if iter.speed != nil {
			iter.speed.decodeDuration += time.Since(decodeStarted)
		}
	}

	doc.SetRecordID(recordID)

	return unused, doc, nil
}

// Close implements iterator.Interface.
func (iter *queryIterator) Close() {
	iter.m.Lock()
	defer iter.m.Unlock()

	iter.close()
}

// close closes iterator without holding mutex.
//
// This should be called only when the caller already holds the mutex.
func (iter *queryIterator) close() {
	if iter.rows != nil {
		iter.rows.Close()
		iter.rows = nil
	}

	if iter.speed != nil && !iter.speed.logged {
		iter.speed.logged = true
		// Small indexed lookups are deliberately silent. The thresholds retain
		// the query shapes that can explain load without making diagnostics the
		// dominant workload during Meteor polling.
		if shouldLogQuerySpeed(iter.speed) {
			iter.speed.logger.Info("DEBUGSPEED sqlite query",
				slog.String("database", iter.speed.database),
				slog.String("collection", iter.speed.collection),
				slog.String("filter_fields", iter.speed.filterFields),
				slog.String("sort_fields", iter.speed.sortFields),
				slog.String("index", iter.speed.index),
				slog.Int64("limit", iter.speed.limit),
				slog.Int64("candidate_rows", iter.speed.candidateRows),
				slog.Float64("query_ms", float64(iter.speed.queryDuration.Microseconds())/1000),
				slog.Float64("decode_ms", float64(iter.speed.decodeDuration.Microseconds())/1000),
			)
		}
	}

	resource.Untrack(iter, iter.token)
}

// check interfaces
var (
	_ types.DocumentsIterator = (*queryIterator)(nil)
)
