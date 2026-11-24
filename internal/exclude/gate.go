package exclude

import (
	"os"
	"time"

	"github.com/Coder8124/logos/internal/transcript"
)

// CheckFile is the gate a transcript passes BEFORE it is parsed: the id and
// the folder come from the file's name and first lines (transcript.LocateID
// and LocateCwd, which know how each host spells them), the activity from its last write.
// That look is the only thing read of an excluded session, and nothing of it
// is kept.
//
// The file is stat'd here, through transcript.SourceFile, so a caller never
// repeats the dance: a Cursor chat's path names a database and a chat in one
// string, and statting the string reads every chat as deleted.
//
// The folder is only looked up when a project is excluded: with none, the
// answer cannot depend on it, and a vault that never used the feature pays a
// stat of one small file and nothing else.
func (r Rules) CheckFile(path string) string {
	var last time.Time
	if fi, err := os.Stat(transcript.SourceFile(path)); err == nil {
		last = fi.ModTime()
	}
	return r.CheckFileAt(path, last)
}

// CheckFileAt is CheckFile for a caller that already holds the file's last
// write, such as a sweep that stat'd it to apply its window.
func (r Rules) CheckFileAt(path string, lastWrite time.Time) string {
	if why := r.refusesAll(); why != "" {
		return why
	}
	if len(r.Projects) == 0 && len(r.Sessions) == 0 && len(r.Pauses) == 0 {
		return ""
	}
	var id, cwd string
	if len(r.Sessions) > 0 {
		id = transcript.LocateID(path)
	}
	if len(r.Projects) > 0 {
		cwd = transcript.LocateCwd(path)
	}
	return r.reason(Subject{ID: id, Cwd: cwd, Start: lastWrite, End: lastWrite}, time.Now())
}

// SubjectOf is the question to ask about a session that has been read: where
// and when it really ran, which the file's name and last write only guessed.
func SubjectOf(s *transcript.Session) Subject {
	sub := Subject{Cwd: s.Cwd, ID: s.ID}
	if s.Started > 0 {
		sub.Start = time.Unix(s.Started, 0)
	}
	if s.Ended > 0 {
		sub.End = time.Unix(s.Ended, 0)
	}
	return sub
}
