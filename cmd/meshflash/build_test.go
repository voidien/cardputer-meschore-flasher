//go:build !demo

package main

import "testing"

// The release binary is meshflash over the real world: no Fake, so it never links one (make test checks the linked
// packages too), and its own folders, ~/.config/meshflash and ~/.cache/meshflash.
func TestReleaseBuild(t *testing.T) {
	if name != "meshflash" || fake != nil {
		t.Errorf("the release build is %q with a fake world %v", name, fake != nil)
	}
}
