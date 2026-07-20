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
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Self-throttle for host-CPU pressure.
//
// A client that shares the host with FerretDB can call the `throttle` command to
// (a) learn how busy FerretDB is and (b) ask it to slow down when the host CPU is
// high. While the throttle is active, every command handled sleeps a small amount
// before running, which lowers FerretDB's CPU use and yields time to other
// software on the host. The throttle self-expires, so a crashed or disconnected
// client can never leave FerretDB permanently slow.
//
// All state is process-global and lock-free (atomics), so the hot path
// (throttleApply, called for every command) is cheap.
var (
	// throttleUntilUnixNano is the deadline; 0 means the throttle is off.
	throttleUntilUnixNano atomic.Int64

	// throttleSleepMs is how long to pause before each command while active.
	throttleSleepMs atomic.Int64

	// commandCounter counts commands processed — an activity signal a client reads
	// to see how busy FerretDB is.
	commandCounter atomic.Int64

	// commandCounts maps command name -> *atomic.Int64, so a client can be told a
	// SUMMARY of what FerretDB has been doing (the busiest commands), not just a
	// total. Concurrency-safe for the per-command hot path.
	commandCounts sync.Map
)

// throttleCount records one processed command (total + per-name). Called for every
// command from the dispatch wrapper.
func throttleCount(name string) {
	commandCounter.Add(1)

	v, ok := commandCounts.Load(name)
	if !ok {
		v, _ = commandCounts.LoadOrStore(name, new(atomic.Int64))
	}
	v.(*atomic.Int64).Add(1)
}

// commandSummary returns a short "name=count, …" summary of the busiest commands
// (up to topN), describing what FerretDB has been doing. Empty when nothing ran.
func commandSummary(topN int) string {
	type kv struct {
		name  string
		count int64
	}

	var all []kv
	commandCounts.Range(func(k, v any) bool {
		all = append(all, kv{k.(string), v.(*atomic.Int64).Load()})
		return true
	})
	if len(all) == 0 {
		return ""
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].name < all[j].name
	})
	if topN > 0 && len(all) > topN {
		all = all[:topN]
	}

	parts := make([]string, 0, len(all))
	for _, e := range all {
		parts = append(parts, fmt.Sprintf("%s=%d", e.name, e.count))
	}
	return strings.Join(parts, ", ")
}

const (
	throttleMaxSleepMs    = 1000    // never pause more than 1s per command
	throttleMaxDurationMs = 300_000 // a throttle request lasts at most 5 minutes
)

// throttleSet enables the throttle: pause sleepMs before each command for the next
// durationMs. Values are clamped to safe bounds. Returns the deadline (unix ns).
func throttleSet(sleepMs, durationMs int64) int64 {
	if sleepMs < 0 {
		sleepMs = 0
	}
	if sleepMs > throttleMaxSleepMs {
		sleepMs = throttleMaxSleepMs
	}
	if durationMs < 0 {
		durationMs = 0
	}
	if durationMs > throttleMaxDurationMs {
		durationMs = throttleMaxDurationMs
	}

	throttleSleepMs.Store(sleepMs)
	until := time.Now().Add(time.Duration(durationMs) * time.Millisecond).UnixNano()
	throttleUntilUnixNano.Store(until)
	return until
}

// throttleActive reports whether the throttle is currently in effect and, if so,
// how long each command should pause.
func throttleActive() (bool, time.Duration) {
	until := throttleUntilUnixNano.Load()
	if until == 0 || time.Now().UnixNano() >= until {
		return false, 0
	}
	return true, time.Duration(throttleSleepMs.Load()) * time.Millisecond
}

// effectiveDelay is the pause applied before each command: the MAX of the
// client-requested throttle delay (when active) and FerretDB's own self-regulated
// delay (autoSlowdownMs, see selfregulate.go). Either can drive the slow-down.
func effectiveDelay() time.Duration {
	var client time.Duration
	if active, d := throttleActive(); active {
		client = d
	}
	auto := time.Duration(autoSlowdownMs.Load()) * time.Millisecond
	if auto > client {
		return auto
	}
	return client
}

// throttleApply pauses before a command runs by the effective delay (client throttle
// and/or FerretDB self-regulation), respecting client disconnect via ctx. Called for
// every command (after throttleCount). Counting is separate so a client can get a
// per-command summary.
func throttleApply(ctx context.Context) {
	d := effectiveDelay()
	if d <= 0 {
		return
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// throttleStatus returns the current throttle state for the command response,
// including FerretDB's self-regulated delay and last measured host CPU%.
func throttleStatus() (active bool, sleepMs, untilNano, commands, autoMs, cpuPct int64) {
	a, _ := throttleActive()
	return a, throttleSleepMs.Load(), throttleUntilUnixNano.Load(), commandCounter.Load(),
		autoSlowdownMs.Load(), lastCPUPercent.Load()
}
