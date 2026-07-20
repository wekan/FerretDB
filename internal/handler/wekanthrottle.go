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
	"sync/atomic"
	"time"
)

// WeKan CPU governor throttle.
//
// When WeKan detects that the host's CPU usage is high (WeKan and FerretDB share
// the machine), it calls the custom `wekanThrottle` command to (a) learn what
// FerretDB is doing and (b) ask it to slow down. While the throttle is active,
// every command handled sleeps a small amount before running, which lowers
// FerretDB's CPU use and yields time to other software. The throttle
// self-expires, so a WeKan crash can never leave FerretDB permanently slow.
//
// All state is process-global and lock-free (atomics), so the hot path
// (wekanThrottleApply, called for every command) is cheap.
var (
	// throttleUntilUnixNano is the deadline; 0 means the throttle is off.
	throttleUntilUnixNano atomic.Int64

	// throttleSleepMs is how long to pause before each command while active.
	throttleSleepMs atomic.Int64

	// commandCounter counts commands processed — an activity signal WeKan reads to
	// see how busy FerretDB is ("what it is doing").
	commandCounter atomic.Int64
)

const (
	wekanThrottleMaxSleepMs    = 1000    // never pause more than 1s per command
	wekanThrottleMaxDurationMs = 300_000 // a throttle request lasts at most 5 minutes
)

// wekanThrottleSet enables the throttle: pause sleepMs before each command for the
// next durationMs. Values are clamped to safe bounds. Returns the deadline (unix ns).
func wekanThrottleSet(sleepMs, durationMs int64) int64 {
	if sleepMs < 0 {
		sleepMs = 0
	}
	if sleepMs > wekanThrottleMaxSleepMs {
		sleepMs = wekanThrottleMaxSleepMs
	}
	if durationMs < 0 {
		durationMs = 0
	}
	if durationMs > wekanThrottleMaxDurationMs {
		durationMs = wekanThrottleMaxDurationMs
	}

	throttleSleepMs.Store(sleepMs)
	until := time.Now().Add(time.Duration(durationMs) * time.Millisecond).UnixNano()
	throttleUntilUnixNano.Store(until)
	return until
}

// wekanThrottleActive reports whether the throttle is currently in effect and, if
// so, how long each command should pause.
func wekanThrottleActive() (bool, time.Duration) {
	until := throttleUntilUnixNano.Load()
	if until == 0 || time.Now().UnixNano() >= until {
		return false, 0
	}
	return true, time.Duration(throttleSleepMs.Load()) * time.Millisecond
}

// wekanThrottleApply counts the command and, when the throttle is active, pauses
// before it runs (respecting client disconnect via ctx). Called for every command.
func wekanThrottleApply(ctx context.Context) {
	commandCounter.Add(1)

	active, d := wekanThrottleActive()
	if !active || d <= 0 {
		return
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// wekanThrottleStatus returns the current throttle state for the command response.
func wekanThrottleStatus() (active bool, sleepMs, untilNano, commands int64) {
	a, _ := wekanThrottleActive()
	return a, throttleSleepMs.Load(), throttleUntilUnixNano.Load(), commandCounter.Load()
}
