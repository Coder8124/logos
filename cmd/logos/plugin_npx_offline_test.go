package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With no binary installed the plugin falls back to npx, and npx asks the npm
// registry before running even a package it has cached. Behind a proxy or
// offline that was 70 s per launch, paid twice (probe, then serve): Claude
// Code gave up on the server and the SessionStart hook on the restore. With
// --prefer-offline a cached package starts in half a second.
func TestThePluginsNpxFallbackDoesNotWaitOnTheRegistryForACachedPackage(t *testing.T) {
	bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	fakeProgram(t, bin, "npx", `echo "$*" >> '`+calls+`'; echo "logos 0.4.3"`)

	// logos_runs is overridden so no binary installed on this machine, such as
	// a Homebrew logos, is taken before the npx branch is reached.
	got, err := runResolver(t, bin+":/usr/bin:/bin", `logos_runs() { return 1; }; logos_resolve && "${LOGOS[@]}" mcp serve`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("npx was never run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("npx ran %d times, want the probe and the launch:\n%s", len(lines), data)
	}
	for _, l := range lines {
		if !strings.Contains(l, "--prefer-offline") {
			t.Errorf("npx %s asks the registry before using its cache", l)
		}
	}
}
