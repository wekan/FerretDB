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
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestThrottle(t *testing.T) {
	t.Run("set enables the throttle with the requested sleep", func(t *testing.T) {
		throttleSet(7, 5000)
		active, d := throttleActive()
		assert.True(t, active)
		assert.Equal(t, 7*time.Millisecond, d)
	})

	t.Run("values are clamped to safe bounds", func(t *testing.T) {
		throttleSet(999999, 999999999)
		_, d := throttleActive()
		assert.Equal(t, time.Duration(throttleMaxSleepMs)*time.Millisecond, d)
		_, sleepMs, until, _, _, _ := throttleStatus()
		assert.Equal(t, int64(throttleMaxSleepMs), sleepMs)
		// deadline is no further out than the max duration (+ a little slack).
		max := time.Now().Add(time.Duration(throttleMaxDurationMs+1000) * time.Millisecond).UnixNano()
		assert.LessOrEqual(t, until, max)
	})

	t.Run("a zero/expired duration means not active", func(t *testing.T) {
		throttleSet(10, 0)
		active, _ := throttleActive()
		assert.False(t, active)
	})

	t.Run("apply does not block when inactive", func(t *testing.T) {
		throttleSet(10, 0)      // inactive
		autoSlowdownMs.Store(0) // no self-regulation either
		start := time.Now()
		throttleApply(context.Background())
		assert.Less(t, time.Since(start), 5*time.Millisecond, "must not sleep when inactive")
	})

	t.Run("throttleCount counts total + per-command, and summary lists the busiest", func(t *testing.T) {
		_, _, _, before, _, _ := throttleStatus()
		throttleCount("find")
		throttleCount("find")
		throttleCount("update")
		_, _, _, after, _, _ := throttleStatus()
		assert.Equal(t, before+3, after, "total command counter increments")
		summary := commandSummary(5)
		assert.Contains(t, summary, "find=", "summary names the busiest command")
		assert.Contains(t, summary, "update=", "summary includes other commands")
		// find (2) must be listed before update (1).
		assert.Less(t, strings.Index(summary, "find="), strings.Index(summary, "update="), "sorted by count desc")
	})

	t.Run("apply respects a cancelled context (no long block)", func(t *testing.T) {
		throttleSet(1000, 5000) // active, 1s per command
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		throttleApply(ctx)
		assert.Less(t, time.Since(start), 200*time.Millisecond, "cancelled context returns promptly")
	})

	// leave the throttle off for any other tests in the package.
	throttleSet(0, 0)
}
