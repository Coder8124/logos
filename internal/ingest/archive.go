package ingest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/transcript"
	"github.com/Coder8124/logos/internal/vault"
)

// Archiving exists because of the one assumption this package makes that the
// user cannot enforce: "the transcript stays where it is" (candidate.go). It
// routinely holds pasted secrets and dead-end reasoning nobody meant to
// persist, so logos will not copy it into the vault — and that is still right.
// But distillation re-reads the source, so on the day a corp wipe or a log
// rotation takes the transcripts, every harvest becomes permanently
// un-upgradable and every provenance line starts citing a file nobody can
// produce. Archive is the third option: the user names storage they choose,
// under their own policy, and the queue is repointed at it.

// ArchiveResult counts what one archive run did, so the caller can announce it
// with numbers rather than a reassuring adjective (invariant 3).
type ArchiveResult struct {
	Dir       string // where the copies landed, absolute
	Copied    int    // distinct transcripts copied by this run
	Repointed int    // candidates now citing the archive
	Already   int    // candidates already citing a transcript inside Dir
	Missing   int    // distinct transcripts that were already gone
	Failed    int    // distinct transcripts that are still there but could not be copied
	Bytes     int64
}

// Archive copies every transcript the queue still cites into destDir and
// rewrites each candidate to cite the copy.
//
// Transcripts are deduplicated by path: Cursor addresses every chat in a
// workspace as <state.vscdb>#<chat id>, so copying per candidate would write
// the same database once per chat. The fragment is carried across onto the new
// path, since it is what says which chat the candidate was.
//
// The second return is one line per candidate that could not be archived. A
// missing transcript is the case the user is running this to get ahead of, so
// it is named rather than folded into a count of successes (invariant 4). Such
// a candidate keeps citing where its transcript used to be: that path is the
// provenance a reviewer reads, and blanking it loses the record of where the
// claims came from.
func Archive(vaultDir, destDir string) (ArchiveResult, []string, error) {
	dest, err := filepath.Abs(destDir)
	if err != nil {
		return ArchiveResult{}, nil, err
	}
	res := ArchiveResult{Dir: dest}
	if err := vault.MkdirPrivate(dest); err != nil {
		return res, nil, err
	}

	notes, problems := candidateFiles(vaultDir)
	// One copy per distinct transcript, reused by every candidate citing it.
	copied := map[string]string{}
	// And one report per distinct transcript that could not be copied. Without
	// this a single deleted file cited by three candidates printed the same
	// line three times and counted as three losses.
	failed := map[string]bool{}

	for _, note := range notes {
		c := Parse(note.raw, filepath.Base(note.path))
		// Only what a human can still act on. A rejected candidate is a session
		// the user said no to and a promoted one has already been distilled;
		// copying either one's raw transcript into long-term storage moves
		// pasted secrets somewhere new to keep a window open that is shut.
		if c.Status != "" && c.Status != StatusPending {
			continue
		}
		if c.Source == "" {
			// Not copied, not counted — but said out loud, because a silent
			// skip under a success line is the failure invariant 4 exists for.
			problems = append(problems, fmt.Sprintf("%s: the candidate cites no transcript, so there is nothing to archive", note.path))
			continue
		}
		file, frag := splitSourceFragment(c.Source)
		if under(file, dest) {
			res.Already++
			continue
		}
		if failed[file] {
			continue
		}

		to, ok := copied[file]
		if !ok {
			var n int64
			to, n, err = copyTranscript(file, dest, c.Harness)
			if err != nil {
				failed[file] = true
				// "Already gone" and "still there but unreadable" are opposite
				// instructions to the user: one says stop looking, the other
				// says fix a permission and run this again.
				if errors.Is(err, fs.ErrNotExist) {
					res.Missing++
				} else {
					res.Failed++
				}
				problems = append(problems, fmt.Sprintf("%s: %v", file, err))
				continue
			}
			copied[file] = to
			res.Copied++
			res.Bytes += n
		}

		if err := repoint(note.path, note.raw, to+frag); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", note.path, err))
			continue
		}
		res.Repointed++
	}
	return res, problems, nil
}

type candidateNote struct {
	path string
	raw  string
}

// candidateFiles reads every candidate note with its path, which All does not
// return and rewriting one needs. An unreadable note is reported, never
// dropped: "we archived everything" while one candidate was skipped in silence
// is the success-shaped failure invariant 4 exists for.
func candidateFiles(vaultDir string) ([]candidateNote, []string) {
	root := filepath.Join(vaultDir, Dir)
	var out []candidateNote
	var problems []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, rerr))
			return nil
		}
		out = append(out, candidateNote{path: path, raw: string(raw)})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf("%s: %v", root, err))
	}
	return out, problems
}

// copyFile streams one file to a new path, private from the first write.
//
// Streamed rather than read whole: a Cursor database runs to hundreds of
// megabytes, and buffering it to write it back is that much resident memory
// for no gain. The copy is written beside its destination and renamed, so an
// interrupted run leaves no half-file that reads as an archived transcript.
func copyFile(from, to string) (int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	tmp, err := os.OpenFile(to+".partial", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), to); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	return n, nil
}

// splitSourceFragment separates the file on disk from Cursor's "#<chat id>"
// addressing, so a copy can carry the fragment onto its new path.
func splitSourceFragment(source string) (file, fragment string) {
	file = transcript.SourceFile(source)
	return file, strings.TrimPrefix(source, file)
}

// under reports whether path is inside dir, which is how a second run knows it
// has nothing to do rather than copying the archive into itself.
func under(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyTranscript copies one transcript under <dest>/<harness>/, returning where
// it landed. Two harnesses can both call a file "state.vscdb" and two projects
// can both call one "rollout.jsonl", so a name already taken by a different
// transcript is suffixed rather than overwritten — an archive that silently
// replaced somebody's earlier copy would be the durable-state surprise this
// codebase keeps being bitten by.
func copyTranscript(file, dest, harness string) (string, int64, error) {
	dir := filepath.Join(dest, safeSegment(orDefault(harness, "unknown")))
	if err := vault.MkdirPrivate(dir); err != nil {
		return "", 0, err
	}
	to := freeName(dir, filepath.Base(file))

	n, err := copyFile(file, to)
	if err != nil {
		return "", 0, err
	}
	// SQLite's sidecars, for Cursor's state.vscdb: in WAL mode the newest
	// chats live in the "-wal" file until a checkpoint folds them into the
	// database. Copying the main file alone archives a database missing
	// exactly the sessions the user ran this to save. A sidecar that is not
	// there is the normal case (a checkpointed or non-WAL database) and is not
	// an error; one that is there and unreadable is, because a half-copied
	// database that will not open is worse than a refusal.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(file + suffix); err != nil {
			continue
		}
		m, err := copyFile(file+suffix, to+suffix)
		if err != nil {
			return "", 0, fmt.Errorf("copying %s beside the database: %w", filepath.Base(file+suffix), err)
		}
		n += m
	}
	return to, n, nil
}

// freeName returns a path in dir that nothing occupies yet, numbering a taken
// name rather than clobbering it.
func freeName(dir, base string) string {
	to := filepath.Join(dir, base)
	if _, err := os.Stat(to); err != nil {
		return to
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		to = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(to); err != nil {
			return to
		}
	}
}

// repoint rewrites only the source line of a candidate's frontmatter.
//
// Not a Parse/Markdown round trip: a distilled candidate's body is the work an
// agent already paid for, and re-rendering it to change one path risks losing
// whatever the parser does not model. Changing the line that has to change is
// the smaller promise.
func repoint(path, raw, source string) error {
	fm, _ := splitFM(raw)
	if fm == "" {
		return fmt.Errorf("no frontmatter to record the archived path in")
	}
	var found bool
	lines := strings.Split(fm, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, "source:") {
			lines[i] = "source: " + ys(source)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no source line to repoint")
	}
	updated := raw[:4] + strings.Join(lines, "\n") + raw[4+len(fm):]
	return vault.WriteAtomic(path, []byte(updated))
}
