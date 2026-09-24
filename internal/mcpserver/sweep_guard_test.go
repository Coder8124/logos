package mcpserver

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/transcript"
)

// Every resume sweeps transcripts, and the developer's own would be filed into
// the test's vault whenever a project name matched a folder they worked in. A
// test that wants a sweep supplies its own transcripts.
func TestMain(m *testing.M) {
	ingest.RecentTranscripts = func(string, time.Time, time.Time, func(string, time.Time) bool) ([]*transcript.Session, []string) {
		return nil, nil
	}
	os.Exit(m.Run())
}

// sweepable replaces the machine's transcripts with one session of project that
// ended two hours ago without a checkpoint.
func sweepable(t *testing.T, project string) {
	t.Helper()
	s := workedIn("cursor", project)
	s.Ended = time.Now().Add(-2 * time.Hour).Unix()
	s.Started = s.Ended - 1200
	was := ingest.RecentTranscripts
	ingest.RecentTranscripts = func(string, time.Time, time.Time, func(string, time.Time) bool) ([]*transcript.Session, []string) {
		return []*transcript.Session{s}, nil
	}
	t.Cleanup(func() { ingest.RecentTranscripts = was })
}

// A host killed before the shutdown path ran left a session no resume ever saw
// (#134). The resume that finds it says so, with the number — a checkpoint
// nobody wrote appearing in the history reads as a bug otherwise.
func TestResumeRecordsAndAnnouncesASessionThatEndedWithoutACheckpoint(t *testing.T) {
	sweepable(t, "shop")
	srv := &Server{DB: testDB(t), vault: t.TempDir()}
	s := &Session{Server: srv}

	out, err := s.resume("shop", "cursor", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Recorded 1 earlier session that ended without a checkpoint") {
		t.Errorf("resume did not announce the session it recorded:\n%s", out)
	}
	if !strings.Contains(out, "fix the checkout crash") {
		t.Errorf("the recorded session is not in the handoff it was missing from:\n%s", out)
	}
}
