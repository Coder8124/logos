package buildinfo

import "testing"

func TestAGoInstallBuildReportsTheReleaseItWasFetchedAt(t *testing.T) {
	if got := fromModule("dev", "v0.4.2"); got != "v0.4.2" {
		t.Fatalf("an unstamped build of module v0.4.2 reported %q", got)
	}
}

func TestALocalBuildIsNotMistakenForARelease(t *testing.T) {
	for _, module := range []string{
		"(devel)",
		"",
		"v0.4.3-0.20260912203602-882c8e1abcde",
		"v0.4.3-0.20260912203602-882c8e1abcde+dirty",
		"v0.4.2+dirty",
		"v0.5.0-rc.1",
	} {
		if got := fromModule("dev", module); got != "dev" {
			t.Errorf("module version %q was reported as release %q", module, got)
		}
	}
}
