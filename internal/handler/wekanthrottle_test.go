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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWekanThrottle(t *testing.T) {
	t.Run("set enables the throttle with the requested sleep", func(t *testing.T) {
		wekanThrottleSet(7, 5000)
		active, d := wekanThrottleActive()
		assert.True(t, active)
		assert.Equal(t, 7*time.Millisecond, d)
	})

	t.Run("values are clamped to safe bounds", func(t *testing.T) {
		wekanThrottleSet(999999, 999999999)
		_, d := wekanThrottleActive()
		assert.Equal(t, time.Duration(wekanThrottleMaxSleepMs)*time.Millisecond, d)
		_, sleepMs, until, _ := wekanThrottleStatus()
		assert.Equal(t, int64(wekanThrottleMaxSleepMs), sleepMs)
		// deadline is no further out than the max duration (+ a little slack).
		max := time.Now().Add(time.Duration(wekanThrottleMaxDurationMs+1000) * time.Millisecond).UnixNano()
		assert.LessOrEqual(t, until, max)
	})

	t.Run("a zero/expired duration means not active", func(t *testing.T) {
		wekanThrottleSet(10, 0)
		active, _ := wekanThrottleActive()
		assert.False(t, active)
	})

	t.Run("apply counts commands and does not block when inactive", func(t *testing.T) {
		wekanThrottleSet(10, 0) // inactive
		_, _, _, before := wekanThrottleStatus()
		start := time.Now()
		wekanThrottleApply(context.Background())
		assert.Less(t, time.Since(start), 5*time.Millisecond, "must not sleep when inactive")
		_, _, _, after := wekanThrottleStatus()
		assert.Equal(t, before+1, after, "command counter increments")
	})

	t.Run("apply respects a cancelled context (no long block)", func(t *testing.T) {
		wekanThrottleSet(1000, 5000) // active, 1s per command
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		wekanThrottleApply(ctx)
		assert.Less(t, time.Since(start), 200*time.Millisecond, "cancelled context returns promptly")
	})

	// leave the throttle off for any other tests in the package.
	wekanThrottleSet(0, 0)
}
