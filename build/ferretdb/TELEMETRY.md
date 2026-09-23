# Release telemetry gate

Both release workflows check the reviewed source, run the offline gate/matrix
tests and existing telemetry behavior tests, then invoke the common build script.
Local builds and both serial/parallel release matrices also check source and each
native binary. Failures emit `::error::Telemetry audit failed`, return nonzero
and prevent checksumming/publication. A binary audit failure cannot become an
unsupported-platform skip.

The former usage reporter remains inert and locked disabled. Its unused beacon
URL default has been removed. Logging, local metrics and explicitly configured
tracing remain; the trace exporter is constructed only with a nonempty user URL.

`telemetry-source.json` inventories cmd/, internal/, ferretdb/, and pinned module
metadata. Before refreshing its reviewed map, inspect all changed source and
dependency behavior for default outbound reporting and run the regression tests.
Use `snapshot(root, policy)` from `check-telemetry.py` after that review. Builds
never update the manifest automatically.

Tests: `python3 -B tests/release-telemetry.py`,
`python3 -B tests/telemetry-build.py`, and `go test ./internal/util/telemetry`.
Use a repository-local TMPDIR and `GOTELEMETRY=off`.
A native macOS ARM64 binary was built and passed the artifact check; the complete
cross-platform matrix was tested with a compiler fixture, not cross-compiled.

Signatures are regression checks, not a proof about arbitrary or encoded machine
code. The reviewed source and behavior tests are required alongside them.
