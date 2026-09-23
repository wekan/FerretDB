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
metadata. Changed hashes are informational; automated indicator checks compare
known hashes, keyword occurrences and URL literals. Legitimate changes can be
recorded in the indicator baseline without comprehensive review or AI approval.

Tests: `python3 -B tests/release-telemetry.py`,
`python3 -B tests/telemetry-build.py`, and `go test ./internal/util/telemetry`.
Use a repository-local TMPDIR and `GOTELEMETRY=off`.
A native macOS ARM64 binary was built and passed the artifact check; the complete
cross-platform matrix was tested with a compiler fixture, not cross-compiled.

Signatures are regression checks, not a proof about arbitrary or encoded machine
code. Source indicator checks and behavior tests complement them.


Source inventory differences are informational. Automated source indicators
(known hashes, new suspicious keywords and new URL literals) can stop a build;
ordinary changed hashes do not. Artifact signatures and runtime telemetry tests
remain enforced. No AI approval or whole-dependency review is required. See
[release checks](../../releases/README-release.md).
