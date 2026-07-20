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

package fsql

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// slowQueryThreshold is the duration at or above which a SQL statement is logged
// at WARN level (in addition to the per-query DEBUG logging), so performance
// problems are visible without turning on debug logging.
//
// Under a heavy poll-and-diff workload, FerretDB on SQLite could sit above 100%
// CPU with the application very slow, and normal logs showed nothing. Surfacing slow
// statements at WARN turns that silent pathology into an actionable log line
// naming the exact statement and how long it took.
//
// The default is 1s. It can be tuned with the FERRETDB_SLOW_QUERY_THRESHOLD
// environment variable (a Go duration such as "500ms" or "2s"); a value of 0 or
// less disables slow-query logging entirely. An unparseable value falls back to
// the default.
var slowQueryThreshold = slowQueryThresholdFromEnv(os.Getenv("FERRETDB_SLOW_QUERY_THRESHOLD"))

// slowQueryThresholdFromEnv parses the FERRETDB_SLOW_QUERY_THRESHOLD value,
// returning the 1s default for an empty or unparseable value. A parsed
// non-positive value is returned as-is and disables slow-query logging.
func slowQueryThresholdFromEnv(v string) time.Duration {
	const def = time.Second

	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}

	return d
}

// logSlow logs a WARN line for a SQL statement whose duration is at or above
// slowQueryThreshold (when the threshold is positive); it is a no-op otherwise.
// See slowQueryThreshold (#6480).
func logSlow(ctx context.Context, l *slog.Logger, query string, dur time.Duration, extra ...any) {
	if l == nil || slowQueryThreshold <= 0 || dur < slowQueryThreshold {
		return
	}

	fields := append(
		[]any{slog.Duration("time", dur), slog.Duration("threshold", slowQueryThreshold)},
		extra...,
	)
	l.With(fields...).WarnContext(ctx, fmt.Sprintf("slow query: %s", query))
}
