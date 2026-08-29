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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAwaitDataReturnsFilledBatchBeforeWaitingAgain pins the ordering that a
// tailable cursor needs. A notification wakes the query; when that query fills
// the batch, awaitData must return it before entering another notification
// wait. Keeping the old pre-query check made every cursor one write behind.
func TestAwaitDataReturnsFilledBatchBeforeWaitingAgain(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("msg_getmore.go")
	assert.NoError(t, err)

	awaitDataSource := string(source)
	start := strings.Index(awaitDataSource, "func (h *Handler) awaitData(")
	end := strings.Index(awaitDataSource[start:], "func tailableAwaitPollInterval(")
	assert.Greater(t, start, -1)
	assert.Greater(t, end, -1)
	awaitDataSource = awaitDataSource[start : start+end]

	fill := strings.Index(awaitDataSource, "resBatch, err = h.makeNextBatch")
	returnFilled := strings.Index(awaitDataSource, "if resBatch.Len() != 0")
	assert.Greater(t, fill, -1, "awaitData must fill the response batch")
	assert.Greater(t, returnFilled, fill,
		"the filled-batch check must follow makeNextBatch, before the loop waits again")
	assert.Equal(t, 1, strings.Count(awaitDataSource, "if resBatch.Len() != 0"),
		"a stale pre-query batch check must not hide the ordering regression")
}

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
