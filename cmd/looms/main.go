// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"os"

	"github.com/teradata-labs/loom/pkg/sandbox/script"
)

func main() {
	// A script child (pkg/sandbox/script) runs before anything else: it must
	// not read settings or keys, which the commands below do on start.
	if len(os.Args) == 2 && os.Args[1] == script.ChildCommand {
		os.Exit(script.ServeChild(os.Stdin, os.Stdout, os.Exit))
	}
	Execute()
}
