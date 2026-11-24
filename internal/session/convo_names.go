package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Coder8124/logos/internal/vault"
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
// Task, or its Intent when the task says nothing a person would navigate by,
// cut at a word boundary. Task first because it is the short "what" a person
// scans a folder list for; Intent is the "why", a sentence that truncates
// badly. A task that is a host slash command ("/model opus") names the
// command, not the work, so it is skipped. It is derived from checkpoint
// frontmatter and stored nowhere else: NameConvoFolders recomputes it from the
// markdown. "" means there is nothing to name the folder after.
func convoSlug(c Checkpoint) string {
	for _, text := range []string{c.Task, c.Intent} {
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, "/") {
			continue
		}
		if s := cutSlug(slugWords(text)); s != "" {
			return s
		}
	}
	return ""
}

// slugWords keeps letters and digits and turns every other run into one dash.
// Not safe(): safe spells a symbol out as its code point so two titles stay
// distinct, which put "u-7e" for a "~" in the middle of a folder name. The id
// beside the slug already keeps folders distinct, so the slug only has to read
// well. One dash per run also means it can never hold convoSep.
func slugWords(text string) string {
	return dashRuns(text, func(rune) bool { return true })
}

func cutSlug(s string) string {
	if utf8.RuneCountInString(s) <= maxConvoSlug {
		return s
	}
	runes := []rune(s)
	window := string(runes[:maxConvoSlug+1])
	// A word boundary if there is one in the window; a single unbroken word
	// longer than the window is cut mid-word rather than dropped.
	if i := strings.LastIndex(window, "-"); i > 0 {
		return strings.Trim(window[:i], "-")
	}
	return strings.Trim(string(runes[:maxConvoSlug]), "-")
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
	return convoName(convoSlug(c), id)
}

// convoName joins a slug to an id, held to maxNameLen bytes. The slug is cut
// to what room the id leaves, at a word boundary where there is one and at a
// rune boundary otherwise, and dropped entirely when the id alone fills the
// name: a 255-byte host id plus any slug is a name the filesystem refuses, and
// the id is the identity, so it is the slug that gives way. The cap matters on
// lookup too — a name that could never have been written must not be looked for.
func convoName(slug, id string) string {
	room := maxNameLen - len(id) - len(convoSep)
	if slug == "" || room < 1 {
		return id
	}
	if len(slug) > room {
		cut := capAt(slug, room)
		if len(cut) < len(slug) && slug[len(cut)] != '-' {
			if i := strings.LastIndex(cut, "-"); i > 0 {
				cut = cut[:i]
			}
		}
		slug = strings.Trim(cut, "-")
	}
	if slug == "" {
		return id
	}
	return slug + convoSep + id
}

// lockConvoFolders serialises choosing, creating and renaming conversation
// folders across processes. It is its own lock, not auto-records: that one
// guards rewrites of auto record files and is held for the whole of a grow, so
// taking it on every checkpoint would stall each Commit behind an unrelated
// distil. Order, where both are held: auto-records first, then this one, which
// is how NameConvoFolders takes them.
func lockConvoFolders(vaultDir string) (*vault.Guard, error) {
	return vault.Lock(vaultDir, "convo-folders")
}

// NameConvoFolders renames every conversation folder still named by its bare
// host id to <slug>--<id>, from the earliest checkpoint inside it, and returns
// how many it renamed. The whole directory moves in one rename so the
// transcript and any sync copies go with it. It is derived layout: the slug
// comes from checkpoint frontmatter, so it is rebuildable from markdown.
//
// Nothing is renamed over an existing path, and an id that already has more
// than one folder — a bare one beside a slugged one, or two slugged ones — is
// left alone: merging them means choosing between two copies of possibly
// different checkpoints, and a wrong guess destroys one. All stay readable
// (lookups group by id) and the id is returned once for the caller to say, so
// the repair is a person's call, not a silent one.
//
// Each rename holds auto-records and then convo-folders, and re-checks under
// them. convo-folders keeps a checkpoint being filed from choosing the old
// name and recreating it after the move; auto-records keeps a grow or distil
// of an auto record from having its folder moved mid-rewrite.
func NameConvoFolders(vaultDir string) (named int, skipped []string, err error) {
	named, skipped, errs := nameConvoFolders(vaultDir, false)
	return named, skipped, errors.Join(errs...)
}

// nameConvoFolders is NameConvoFolders that keeps going after a folder that
// will not rename, one error per folder, and that can count without renaming:
// a dry run says "would name N" from the same decisions the real run makes.
func nameConvoFolders(vaultDir string, dryRun bool) (named int, skipped []string, errs []error) {
	scopes, err := Scopes(vaultDir)
	if err != nil {
		return 0, nil, []error{err}
	}
	for _, scope := range scopes {
		dir := filepath.Join(vaultDir, CheckpointDir, filepath.FromSlash(safeScope(scope)))
		entries, e := os.ReadDir(filepath.Join(dir, ConvosDir))
		if e != nil {
			continue
		}
		byID := map[string][]string{}
		for _, en := range entries {
			if en.IsDir() && en.Name() != UnattachedFolder {
				id := convoFolderID(en.Name())
				byID[id] = append(byID[id], en.Name())
			}
		}
		for _, en := range entries {
			name := en.Name()
			if !en.IsDir() || name == UnattachedFolder {
				continue
			}
			id := convoFolderID(name)
			shown := filepath.ToSlash(filepath.Join(CheckpointDir, scope, ConvosDir, name))
			if len(byID[id]) > 1 {
				// Said once per id, at its first folder in listing order.
				if byID[id][0] == name {
					skipped = append(skipped, shown+": more than one folder stands for this conversation ("+
						strings.Join(byID[id], ", ")+"), left all")
				}
				continue
			}
			if id != name {
				continue // already named
			}
			ok, why, e := nameOne(vaultDir, dir, name, dryRun)
			if e != nil {
				errs = append(errs, fmt.Errorf("naming conversation folder %s after its intent: %w", shown, e))
				continue
			}
			if why != "" {
				skipped = append(skipped, shown+": "+why)
			}
			if ok {
				named++
			}
		}
	}
	return named, skipped, errs
}

// nameOne renames one bare folder under the locks, re-reading everything the
// unlocked scan in NameConvoFolders only guessed: a checkpoint filed in the
// meantime may already have created a slugged sibling.
func nameOne(vaultDir, scopeDir, name string, dryRun bool) (renamed bool, why string, err error) {
	if dryRun {
		return nameOneLocked(scopeDir, name, true)
	}
	ar, err := lockAutoRecords(vaultDir)
	if err != nil {
		return false, "", err
	}
	defer ar.Unlock()
	cf, err := lockConvoFolders(vaultDir)
	if err != nil {
		return false, "", err
	}
	defer cf.Unlock()
	return nameOneLocked(scopeDir, name, false)
}

func nameOneLocked(scopeDir, name string, dryRun bool) (renamed bool, why string, err error) {
	if have := convoFoldersFor(scopeDir, name); len(have) != 1 || have[0] != name {
		if len(have) > 1 {
			return false, "another folder now stands for this conversation, left both", nil
		}
		return false, "", nil
	}
	c, ok := earliestIn(filepath.Join(scopeDir, ConvosDir, name))
	if !ok {
		return false, "", nil
	}
	target := convoName(convoSlug(c), name)
	if target == name {
		return false, "", nil
	}
	if _, e := os.Lstat(filepath.Join(scopeDir, ConvosDir, target)); e == nil {
		return false, target + " already exists, not renamed over it", nil
	}
	if dryRun {
		return true, "", nil
	}
	if e := os.Rename(filepath.Join(scopeDir, ConvosDir, name), filepath.Join(scopeDir, ConvosDir, target)); e != nil {
		return false, "", e
	}
	return true, "", nil
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
