package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TranscriptStem is the archived host transcript's file name, before the
// extension the host gave it: sessions/<scope>/_convos/<host id>/transcript.jsonl.
//
// One fixed name per conversation folder, not one per harvest, because the
// host appends to the same file for the whole conversation: every harvest's
// copy is a prefix of the next one's, so keeping more than the newest is
// keeping the same bytes again, and a reader asking "what did this
// conversation say" wants one file, not a pick among several.
const TranscriptStem = "transcript"

// IsTranscriptFile reports whether a file in a conversation folder is the
// archived transcript. The in-flight copy (".partial", see the ingest
// archiver) is not: it is either about to be renamed over the real one or is
// the debris of a run that died, and reading it as the transcript would serve
// half a conversation as though it were the whole.
func IsTranscriptFile(name string) bool {
	return strings.HasPrefix(name, TranscriptStem+".") && !strings.HasSuffix(name, ".partial")
}

// TranscriptDir is the conversation folder a harvested transcript for
// hostSession is archived into.
//
// An existing folder for that id wins over the one project would name,
// because the folder is already where that conversation's checkpoints are —
// including a worktree scope (sessions/<project>/<worktree>/_convos/<id>) the
// harvest has no way to know about: a transcript records the directory the
// host stood in, and a worktree's basename is not its scope. Found by the
// folder's name rather than by parsing every checkpoint for its host_session,
// because the name is derived from exactly that frontmatter (Refile keeps the
// two agreeing) and a harvest of a few hundred transcripts would otherwise
// read the whole vault once per transcript.
//
// With no folder yet, the transcript gets the one a checkpoint from the same
// conversation would get under project. No host id, or no project, is an
// error rather than a guess: UnattachedFolder is shared by every conversation
// nothing named, so one transcript there would be overwritten by the next, and
// a project folder made up for an unattributed session is one nobody resuming
// would look in.
func TranscriptDir(vaultDir, project, hostSession string) (string, error) {
	folder := safe(hostSession)
	if folder == "" {
		return "", fmt.Errorf("the transcript names no session id, so it has no conversation folder of its own")
	}
	dirs := convoDirsFor(vaultDir, folder)
	for _, d := range dirs {
		if transcriptIn(d) != "" {
			return d, nil // replace the copy where it already is, never fork a second
		}
	}
	if len(dirs) > 0 {
		return newestConvoDir(dirs), nil
	}
	scope := safeScope(project)
	if scope == "" {
		return "", fmt.Errorf("the transcript's project is unknown, so there is no folder to file it under")
	}
	return filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(scope), ConvosDir, folder), nil
}

// FindTranscript is the archived transcript for hostSession, wherever its
// conversation folder is, or "" when none was ever archived. The lookup is by
// the exact id: a conversation's name or a prefix of its id is a question for
// a resolver this does not try to be, and a near match that served the wrong
// conversation's text would be worse than a miss.
func FindTranscript(vaultDir, hostSession string) string {
	folder := safe(hostSession)
	if folder == "" {
		return ""
	}
	for _, d := range convoDirsFor(vaultDir, folder) {
		if p := transcriptIn(d); p != "" {
			return p
		}
	}
	return ""
}

// convoDirsFor lists every existing conversation folder named folder, in a
// project's scope or one of its worktree scopes — the same two levels Scopes
// reaches, and no deeper. Sorted, so a conversation that somehow has a folder
// in two scopes is resolved the same way every run.
func convoDirsFor(vaultDir, folder string) []string {
	root := filepath.Join(vaultDir, CheckpointDir)
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	try := func(scopeDir string) {
		d := filepath.Join(scopeDir, ConvosDir, folder)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		pdir := filepath.Join(root, p.Name())
		try(pdir)
		nested, err := os.ReadDir(pdir)
		if err != nil {
			continue // one unreadable project must not hide the others
		}
		for _, n := range nested {
			if n.IsDir() && n.Name() != ConvosDir {
				try(filepath.Join(pdir, n.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// newestConvoDir picks, among folders for the same conversation, the one
// holding its latest checkpoint: where it was last working is where a reader
// following it will look.
func newestConvoDir(dirs []string) string {
	best, bestName := dirs[0], ""
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && IsCheckpointFile(e.Name()) && e.Name() > bestName {
				best, bestName = d, e.Name()
			}
		}
	}
	return best
}

// transcriptIn is the archived transcript in one conversation folder, or "".
func transcriptIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && IsTranscriptFile(e.Name()) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
