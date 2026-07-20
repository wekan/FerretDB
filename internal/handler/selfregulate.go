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
	"bufio"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Autonomous CPU self-regulation.
//
// The `throttle` command lets a client ask FerretDB to slow down, but a client that
// shares the host may be too starved of CPU itself to measure the load or to send
// the request. So FerretDB ALSO regulates itself: a background goroutine samples the
// host's total CPU usage and, when it is too high, adds an increasing delay before
// each command until CPU returns to an acceptable level — then backs the delay off
// again. This keeps FerretDB from monopolizing the host regardless of any client.
//
// The effective per-command delay is the MAX of this self-regulated delay and any
// client-requested `throttle` delay (see throttle.go), so the two cooperate.
var (
	// autoSlowdownMs is the per-command delay FerretDB has chosen on its own.
	autoSlowdownMs atomic.Int64

	// lastCPUPercent is the most recent host CPU% FerretDB measured (0..100),
	// exposed to clients via the throttle command response.
	lastCPUPercent atomic.Int64

	// lastProcCPUPercent is FerretDB's OWN process CPU usage as a percentage where
	// 100 == one full core, so it can exceed 100 on multiple cores (e.g. 250 == 2.5
	// cores). This is the signal that matters when FerretDB monopolises a few cores
	// on a many-core host: the host-wide percentage can look moderate (3 of 4 cores
	// busy == 75% host) while FerretDB alone is at 300%. Exposed to clients so the
	// problem is visible even when the host-wide percentage never crosses a threshold.
	lastProcCPUPercent atomic.Int64
)

// selfRegulateConfig holds the (env-overridable) tuning, read once at start.
type selfRegulateConfig struct {
	enabled  bool
	highPct  int64
	target   int64
	baseMs   int64
	maxMs    int64
	interval time.Duration
}

func loadSelfRegulateConfig() selfRegulateConfig {
	return selfRegulateConfig{
		enabled:  envBool("FERRETDB_CPU_SELF_REGULATE", true),
		highPct:  envInt("FERRETDB_CPU_HIGH_PERCENT", 85),
		target:   envInt("FERRETDB_CPU_TARGET_PERCENT", 70),
		baseMs:   envInt("FERRETDB_CPU_SLOWDOWN_MS", 5),
		maxMs:    envInt("FERRETDB_CPU_SLOWDOWN_MAX_MS", 200),
		interval: time.Duration(envInt("FERRETDB_CPU_INTERVAL_MS", 5000)) * time.Millisecond,
	}
}

func envBool(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "":
		return def
	case "false", "0", "no", "off":
		return false
	default:
		return true
	}
}

func envInt(name string, def int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64); err == nil && v >= 0 {
		return v
	}
	return def
}

// nextAutoSlowdown decides the next self-regulated delay from the current delay and
// the measured host CPU%. Pure and unit-tested:
//   - CPU at/above high  -> escalate (start at base, then double, capped at maxMs);
//   - CPU below target    -> back off (halve; drop to 0 once at/below base);
//   - in between          -> hold.
func nextAutoSlowdown(current, cpuPct, highPct, target, baseMs, maxMs int64) int64 {
	switch {
	case cpuPct >= highPct:
		if current <= 0 {
			return baseMs
		}
		n := current * 2
		if n > maxMs {
			n = maxMs
		}
		return n
	case cpuPct < target:
		if current <= baseMs {
			return 0
		}
		return current / 2
	default:
		return current
	}
}

// procStatCPU reads the aggregate "cpu" line of /proc/stat and returns busy+idle
// jiffies. ok is false when /proc/stat is unavailable (non-Linux) or unparsable, in
// which case self-regulation stays off.
func procStatCPU() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	if !s.Scan() {
		return 0, 0, false
	}

	line := s.Text()
	if !strings.HasPrefix(line, "cpu ") {
		return 0, 0, false
	}

	fields := strings.Fields(line)[1:] // user nice system idle iowait irq softirq steal ...
	if len(fields) < 5 {
		return 0, 0, false
	}

	vals := make([]uint64, 0, len(fields))
	for _, field := range fields {
		v, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, v)
	}
	if len(vals) < 5 {
		return 0, 0, false
	}

	idle = vals[3] + vals[4] // idle + iowait
	for _, v := range vals {
		total += v
	}
	return idle, total, true
}

// procSelfCPU reads FerretDB's own process CPU time (utime+stime) from
// /proc/self/stat in clock ticks. ok is false when the file is unavailable
// (non-Linux) or unparsable. The comm field (2) may contain spaces and parentheses,
// so the fields are read from AFTER the last ')' — utime is overall field 14 and
// stime is field 15, i.e. the 12th and 13th tokens following the last ')'.
func procSelfCPU() (procTicks uint64, ok bool) {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}

	s := string(b)
	rparen := strings.LastIndexByte(s, ')')
	if rparen < 0 || rparen+2 > len(s) {
		return 0, false
	}

	fields := strings.Fields(s[rparen+1:]) // starts at field 3 (state)
	// utime = field 14 -> index 11 here; stime = field 15 -> index 12 here.
	if len(fields) < 13 {
		return 0, false
	}

	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}

	return utime + stime, true
}

// runSelfRegulation is the background loop (started from New, stopped from Close).
func (h *Handler) runSelfRegulation() {
	cfg := loadSelfRegulateConfig()
	if !cfg.enabled {
		autoSlowdownMs.Store(0)
		h.L.Info("CPU self-regulation disabled (FERRETDB_CPU_SELF_REGULATE=false).")
		return
	}

	prevIdle, prevTotal, ok := procStatCPU()
	if !ok {
		h.L.Info("CPU self-regulation unavailable (no readable /proc/stat); relying on client throttle only.")
		return
	}

	// Seed the process-CPU delta too (best effort; process-CPU reporting is disabled
	// if /proc/self/stat is unreadable, without affecting self-regulation).
	numCPU := int64(runtime.NumCPU())
	if numCPU < 1 {
		numCPU = 1
	}
	prevProc, procOK := procSelfCPU()

	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.selfRegulateStop:
			autoSlowdownMs.Store(0)
			return

		case <-ticker.C:
			idle, total, ok := procStatCPU()
			if !ok {
				continue
			}

			idleDelta := idle - prevIdle
			totalDelta := total - prevTotal
			prevIdle, prevTotal = idle, total
			if totalDelta == 0 {
				continue
			}

			cpuPct := int64(100.0 * (1.0 - float64(idleDelta)/float64(totalDelta)))
			if cpuPct < 0 {
				cpuPct = 0
			} else if cpuPct > 100 {
				cpuPct = 100
			}
			lastCPUPercent.Store(cpuPct)

			// FerretDB's OWN process CPU% (100 == one full core), where the host-wide
			// number can hide a few-core peg on a many-core host. Uses the SAME
			// host-jiffies total delta: procDelta is process time across all cores, and
			// one core's worth of jiffies is totalDelta/numCPU, so
			// procPct = 100 * numCPU * procDelta / totalDelta.
			if procOK {
				if proc, ok2 := procSelfCPU(); ok2 {
					procDelta := proc - prevProc
					prevProc = proc
					procPct := int64(100.0 * float64(numCPU) * float64(procDelta) / float64(totalDelta))
					if procPct < 0 {
						procPct = 0
					}
					lastProcCPUPercent.Store(procPct)
				}
			}

			cur := autoSlowdownMs.Load()
			next := nextAutoSlowdown(cur, cpuPct, cfg.highPct, cfg.target, cfg.baseMs, cfg.maxMs)
			if next == cur {
				continue
			}
			autoSlowdownMs.Store(next)

			switch {
			case next > cur:
				h.L.Warn(
					"High CPU: FerretDB self-regulating — increasing the delay it adds between operations to lower CPU.",
					slog.Int64("cpuPercent", cpuPct), slog.Int64("slowDownMs", next),
				)
			case next == 0:
				h.L.Info(
					"CPU back to an acceptable level: FerretDB self-regulation off.",
					slog.Int64("cpuPercent", cpuPct),
				)
			default:
				h.L.Info(
					"CPU easing: FerretDB reducing its self-regulation delay.",
					slog.Int64("cpuPercent", cpuPct), slog.Int64("slowDownMs", next),
				)
			}
		}
	}
}
