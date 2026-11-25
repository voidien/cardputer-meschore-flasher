//go:build demo

package main

import "testing"

// The demo build is meshflash-demo over the fake world: a name of its own, so its folders are its own
// (~/.config/meshflash-demo, S2) and it never reads or writes the real meshflash's.
func TestDemoBuild(t *testing.T) {
	if name != "meshflash-demo" || fake == nil {
		t.Errorf("the demo build is %q with a fake world %v", name, fake != nil)
	}
}
