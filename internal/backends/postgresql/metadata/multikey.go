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

package metadata

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// Multikey indexes.
//
// A btree index on {"members.userId": 1} indexes _jsonb->'members'->'userId',
// which is NULL for every document whose members is an array: a membership test
// ({members: {$elemMatch: {userId: X}}}) read every row. For every array whose
// FIELD leads an index, the collection also has a GIN index on the array itself
// (jsonb_path_ops), which answers containment - `members @> '[{"userId": X}]'`
// is exactly "an element whose userId is X" - from the index. The query uses it
// only to narrow (query_multikey.go); the Go filter still decides.

// plainName is a field name the index DDL may contain as it is.
var plainName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// MultikeyPath returns ARRAY and FIELD of an index whose leading key is the
// two-segment dotted path ARRAY.FIELD.
func MultikeyPath(index IndexInfo) (string, string, bool) {
	if len(index.Key) == 0 {
		return "", "", false
	}

	array, field, ok := strings.Cut(index.Key[0].Field, ".")
	if !ok || !plainName.MatchString(array) || !plainName.MatchString(field) {
		return "", "", false
	}

	return array, field, true
}

// MultikeyArrays returns, for each array, the fields whose ARRAY.FIELD leads an index.
func MultikeyArrays(indexes []IndexInfo) map[string][]string {
	res := map[string][]string{}

	for _, index := range indexes {
		if array, field, ok := MultikeyPath(index); ok && !slices.Contains(res[array], field) {
			res[array] = append(res[array], field)
		}
	}

	return res
}

// MultikeyIndexName returns the name of the GIN index of an array of a table.
func MultikeyIndexName(table, array string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(table + "." + array))

	if l := maxIndexNameLength - 13; len(table) > l {
		table = table[:l]
	}

	return fmt.Sprintf("%s_mk_%08x", table, h.Sum32())
}

// ensureMultikey creates the GIN indexes the indexes need. It is a no-op for
// those that exist.
func ensureMultikey(ctx context.Context, p *pgxpool.Pool, dbName, table string, indexes []IndexInfo) error {
	for array := range MultikeyArrays(indexes) {
		q := fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s ON %s USING gin ((%s->%s) jsonb_path_ops)`,
			pgx.Identifier{MultikeyIndexName(table, array)}.Sanitize(),
			pgx.Identifier{dbName, table}.Sanitize(),
			DefaultColumn, quoteString(array),
		)

		if _, err := p.Exec(ctx, q); err != nil {
			return lazyerrors.Error(err)
		}
	}

	return nil
}

// dropUnusedMultikey drops the GIN indexes of arrays no remaining index leads with.
func dropUnusedMultikey(ctx context.Context, p *pgxpool.Pool, dbName, table string, dropped, remaining []IndexInfo) error {
	keep := MultikeyArrays(remaining)

	for array := range MultikeyArrays(dropped) {
		if _, ok := keep[array]; ok {
			continue
		}

		q := fmt.Sprintf(`DROP INDEX IF EXISTS %s`, pgx.Identifier{dbName, MultikeyIndexName(table, array)}.Sanitize())
		if _, err := p.Exec(ctx, q); err != nil {
			return lazyerrors.Error(err)
		}
	}

	return nil
}
