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

// Package oplog provides decorators that add OpLog functionality to the backend.
package oplog

import (
	"context"
	"log/slog"
	"time"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/logging"
)

// document represents a single OpLog collection record.
type document struct {
	o  *types.Document
	ns string
	op string // i, d, u, c
	o2 *types.Document
}

// marshal returns the BSON document representation with a given timestamp.
func (d *document) marshal(t time.Time) (*types.Document, error) {
	res, err := types.NewDocument(
		"_id", types.NewObjectID(),
		"op", d.op,
		"ns", d.ns,
		"ts", types.NextTimestamp(t),
		"o", d.o,
		"t", int64(1),
		"v", int64(2),
		"wall", t,
	)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	if d.o2 != nil {
		res.Set("o2", d.o2)
	}

	return res, nil
}

// appendCommand records a command that changed a database, in the shape
// MongoDB uses: op "c" on "<db>.$cmd". Dropping a collection is
// {drop: <name>}, dropping a database {dropDatabase: 1}. Without them a client
// replaying the OpLog keeps documents of a collection that no longer exists.
// Best-effort like the document records: a failure is logged, never returned,
// and nothing is recorded while the OpLog does not exist or for the database
// that holds it.
func appendCommand(ctx context.Context, origB backends.Backend, l *slog.Logger, notify func(), dbName string, o *types.Document) {
	if dbName == oplogDatabase {
		return
	}

	db, err := origB.Database(oplogDatabase)
	if err != nil {
		l.ErrorContext(ctx, "Failed to open OpLog database", logging.Error(err))
		return
	}

	cList, err := db.ListCollections(ctx, &backends.ListCollectionsParams{Name: oplogCollection})
	if err != nil || len(cList.Collections) == 0 {
		return
	}

	oplogC, err := db.Collection(oplogCollection)
	if err != nil {
		l.ErrorContext(ctx, "Failed to open OpLog collection", logging.Error(err))
		return
	}

	d := &document{o: o, ns: dbName + ".$cmd", op: "c"}

	doc, err := d.marshal(time.Now())
	if err != nil {
		l.ErrorContext(ctx, "Failed to create document", logging.Error(err))
		return
	}

	if _, err = oplogC.InsertAll(ctx, &backends.InsertAllParams{Docs: []*types.Document{doc}}); err != nil {
		l.ErrorContext(ctx, "Failed to insert documents", logging.Error(err))
		return
	}

	notify()
}
