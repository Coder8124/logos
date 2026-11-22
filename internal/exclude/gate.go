package exclude

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/transcript"
)

// CheckFile is the gate a transcript passes BEFORE it is parsed: the folder
// comes from the first few lines (the one cheap, bounded look the sweep already
// takes to place a transcript), the id from the file's name, and the activity
// from its last write. That look is the only thing read of an excluded
// session, and nothing of it is kept.
//
// The folder is only looked up when a project is excluded: with none, the
// answer cannot depend on it, and a vault that never used the feature pays a
// stat of one small file and nothing else.
func CheckFile(vault, path string, lastWrite time.Time) string {
	r, err := Load(vault)
	if err != nil {
		return ReasonUnread
	}
	if len(r.Projects) == 0 && len(r.Sessions) == 0 && len(r.Pauses) == 0 {
		return ""
	}
	src := transcript.SourceFile(path)
	s := Subject{
		ID:    strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)),
		Start: lastWrite,
		End:   lastWrite,
	}
	if len(r.Projects) > 0 {
		s.Cwd = transcript.FirstCwd(path)
	}
	return r.reason(s, time.Now())
}
