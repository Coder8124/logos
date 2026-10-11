package main

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/router"
)

// fakeNight stands in for the launch and for what a dream finds, so the test
// needs neither a second process nor a local model.
func fakeNight(t *testing.T, res dream.Result) *int {
	t.Helper()
	launched := 0
	oldLaunch, oldRun := auto.Launch, dreamRun
	auto.Launch = func(string) error { launched++; return nil }
	dreamRun = func(*sql.DB, string, *router.Router, time.Time, string, bool) (dream.Result, error) {
		return res, nil
	}
	t.Cleanup(func() { auto.Launch, dreamRun = oldLaunch, oldRun })
	if err := auto.Set(vaultPath(), "dream", true); err != nil {
		t.Fatal(err)
	}
	return &launched
}

func resumeShop(t *testing.T) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := runResume([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
}

// The dream switch launches at most one dream a day, says so when it does, and
// the resume after it reads out what the dream did — even when the agent has
// checkpointed in between, which is the ordinary order of things.
func TestAutoDreamRunsAtMostOnceADayAndResumeReportsItsCounts(t *testing.T) {
	standIn(t, "elsewhere")
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	launched := fakeNight(t, dream.Result{Merged: 2, Superseded: 1, Faded: 4, Insights: 3})

	if out := resumeShop(t); !strings.Contains(out, "dream started in the background") {
		t.Errorf("resume launched a dream without saying so:\n%s", out)
	}
	if err := runDreamAuto(); err != nil {
		t.Fatal(err)
	}
	// Both are stamped to the second; the checkpoint has to be after the
	// dream, as it is in a real session, for the report to be tested at all.
	time.Sleep(1100 * time.Millisecond)
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	out := resumeShop(t)
	if !strings.Contains(out, "dream merged 2, superseded 1, 4 fading, 3 connection(s) to review") {
		t.Errorf("resume did not say what the dream did:\n%s", out)
	}
	if *launched != 1 {
		t.Errorf("two resumes in a day launched %d dreams, want 1", *launched)
	}
}

// A night with no model is a night nothing was judged, not a night nothing
// needed merging, and resume must not word the one as the other.
func TestAutoDreamWithNoModelIsReportedAsSkippedNotAsZero(t *testing.T) {
	standIn(t, "elsewhere")
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	fakeNight(t, dream.Result{ReplaySkipped: true, REMSkipped: true})
	resumeShop(t)
	if err := runDreamAuto(); err != nil {
		t.Fatal(err)
	}
	out := resumeShop(t)
	if !strings.Contains(out, "skipped: no local model configured") {
		t.Errorf("resume did not say the dream was skipped for want of a model:\n%s", out)
	}
	if strings.Contains(out, "merged 0") || strings.Contains(out, "connection(s) to review") {
		t.Errorf("a skipped dream was reported as zeros:\n%s", out)
	}
}
