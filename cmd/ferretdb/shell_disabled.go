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

//go:build ferretdb_no_shell

package main

import (
	"context"
	"fmt"
	"os"
)

// runShell reports that this binary was built without the command shell
// (-tags ferretdb_no_shell); mongosh is still built in.
func runShell(_ context.Context, name string, _ []string) int {
	fmt.Fprintf(os.Stderr, "%s: not available in this build of ferretdb (built with ferretdb_no_shell)\n", name)
	return 127
}
