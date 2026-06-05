//go:build tools

// This file pins the Atlas Bun provider as a module dependency so that
// `go run ariga.io/atlas-provider-bun` (invoked by atlas.hcl in standalone
// mode) resolves. The `tools` build tag excludes it from normal builds.
package main

import _ "ariga.io/atlas-provider-bun/bunschema"
