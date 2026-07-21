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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTailableAwaitPollInterval covers the awaitData poll interval used when a
// tailable+awaitData cursor has no new data. The default must be much calmer than the
// old hard-coded 10ms (which made an idle, continuously-tailed cursor re-run its query
// ~100 times/second and pin CPU), and it must never fall back to a busy-loop (<= 0).
func TestTailableAwaitPollInterval(t *testing.T) {
	t.Run("default is calm when unset", func(t *testing.T) {
		t.Setenv("FERRETDB_TAILABLE_AWAIT_POLL_MS", "")

		got := tailableAwaitPollInterval()
		assert.Equal(t, 500*time.Millisecond, got)

		// Regression guard: the default must not be the old busy-loop interval, and must
		// be far enough above it that an idle tail cannot peg CPU.
		assert.GreaterOrEqual(t, got, 100*time.Millisecond,
			"default poll interval must be much larger than the old 10ms")
	})

	t.Run("honours a custom value", func(t *testing.T) {
		t.Setenv("FERRETDB_TAILABLE_AWAIT_POLL_MS", "250")
		assert.Equal(t, 250*time.Millisecond, tailableAwaitPollInterval())
	})

	// Negative / degenerate inputs must fall back to the default, never to a value that
	// re-creates the busy-loop (0) or a nonsensical negative sleep.
	for name, val := range map[string]string{
		"zero":        "0",
		"negative":    "-5",
		"nonNumeric":  "soon",
		"emptyString": "",
	} {
		t.Run("falls back to default for "+name, func(t *testing.T) {
			t.Setenv("FERRETDB_TAILABLE_AWAIT_POLL_MS", val)

			got := tailableAwaitPollInterval()
			assert.Equal(t, 500*time.Millisecond, got)
			assert.Greater(t, got, time.Duration(0), "must never be a zero/negative (busy-loop) interval")
		})
	}
}
