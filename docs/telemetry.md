---
sidebar_position: 12
slug: /telemetry/ # referenced in many places; must not change
---

# Telemetry removal

This fork removes the outbound usage reporter. Compatibility flags and commands
cannot enable reporting: telemetry remains disabled and locked, including when
saved state or an explicit enable flag requests otherwise. Reporter.Run is inert
and does not create an HTTP client or send requests. Local logs and metrics remain;
OpenTelemetry export requires an explicitly configured endpoint.

## Build enforcement

`build.sh build`, `dist`, `dist-seq` and `dist-par` reject unreviewed source or
dependency changes, generate version metadata, then run the no-network reporter
tests before compilation. Tests are uncached and module files are read-only.
Every resulting binary is checked for known removed reporter implementations.
Failures stop the build with an error annotation, including parallel builds.
`build.sh telemetry-check` runs the same preflight independently.

Release All and Release All Missing use these build paths. The source Dockerfile
runs source, native behavior and target-binary checks; the Docker release workflow
also checks downloaded FerretDB binaries before packaging. Go toolchain telemetry
is disabled. These checks are regression defenses, not proof against every
possible new or encoded reporting implementation.

## Dependency review after PR 33

Reviewed SAP go-hdb 1.18.4 to 1.18.9, modernc.org/sqlite 1.58.0 to 1.59.0 and
modernc.org/libc 1.75.6 to 1.75.7. SQLite's required libc pin matches. The changes
cover HANA authentication/protocol handling, SQLite callback-context pooling and
libc memory/string routines; no new default outbound usage reporter was found.
FerretDB does not retain SQLite callback contexts. HANA connections remain
operator-configured database traffic. The reviewed go.mod/go.sum hashes are
updated explicitly; builds never automatically approve dependency changes.

Validation includes SQLite backend, HANA unit and telemetry behavior tests,
a macOS ARM64 build and its binary scan, and negative tests for local, serial
and parallel build failures. Docker images, live HANA and the full native
platform matrix require their own environment and were not run locally.
