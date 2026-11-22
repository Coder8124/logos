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
	folder := dropSymbols(hostSession)
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
	folder := dropSymbols(hostSession)
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
		// By id, not by name: the folder may be <slug>--<id> or the legacy
		// bare id, and a lookup that only knew one would lose the other's
		// transcript the day the folder was renamed.
		//
		// The bare id is one stat, and most conversations still have just it;
		// the directory listing that finds a slugged folder is what a vault
		// with thousands of conversations cannot pay for in every scope on
		// every lookup. A bare hit stops there only when the transcript is in
		// it: a bare folder can reappear beside a renamed one (an older Logos
		// on another machine, a sync client restoring the old name), and
		// stopping at it then hid the renamed folder's transcript and filed a
		// second copy in the bare one.
		bare := filepath.Join(scopeDir, ConvosDir, folder)
		if fi, err := os.Stat(bare); err == nil && fi.IsDir() && transcriptIn(bare) != "" {
			out = append(out, bare)
			return
		}
		for _, n := range convoFoldersFor(scopeDir, folder) {
			out = append(out, filepath.Join(scopeDir, ConvosDir, n))
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

// KeptSessionIDs lists the host session ids the vault holds anything for: a
// conversation folder (an archived transcript, or checkpoints filed beside
// it) or a checkpoint that names its session. It exists so a short id a person
// typed can be resolved against what is really kept, and refused when it fits
// two conversations. Distinct by the folder spelling, so an id that appears as
// both a folder and a checkpoint's host_session is one conversation.
func KeptSessionIDs(vaultDir string) []string {
	seen := map[string]string{}
	add := func(id string) {
		if key := dropSymbols(id); key != "" {
			if _, ok := seen[key]; !ok {
				seen[key] = id
			}
		}
	}
	root := filepath.Join(vaultDir, CheckpointDir)
	projects, _ := os.ReadDir(root)
	scan := func(scopeDir string) {
		entries, err := os.ReadDir(filepath.Join(scopeDir, ConvosDir))
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				add(convoFolderID(e.Name()))
			}
		}
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		pdir := filepath.Join(root, p.Name())
		scan(pdir)
		nested, _ := os.ReadDir(pdir)
		for _, n := range nested {
			if n.IsDir() && n.Name() != ConvosDir {
				scan(filepath.Join(pdir, n.Name()))
			}
		}
	}
	if scopes, err := Scopes(vaultDir); err == nil {
		for _, sc := range scopes {
			all, err := History(vaultDir, sc, 0)
			if err != nil {
				continue
			}
			for _, c := range all {
				add(c.HostSession)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for _, id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
