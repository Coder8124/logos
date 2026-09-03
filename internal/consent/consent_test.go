package consent

import (
	"testing"
	"time"
)

// Every test resets the package-level state first — consent is intentionally
// a global (see consent.go), so tests must not depend on run order.
func reset() {
	Revoke()
}

func TestNotAllowedByDefault(t *testing.T) {
	reset()
	if Allowed() {
		t.Error("Allowed() should be false before any Grant")
	}
	if Remaining() != 0 {
		t.Errorf("Remaining() = %v before any Grant, want 0", Remaining())
	}
}

func TestGrantAllowsForTheDuration(t *testing.T) {
	reset()
	Grant(50 * time.Millisecond)
	if !Allowed() {
		t.Error("Allowed() should be true immediately after Grant")
	}
	if r := Remaining(); r <= 0 || r > 50*time.Millisecond {
		t.Errorf("Remaining() = %v, want (0, 50ms]", r)
	}

	time.Sleep(80 * time.Millisecond)
	if Allowed() {
		t.Error("Allowed() should be false after the grant expires")
	}
	if Remaining() != 0 {
		t.Errorf("Remaining() = %v after expiry, want 0", Remaining())
	}
}

func TestGrantZeroMeansForTheRestOfTheRun(t *testing.T) {
	reset()
	Grant(0)
	if !Allowed() {
		t.Error("Grant(0) should allow immediately")
	}
	// No sleep will make this expire — that is the point of "rest of the run".
	if !Allowed() {
		t.Error("a Grant(0) grant should not expire on its own")
	}
}

func TestNegativeDurationAlsoMeansForTheRestOfTheRun(t *testing.T) {
	reset()
	Grant(-1 * time.Second)
	if !Allowed() {
		t.Error("Grant(negative) should behave like Grant(0)")
	}
}

func TestRevokeWithdrawsAStandingGrant(t *testing.T) {
	reset()
	Grant(time.Hour)
	if !Allowed() {
		t.Fatal("Allowed() should be true right after Grant")
	}
	Revoke()
	if Allowed() {
		t.Error("Allowed() should be false immediately after Revoke")
	}
}

func TestRevokeAlsoWithdrawsAForeverGrant(t *testing.T) {
	reset()
	Grant(0)
	Revoke()
	if Allowed() {
		t.Error("Revoke should cancel a Grant(0) forever-grant too")
	}
}

func TestRegrantOverwritesAnEarlierGrant(t *testing.T) {
	reset()
	Grant(time.Hour)
	Grant(10 * time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	if Allowed() {
		t.Error("a later, shorter Grant should replace the earlier one, not extend it")
	}
}
