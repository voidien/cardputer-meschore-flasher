//go:build demo

package main

// Phase 2 contract (docs/phase2-design.md): builder E owns this file. Only the demo build (go build -tags demo)
// compiles it, so only meshflash-demo links the fake world and the catalog it carries.

import (
	"meshflash/internal/app"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
)

func init() {
	name = "meshflash-demo"
	fake = func(e app.Env) (flash.Deps, error) { return fakeworld.Demo(e.CacheDir, e.Version, e.Log) }
}
