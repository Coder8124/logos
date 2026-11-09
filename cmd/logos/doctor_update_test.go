package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/selfupdate"
)

// fakeReleases answers GitHub's latest-release request with tag, and counts
// the requests so a test can assert the plain doctor made none.
func fakeReleases(t *testing.T, tag string, status int) (*selfupdate.Options, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[]}`, tag)
	}))
	t.Cleanup(srv.Close)
	return &selfupdate.Options{Client: &selfupdate.Client{
		HTTP: srv.Client(), APIBase: srv.URL, Repo: "Coder8124/logos", Agent: "logos-test",
	}}, &hits
}

func TestAnOutOfDateBinaryIsToldTheUpgradeForHowItWasInstalled(t *testing.T) {
	opts, _ := fakeReleases(t, "v0.5.8", http.StatusOK)
	for kind, want := range map[selfupdate.InstallKind]string{
		selfupdate.Homebrew:   "brew upgrade logos-mcp",
		selfupdate.NPMManaged: "npm i -g @noeton/logos@latest",
		selfupdate.Standalone: "logos update",
	} {
		c := updateCheck("0.5.7", *opts, kind)
		if c.State != health.Warn || !strings.Contains(c.Detail, "v0.5.8") || c.Fix != want {
			t.Errorf("kind %v: got %+v, want a warning naming v0.5.8 with fix %q", kind, c, want)
		}
	}
}

func TestTheLatestReleaseIsUpToDateWhetherOrNotItsTagHasAV(t *testing.T) {
	opts, _ := fakeReleases(t, "v0.5.8", http.StatusOK)
	for _, current := range []string{"0.5.8", "v0.5.8"} {
		c := updateCheck(current, *opts, selfupdate.Standalone)
		if c.State != health.OK || c.Fix != "" || !strings.Contains(c.Detail, "latest release") {
			t.Errorf("%s: got %+v, want ok and the latest release", current, c)
		}
	}
}

func TestABuildAheadOfTheLatestReleaseIsNotToldToDowngrade(t *testing.T) {
	opts, _ := fakeReleases(t, "v0.5.7", http.StatusOK)
	c := updateCheck("0.5.8", *opts, selfupdate.Standalone)
	if c.State != health.OK || c.Fix != "" || !strings.Contains(c.Detail, "newer than") {
		t.Errorf("got %+v, want ok and newer than the release", c)
	}
}

func TestAFailedReleaseCheckIsSaidNotPassedOff(t *testing.T) {
	opts, _ := fakeReleases(t, "", http.StatusServiceUnavailable)
	c := updateCheck("0.5.7", *opts, selfupdate.Standalone)
	if c.State != health.Unknown || !strings.Contains(c.Detail, "503") {
		t.Errorf("got %+v, want unchecked with the reason", c)
	}
}

func TestADevBuildAsksNobody(t *testing.T) {
	opts, hits := fakeReleases(t, "v0.5.8", http.StatusOK)
	c := updateCheck("dev", *opts, selfupdate.Standalone)
	if c.State != health.Unknown || *hits != 0 {
		t.Errorf("got %+v after %d requests, want unchecked and none", c, *hits)
	}
}

func TestAPlainDoctorStaysOfflineAndSaysHowToCheckTheVersion(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	old := version
	version = "0.5.7"
	t.Cleanup(func() { version = old })
	out := captureStdout(t, func() { doctor(false, false, nil) })
	if !strings.Contains(out, "logos doctor --check-updates") {
		t.Errorf("plain doctor never mentions --check-updates:\n%s", out)
	}
	if strings.Contains(out, "out of date") || strings.Contains(out, "latest release is") {
		t.Errorf("plain doctor reported a release it had no business asking about:\n%s", out)
	}
}

func TestDoctorWithCheckUpdatesShowsTheVersionRow(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	opts, hits := fakeReleases(t, "v99.0.0", http.StatusOK)
	old := version
	version = "0.5.7"
	t.Cleanup(func() { version = old })
	out := captureStdout(t, func() { doctor(false, false, opts) })
	if *hits != 1 || !strings.Contains(out, "logos 0.5.7 is out of date; the latest release is v99.0.0") {
		t.Errorf("%d requests; output:\n%s", *hits, out)
	}
	if strings.Contains(out, "run `logos doctor --check-updates`") {
		t.Errorf("hint shown although the check ran:\n%s", out)
	}
}
