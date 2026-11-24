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

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommand(t *testing.T) {
	t.Parallel()

	node, argv := command("/opt/tool/tool", []string{"--quiet", "--eval", "a b"})
	if node != "/opt/tool/bin/node" {
		t.Fatalf("node = %q", node)
	}

	want := []string{"/opt/tool/bin/node", "/opt/tool/lib/tool/tool.js", "--quiet", "--eval", "a b"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}

	// No arguments: only the runtime and the program.
	_, argv = command("/opt/tool/tool", nil)
	if !slices.Equal(argv, want[:2]) {
		t.Fatalf("argv = %q, want %q", argv, want[:2])
	}
}

// TestExec builds the launcher, puts a stand-in runtime beside it and checks that
// arguments and the environment arrive unchanged, and that a missing runtime is
// an error rather than a silent success.
func TestExec(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("the stand-in runtime is a shell script")
	}

	dir := t.TempDir()
	launcher := filepath.Join(dir, "tool")

	build := exec.Command("go", "build", "-o", launcher, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	run := func() *exec.Cmd {
		cmd := exec.Command(launcher, "--eval", "a b")
		cmd.Env = append(os.Environ(), "LAUNCHER_TEST=kept")

		return cmd
	}

	if out, err := run().CombinedOutput(); err == nil {
		t.Fatalf("missing runtime must fail, got %q", out)
	}

	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nprintf '%s|' \"$@\" \"$LAUNCHER_TEST\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "node"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run().Output()
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{filepath.Join(dir, "lib", "tool", "tool.js"), "--eval", "a b", "kept"}, "|") + "|"
	if string(out) != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}
