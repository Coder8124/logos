package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// convoSep joins a conversation folder's intent slug to the host's id. Neither
// side can contain it: dropSymbols collapses every run of separators to one
// dash, so a host id never holds "--", and slugForConvo collapses the same
// way. That is what lets convoFolderID split on the last occurrence without
// ever mistaking a slug's own dashes for the boundary.
const convoSep = "--"

// maxConvoSlug is how much of the intent a folder name carries. Finder shows
// the middle of a long name as an ellipsis, which is the failure that made
// bare ids unnavigable; forty runes still fits beside a 36-character id.
const maxConvoSlug = 40

// convoFolderID is the host session id a conversation folder stands for: the
// whole name for a legacy folder named only by the id, or what follows the
// last "--" for one named <slug>--<id>. The id is the identity and the slug
// is decoration, so every place that compares a folder name to a host session
// goes through here — comparing names directly would fork a conversation the
// moment its folder was renamed.
func convoFolderID(name string) string {
	if i := strings.LastIndex(name, convoSep); i >= 0 {
		return name[i+len(convoSep):]
	}
	return name
}

// convoSlug is the decoration half of a folder name: the first checkpoint's
// Intent, or its Task when no intent was given, cut at a word boundary. It is
// derived from checkpoint frontmatter and stored nowhere else, so nothing is
// lost by deleting it: Refile (NameConvoFolders) recomputes it from the
// markdown. "" means there is nothing to name the folder after.
func convoSlug(c Checkpoint) string {
	text := strings.TrimSpace(c.Intent)
	if text == "" {
		text = c.Task
	}
	s := safe(text)
	for strings.Contains(s, convoSep) {
		s = strings.ReplaceAll(s, convoSep, "-")
	}
	if utf8.RuneCountInString(s) <= maxConvoSlug {
		return strings.Trim(s, "-")
	}
	runes := []rune(s)
	cut := maxConvoSlug
	// A word boundary if there is one in the window; a single unbroken word
	// longer than the window is cut mid-word rather than dropped.
	if i := strings.LastIndex(string(runes[:cut+1]), "-"); i > 0 {
		return strings.Trim(string(runes[:cut+1])[:i], "-")
	}
	return strings.Trim(string(runes[:cut]), "-")
}

// convoFoldersFor lists the folders under a scope's ConvosDir that stand for
// the host id, in the order lookups prefer them. Two can exist for one id —
// one bare and one slugged after an interrupted rename or a sync — and a
// reader must pick the same one every run or a conversation forks. Slugged
// before bare because the slugged name is where Refile is heading, then by
// name.
func convoFoldersFor(scopeDir, id string) []string {
	entries, err := os.ReadDir(filepath.Join(scopeDir, ConvosDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && convoFolderID(e.Name()) == id {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		bi, bj := out[i] == id, out[j] == id
		if bi != bj {
			return bj
		}
		return out[i] < out[j]
	})
	return out
}

// convoFolderFor is the folder a checkpoint under scopeDir is filed in. An
// existing folder for the id always wins, whatever it is called: later
// checkpoints of a conversation never rename or fork its folder just because
// their intent drifted. Only the conversation's first checkpoint names it.
func convoFolderFor(scopeDir string, c Checkpoint) string {
	id := convoFolder(c.HostSession)
	if id == UnattachedFolder {
		return id
	}
	if have := convoFoldersFor(scopeDir, id); len(have) > 0 {
		return have[0]
	}
	if slug := convoSlug(c); slug != "" {
		return slug + convoSep + id
	}
	return id
}

// NameConvoFolders renames every conversation folder still named by its bare
// host id to <slug>--<id>, from the earliest checkpoint inside it, and returns
// how many it renamed. The whole directory moves in one rename so the
// transcript and any sync copies go with it. It is derived layout: the slug
// comes from checkpoint frontmatter, so it is rebuildable from markdown.
//
// Nothing is renamed over an existing path, and an id that already has two
// folders is left alone: merging them means choosing between two copies of
// possibly different checkpoints, and a wrong guess destroys one. Both stay
// readable (lookups group by id) and the skip is returned for the caller to
// say, so the repair is a person's call, not a silent one.
func NameConvoFolders(vaultDir string) (named int, skipped []string, err error) {
	scopes, err := Scopes(vaultDir)
	if err != nil {
		return 0, nil, err
	}
	for _, scope := range scopes {
		dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(scope)))
		entries, e := os.ReadDir(filepath.Join(dir, ConvosDir))
		if e != nil {
			continue
		}
		for _, en := range entries {
			name := en.Name()
			if !en.IsDir() || name == UnattachedFolder || convoFolderID(name) != name {
				continue
			}
			shown := filepath.ToSlash(filepath.Join(CheckpointDir, scope, ConvosDir, name))
			if len(convoFoldersFor(dir, name)) > 1 {
				skipped = append(skipped, shown+": another folder already stands for this conversation, left both")
				continue
			}
			c, ok := earliestIn(filepath.Join(dir, ConvosDir, name))
			if !ok {
				continue
			}
			slug := convoSlug(c)
			if slug == "" {
				continue
			}
			target := filepath.Join(dir, ConvosDir, slug+convoSep+name)
			if _, e := os.Lstat(target); e == nil {
				skipped = append(skipped, shown+": "+slug+convoSep+name+" already exists, not renamed over it")
				continue
			}
			if e := os.Rename(filepath.Join(dir, ConvosDir, name), target); e != nil {
				return named, skipped, fmt.Errorf("naming conversation folder %s after its intent: %w", shown, e)
			}
			named++
		}
	}
	return named, skipped, nil
}

// earliestIn parses the oldest readable, non-empty checkpoint in one
// conversation folder. Oldest by filename, the clock History sorts on.
func earliestIn(dir string) (Checkpoint, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Checkpoint{}, false
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && IsCheckpointFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		if c := ParseCheckpoint(string(raw)); !c.Empty() {
			return c, true
		}
	}
	return Checkpoint{}, false
}
