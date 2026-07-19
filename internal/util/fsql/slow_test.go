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
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowRecorder is a minimal slog.Handler that records emitted records.
type slowRecorder struct {
	records []slog.Record
}

func (h *slowRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *slowRecorder) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *slowRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *slowRecorder) WithGroup(string) slog.Handler { return h }

func TestSlowQueryThresholdFromEnv(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		in   string
		want time.Duration
	}{
		"Empty":    {"", time.Second},
		"Valid":    {"500ms", 500 * time.Millisecond},
		"Seconds":  {"2s", 2 * time.Second},
		"Invalid":  {"nonsense", time.Second},
		"Zero":     {"0", 0},
		"Negative": {"-1s", -time.Second},
	}
	for name, tc := range testCases {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, slowQueryThresholdFromEnv(tc.in))
		})
	}
}

func TestLogSlow(t *testing.T) {
	// mutates the package-level slowQueryThreshold, so it must not run in parallel.
	orig := slowQueryThreshold
	t.Cleanup(func() { slowQueryThreshold = orig })

	t.Run("Slow", func(t *testing.T) {
		slowQueryThreshold = time.Second

		r := &slowRecorder{}
		logSlow(context.Background(), slog.New(r), "SELECT 1", 2*time.Second)

		require.Len(t, r.records, 1)
		assert.Equal(t, slog.LevelWarn, r.records[0].Level)
		assert.Contains(t, r.records[0].Message, "SELECT 1")
	})

	t.Run("Fast", func(t *testing.T) {
		slowQueryThreshold = time.Second

		r := &slowRecorder{}
		logSlow(context.Background(), slog.New(r), "SELECT 1", 100*time.Millisecond)

		assert.Empty(t, r.records)
	})

	t.Run("Disabled", func(t *testing.T) {
		slowQueryThreshold = 0

		r := &slowRecorder{}
		logSlow(context.Background(), slog.New(r), "SELECT 1", time.Hour)

		assert.Empty(t, r.records)
	})

	t.Run("NilLogger", func(t *testing.T) {
		slowQueryThreshold = time.Second

		// must not panic
		logSlow(context.Background(), nil, "SELECT 1", time.Hour)
	})
}
