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

func TestNextAutoSlowdown(t *testing.T) {
	const high, target, base, max = 85, 70, 5, 200

	step := func(cur, cpu int64) int64 {
		return nextAutoSlowdown(cur, cpu, high, target, base, max)
	}

	t.Run("escalates from base then doubles, capped, while CPU high", func(t *testing.T) {
		cur := int64(0)
		cur = step(cur, 95)
		assert.Equal(t, int64(5), cur, "first high sample starts at base")
		for _, want := range []int64{10, 20, 40, 80, 160, 200, 200} {
			cur = step(cur, 95)
			assert.Equal(t, want, cur)
		}
	})

	t.Run("holds while CPU is between target and high", func(t *testing.T) {
		assert.Equal(t, int64(40), step(40, 78))
	})

	t.Run("backs off (halves) below target, to 0 once at/below base", func(t *testing.T) {
		assert.Equal(t, int64(20), step(40, 50), "halves")
		assert.Equal(t, int64(0), step(5, 50), "drops to 0 at base")
		assert.Equal(t, int64(0), step(0, 50), "stays off")
	})
}

func TestEffectiveDelay(t *testing.T) {
	// Reset state.
	throttleSet(0, 0)
	autoSlowdownMs.Store(0)
	assert.Equal(t, time.Duration(0), effectiveDelay())

	t.Run("self-regulation drives the delay with no client request", func(t *testing.T) {
		throttleSet(0, 0)
		autoSlowdownMs.Store(40)
		assert.Equal(t, 40*time.Millisecond, effectiveDelay())
	})

	t.Run("effective delay is the max of client and self-regulated", func(t *testing.T) {
		throttleSet(10, 5000) // client asks 10ms
		autoSlowdownMs.Store(50)
		assert.Equal(t, 50*time.Millisecond, effectiveDelay(), "self-regulation higher wins")
		autoSlowdownMs.Store(3)
		assert.Equal(t, 10*time.Millisecond, effectiveDelay(), "client higher wins")
	})

	// Leave state clean.
	throttleSet(0, 0)
	autoSlowdownMs.Store(0)
}

func TestProcStatCPU(t *testing.T) {
	// On Linux CI this reads real /proc/stat; elsewhere it just must not panic.
	idle, total, ok := procStatCPU()
	if ok {
		assert.Greater(t, total, uint64(0))
		assert.LessOrEqual(t, idle, total)
	}
}

func TestProcSelfCPU(t *testing.T) {
	// On Linux this reads real /proc/self/stat; elsewhere it must not panic and
	// simply reports unavailable.
	first, ok := procSelfCPU()
	if !ok {
		return
	}

	// Burn a little CPU so utime/stime advance, then read again: the process CPU
	// jiffies counter is monotonic and must not go backwards.
	sum := 0
	for i := 0; i < 5_000_000; i++ {
		sum += i
	}
	_ = sum

	second, ok := procSelfCPU()
	assert.True(t, ok, "second read should also succeed")
	assert.GreaterOrEqual(t, second, first, "process CPU jiffies are monotonic")
}
