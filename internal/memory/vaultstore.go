package memory

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/vault"
)

// Memories, written down.
//
// The project's second principle says the database is a cache: delete
// .logos/index.db, reindex, get identical state. That was true of notes and
// checkpoints and quietly false of memories, which lived only in the cache. A
// benchmark case caught it, and it is worth being precise about how bad it was:
// `rm -rf .logos` — the thing the README suggests you do — destroyed every
// preference, every fact about a person, every piece of standing context, with
// no warning and nothing to restore from.
//
// So memories are files now — one per memory, at memories/<kind>/<uid>.md,
// keyed by the content-derived uid computeUID gives it. Not one file per kind:
// 0.6.0 merges two people's vaults with git, and a shared memories/fact.md
// turns every pair of facts learned the same week into a line-level conflict
// neither side caused. One file per memory makes that merge the thing git is
// actually good at — a union of files nobody touched in the same place — so
// two vaults combine the way two people's notes do, not the way two people's
// edits to the same paragraph do.
//
// memories/<kind>.md still exists, but only as a generated digest — the page
// a person actually wants to read (open memories/preference.md, see what it
// thinks you like) assembled from the per-memory files, never itself the
// truth. A hand edit to it is never silently adopted or silently discarded:
// flushLocked refuses to overwrite it once its bytes no longer match what
// logos last wrote, and names the per-memory file to edit instead; `logos
// index` has the authority a plain Store call doesn't and regenerates it
// unconditionally, the same way it already wins over any other hand edit.
//
// The bookkeeping — id, confidence, salience, provenance — rides in an HTML
// comment on both the digest's bullet and the per-memory file's own copy of
// it. Obsidian does not render those, so either file reads as clean prose
// while still round-tripping everything the store needs.

// Dir is where memories live inside the vault.
const Dir = "memories"

// The vault a store writes through to, keyed by the database it belongs to.
//
// This used to be one package-level string, on the reasoning that threading a
// path through Store, Forget, Learn and both dream passes was the worse cost.
// That was true right up until a process could hold two vaults at once, which
// the public API made ordinary: the second Open overwrote the binding, so one
// vault's memories were written into the other's directory and then erased by
// its next whole-file flush. Neither vault ended up with the memory.
//
// Keying by *sql.DB restores the pairing without touching a single call
// signature — the database handle is already threaded everywhere the path would
// have had to go.
var (
	vaultMu sync.RWMutex
	vaults  = map[*sql.DB]string{}
)

// SetVault tells one store where its durable copy belongs. An empty dir unbinds
// it, which disables writing through — what tests that only care about the
// cache want, and what index.Close does so a closed handle leaves nothing
// behind.
func SetVault(db *sql.DB, dir string) {
	forgetLoggedHigh(db)
	pendingStamps.Forget(db)
	// Only on an actual rebind from one real vault to a different one, not on
	// every bind: a stamp describes a file in the vault it was taken in, and
	// used to live in a process-local map that could not tell one vault's
	// binding from another's, so it had to be dropped unconditionally or a
	// copied vault's file matching the old stamp byte for byte would skip
	// adopting it and the next rewrite would delete every line the handle's
	// rows did not already hold (see
	// TestRebindingToAnotherVaultDoesNotTrustTheFirstVaultsStamps). Now
	// digest_stamps lives in this db's own index.db, so a fresh handle's
	// first bind (old == "") must not drop it — that is the normal case for
	// every "logos" subcommand, a brand-new process whose only link to a
	// prior one is this same index.db file, and dropping here is exactly the
	// cross-process persistence this table exists to stop losing. Unbinding
	// (dir == "") must not drop it either: Close just stops writing through,
	// and the stamps stay good for whenever this vault is bound again.
	if old := vaultFor(db); old != "" && dir != "" && old != dir {
		dropStamps(db)
	}
	vaultMu.Lock()
	defer vaultMu.Unlock()
	if dir == "" {
		delete(vaults, db)
		return
	}
	vaults[db] = dir
}

// vaultFor returns the vault bound to a database, or "" if it has none.
func vaultFor(db *sql.DB) string {
	vaultMu.RLock()
	defer vaultMu.RUnlock()
	return vaults[db]
}

// The hash of each kind's file as this process last saw it, so a reconcile can
// tell "nobody has touched this since we wrote it" from "there is something to
// adopt" without doing any of the work.
//
// This exists because reconciling on every mutation is quadratic without it. A
// full pass upserts every line in the file, so storing n memories in a row
// re-upserts 1 + 2 + ... + n rows — at two thousand memories that is four
// million SQLite writes and a run that never finishes. The stamp turns the
// common case, where the only writer is us, into one file read.
//
// Deliberately a hash rather than size-and-mtime. Cheaper checks assume a clock
// resolution the filesystem does not promise, and the failure they admit — an
// edit of the same length inside the same mtime tick — is silently losing the
// user's correction, which is the exact bug reconciling exists to fix. Reading
// the file is not the expensive half.
//
// Persisted in index.db's digest_stamps table, not a process-local Go map.
// Every "logos memory add" is its own process with a fresh map, so a
// process-local stamp never once saw its own prior write — the hand-edit
// refusal flushLocked exists to give never fired outside one long-lived host
// such as the MCP server. Losing this table (index.db deleted) is safe the
// same way losing unflushed's marks is: the next write just finds no stamp,
// does the full pass, and goes through — never a lost memory, only a missed
// detection of an edit that happened in the gap.
var stampMu sync.Mutex // serialises this process's own digest_stamps reads/writes; SQLite handles cross-process safety

// withKind runs fn holding the vault-wide lock for one kind, across every
// process on the machine. See internal/vault/lock.go for why it is an OS lock
// and not a mutex.
//
// The span matters as much as the lock. Storing a memory is reconcile, then
// insert, then flush, and the dangerous moment is not inside any one of them —
// it is between the insert and the flush, when the row exists in the index and
// not yet in the file. Anything that reconciles in that window reads the file
// as authoritative, cannot see the new row, and deletes it as a line the user
// removed by hand. forgetRow runs, memory_log records a deletion nobody made,
// and the writer is told its memory was saved. That is the failure this whole
// product exists not to have, and it is why callers wrap the entire sequence
// rather than locking each step.
//
// Keyed per kind: a write to memories/fact.md has no reason to wait on
// memories/preference.md.
//
// An unbound store — no vault, which is what a cache-only test wants — has no
// file to serialise, so fn runs directly.
func withKind(db *sql.DB, kind Kind, fn func() error) error {
	dir := vaultFor(db)
	// An empty kind reaches here from Forget and setPin when the id does not
	// exist, so there is no row and no file. Locking it would put a stray
	// memory-.lock in the vault for a call that touches nothing.
	if dir == "" || kind == "" {
		return fn()
	}
	g, err := vault.Lock(dir, "memory-"+string(kind))
	if err != nil {
		return err
	}
	defer g.Unlock()
	return fn()
}

func stamp(db *sql.DB, kind Kind, sum [sha256.Size]byte) {
	stampMu.Lock()
	defer stampMu.Unlock()
	// Best-effort: a failure here just means the next write finds no stamp
	// and does a full pass — never a lost or corrupted memory. Nothing in
	// this package can usefully recover from a failed bookkeeping write, so
	// it is silent rather than surfaced on a path whose caller already
	// succeeded at the write that matters.
	_, _ = db.Exec(`INSERT INTO digest_stamps(kind, sha) VALUES(?, ?)
		ON CONFLICT(kind) DO UPDATE SET sha = excluded.sha`, string(kind), hex.EncodeToString(sum[:]))
}

// stampSeen is the hash of the digest this process (or an earlier one, since
// the table outlives any one process) last wrote for this kind, and whether
// one was ever recorded — flushLocked's hand-edit guard needs that second
// value, since a plain bool can't tell "recorded one, but it doesn't match"
// apart from "no stamp yet", and a kind's first-ever write must never refuse.
func stampSeen(db *sql.DB, kind Kind) ([sha256.Size]byte, bool) {
	stampMu.Lock()
	defer stampMu.Unlock()
	var hexSum string
	err := db.QueryRow(`SELECT sha FROM digest_stamps WHERE kind = ?`, string(kind)).Scan(&hexSum)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	raw, err := hex.DecodeString(hexSum)
	if err != nil || len(raw) != sha256.Size {
		return [sha256.Size]byte{}, false
	}
	var sum [sha256.Size]byte
	copy(sum[:], raw)
	return sum, true
}

// dropStamps forgets every kind's claim for this vault, so a handle rebound
// to a different directory (SetVault) never trusts a hash that described the
// previous vault's file.
func dropStamps(db *sql.DB) {
	stampMu.Lock()
	defer stampMu.Unlock()
	_, _ = db.Exec(`DELETE FROM digest_stamps`)
}

// dropStamp forgets the claim for one kind, so the next reconcile does a full
// pass instead of trusting a hash that no longer describes the file.
func dropStamp(db *sql.DB, kind Kind) {
	stampMu.Lock()
	defer stampMu.Unlock()
	_, _ = db.Exec(`DELETE FROM digest_stamps WHERE kind = ?`, string(kind))
}

// kinds is the fixed set, so exporting is deterministic and a kind with no
// memories still gets its file emptied rather than left stale.
var kinds = []Kind{Preference, Person, Fact, Context, Procedure}

// flush writes one kind's file. Called after every mutation.
//
// Whole-file rewrites rather than an append log: a memory can be edited,
// superseded or forgotten, and reconstructing current state from a log on every
// read is how the cache became authoritative in the first place. The files are
// small — a few hundred short lines each — so this is cheaper than the SQLite
// write that precedes it.
//
// The error is returned rather than dropped. It used to be discarded, which
// meant a read-only directory or a full disk produced a memory that existed
// only in the cache while the caller was told it was saved — and the cache is
// the thing the README tells people to delete. A write that cannot be made
// durable has to say so.
func flush(db *sql.DB, kind Kind) error {
	return withKind(db, kind, func() error { return flushLocked(db, kind) })
}

// flushLocked is flush with the kind's lock already held. Callers that need the
// insert and the export to be indivisible — Store, Accept — hold it across all
// of it and call this.
func flushLocked(db *sql.DB, kind Kind) error {
	dir := vaultFor(db)
	if dir == "" {
		return nil
	}
	// memories/<kind>.md is a generated digest now, not the record a mutation
	// writes through to — that is the per-memory file exportMemoryFile already
	// wrote before this ran. Overwriting it here regardless would silently
	// discard a hand edit the moment the next memory of this kind was stored,
	// which is the one outcome a generated-but-editable-looking file must never
	// produce. A stamp we never took (ok == false: nothing of ours has written
	// this kind's digest yet in this process) says nothing either way, so only
	// a stamp that exists and no longer matches refuses the write — `logos
	// index` is the unconditional override, and it writes through ExportKind
	// directly, never through this guard.
	if last, ok := stampSeen(db, kind); ok {
		if onDisk, err := os.ReadFile(filepath.Join(dir, Dir, string(kind)+".md")); err == nil {
			if sha256.Sum256(onDisk) != last {
				return fmt.Errorf("memories/%s.md was hand-edited since logos last wrote it; it is a generated digest now — edit the memory's own file under memories/%s/ instead, then run `logos index` to refresh this one", kind, kind)
			}
		}
	}
	if err := ExportKind(db, dir, kind); err != nil {
		// Record which rows the failed export left behind, so the split state
		// outlives the one call that was told about it. The caller here gets an
		// honest error and then exits; everyone after it — recall, doctor, the
		// next index pass — has only the database to go on.
		markUnflushed(db, dir, kind)
		return fmt.Errorf("memory saved to the cache but not to the vault: %w", err)
	}
	// The file on disk is now this kind, whole. Whatever an earlier failure
	// stranded went out with it, because the export rewrites the entire file
	// rather than appending — which is also why a single later write of any
	// memory of this kind repairs every stranded one.
	// quarantined = 0: a proposal's mark is about the queue file, which this
	// write never touched. See markPendingUnflushed.
	db.Exec("UPDATE memories SET unflushed = 0 WHERE kind = ? AND quarantined = 0 AND unflushed = 1", string(kind))
	// Remember the file we just produced, so flushLocked's hand-edit guard can
	// recognise a later write as ours and let it through. Read back rather than
	// hashing what we think we wrote: if the export and the file on disk ever
	// disagree, the stamp must describe the disk.
	//
	// A read-back that does not succeed must *clear* the stamp, not leave the
	// old one. The common case is not an error at all: exporting a kind whose
	// last memory was just forgotten removes the file, so the read fails and the
	// previous file's hash stayed behind — and a stamp is a claim that the bytes
	// on disk are ours. Restore that file from a backup and the claim is wrong
	// in the one direction that would silently cost the restore: with no stamp,
	// flushLocked's guard has nothing to compare against and skips straight to
	// overwriting, which is why that guard only ever protects a hand edit made
	// to a file the stamp still describes — recovering a restored file is
	// `logos index`'s job (see readKindFiles), not a plain write's.
	if raw, err := os.ReadFile(filepath.Join(dir, Dir, string(kind)+".md")); err == nil {
		stamp(db, kind, sha256.Sum256(raw))
	} else {
		dropStamp(db, kind)
	}
	return nil
}

// markUnflushed flags the memories of one kind that are in the cache and not in
// the file, after an export failed to write that file.
//
// Measured against the bytes actually on disk rather than assumed for the whole
// kind. A failed whole-file export usually leaves the *previous* file intact,
// so most rows of that kind are still perfectly durable, and flagging them all
// would put doctor in the position of naming fifty memories at risk when one
// is. A missing or unreadable file answers the same question the other way: no
// row of this kind is in it, and every one of them is flagged.
//
// Errors are dropped on purpose. This runs on the failure path of a write that
// is already returning an error to the caller, and a second error about the
// bookkeeping would replace the one that says what actually went wrong.
func markUnflushed(db *sql.DB, dir string, kind Kind) {
	onDisk := map[string]bool{}
	if raw, err := os.ReadFile(filepath.Join(dir, Dir, string(kind)+".md")); err == nil {
		for _, m := range parseKind(kind, string(raw)) {
			onDisk[fingerprint(m.Text)] = true
		}
	}
	// quarantined = 0 for the reason Import gives: a pending memory is never
	// written to the file, so its absence from one is not evidence of anything.
	rows, err := db.Query("SELECT id, text FROM memories WHERE kind = ? AND quarantined = 0", string(kind))
	if err != nil {
		return
	}
	var stranded []int64
	for rows.Next() {
		var id int64
		var text string
		if rows.Scan(&id, &text) != nil {
			continue
		}
		if !onDisk[fingerprint(text)] {
			stranded = append(stranded, id)
		}
	}
	rows.Close()
	for _, id := range stranded {
		db.Exec("UPDATE memories SET unflushed = 1 WHERE id = ?", id)
	}
}

// UnflushedIDs is the set of memories the cache holds and the vault does not,
// for a reader that has already loaded some memories and needs to say which of
// them are not durable.
//
// A set rather than a column on Memory because every recall path scans its own
// explicit column list, and threading one more field through all of them would
// touch far more code than the question is worth. One query answers it for a
// whole page of results, and the normal answer is an empty map.
func UnflushedIDs(db *sql.DB) map[int64]bool {
	out := map[int64]bool{}
	rows, err := db.Query("SELECT id FROM memories WHERE unflushed = 1")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// Unflushed counts the memories the cache holds that the vault does not, which
// is what `logos doctor` needs in order to say so. Zero is the normal answer.
func Unflushed(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM memories WHERE unflushed = 1 AND superseded = 0 AND quarantined = 0").Scan(&n)
	return n, err
}

// UnflushedPending counts the proposals in the review queue the cache holds and
// memories/pending.md does not — a separate number from Unflushed because the
// file at risk is a different one, and so is the noun doctor names it by.
func UnflushedPending(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM memories WHERE unflushed = 1 AND superseded = 0 AND quarantined = 1").Scan(&n)
	return n, err
}

// Reconcile and reconcileLocked, which used to adopt a hand edit to
// memories/<kind>.md before anything overwrote it, are gone under A2. Their
// premise — that the kind file is readable truth to adopt edits from — is now
// false: memories/<kind>.md is a generated digest, and the memory's own file
// under memories/<kind>/ is truth. Calling the old code here would treat an
// edit to the digest as the user's intent and delete memories on its say, the
// exact loss this function once existed to prevent, now pointed at the wrong
// file. Recovery for A2 is `logos index` (Import), which rescans
// memories/<kind>/ — see readKindFiles — and the per-write refusal in
// flushLocked, not an adopt-on-read pass. Nothing in this package calls
// Reconcile any more; internal/secretary and internal/dream define their own
// same-named functions over their own files and are unaffected.

// Export writes every kind to the vault.
func Export(db *sql.DB, dir string) error {
	for _, k := range kinds {
		if err := ExportKind(db, dir, k); err != nil {
			return err
		}
	}
	return nil
}

// ExportKind writes one kind's memories to memories/<kind>.md.
//
// Superseded memories are left out. They are history, not knowledge — the
// memory_log already records that they existed and what replaced them, and
// carrying them here would put values the user has already corrected back into
// a file the user reads.
//
// Quarantined memories are left out too, and for a stronger reason: they are
// not yet knowledge at all. A machine proposed them and nobody has said yes.
// Writing them to the file the user reads as "what the assistant knows" would
// make quarantine theatre — reviewable in principle, already believed in
// practice. They start appearing here the moment Accept clears the flag.
func ExportKind(db *sql.DB, dir string, kind Kind) error {
	rows, err := db.Query(
		`SELECT id, text, salience, confidence, project, source, agent, created, last_used, uses, pin, auto_accepted, commit_sha, uid
		 FROM memories WHERE kind = ? AND superseded = 0 AND quarantined = 0 ORDER BY created, id`, string(kind))
	if err != nil {
		return err
	}
	defer rows.Close()

	var mems []Memory
	for rows.Next() {
		var m Memory
		var project, source, agent sql.NullString
		if err := rows.Scan(&m.ID, &m.Text, &m.Salience, &m.Confidence,
			&project, &source, &agent, &m.Created, &m.LastUsed, &m.Uses, &m.Pin, &m.AutoAccepted, &m.Commit, &m.Uid); err != nil {
			return err
		}
		m.Kind, m.Project, m.Source, m.Agent = kind, project.String, source.String, agent.String
		mems = append(mems, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	path := filepath.Join(dir, Dir, string(kind)+".md")
	if len(mems) == 0 {
		// Remove rather than leave an empty file: a kind nobody uses should not
		// clutter the vault, and its absence is not ambiguous.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return vault.WriteAtomic(path, []byte(renderKind(kind, mems)))
}

// generatedDigestBanner marks a kind's flat file as logos's own rendered
// output rather than a legacy or hand-authored source. readKindFiles checks
// for it before treating the file's lines as data to migrate — the fixed
// prefix is enough to recognize, since the rest of the sentence names the
// kind and varies per file.
const generatedDigestBanner = "Generated by logos from memories/"

func renderKind(kind Kind, mems []Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: memory-store\nkind: %s\ncount: %d\n---\n\n", kind, len(mems))
	fmt.Fprintf(&b, "%s\n\n", blurb(kind))
	b.WriteString(generatedDigestBanner + string(kind) + "/ — a hand edit here is not kept; " +
		"edit the memory's own file under memories/" + string(kind) + "/ instead, then run `logos index`.\n\n")
	for _, m := range mems {
		b.WriteString(renderLine(m))
	}
	return b.String()
}

// renderLine is one memory's bullet, in the digest and in its own file alike
// — the one format both are built from, so a memory's file and its line in
// the generated digest never drift into two different renderings of the same
// row.
func renderLine(m Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s <!-- logos id=%d conf=%.2f sal=%.2f src=%s created=%s uses=%d",
		oneLine(m.Text), m.ID, m.Confidence, m.Salience, orDash(m.Source),
		time.Unix(m.Created, 0).UTC().Format(time.RFC3339), m.Uses)
	// Ranking trusts this, and decay reads it: without it in the file, a
	// reindex restarts every memory's clock at `created`. Omitted when it
	// is zero so a never-recalled memory's line reads as it always has.
	if m.LastUsed > 0 {
		fmt.Fprintf(&b, " used=%s", time.Unix(m.LastUsed, 0).UTC().Format(time.RFC3339))
	}
	if m.Project != "" {
		fmt.Fprintf(&b, " project=%s", logField(m.Project))
	}
	if m.Agent != "" {
		fmt.Fprintf(&b, " agent=%s", strings.ReplaceAll(m.Agent, " ", "-"))
	}
	// Omitted for the common case (PinNone) so an untouched memory's
	// comment reads exactly as it always has — pin is the one piece of
	// bookkeeping here a person is expected to want to see and edit by
	// hand, not just round-trip.
	if m.Pin != PinNone {
		fmt.Fprintf(&b, " pin=%d", m.Pin)
	}
	// Only the review switch's accepts carry it: a memory a person
	// accepted reads as it always has.
	if m.AutoAccepted > 0 {
		fmt.Fprintf(&b, " accepted=auto@%s", time.Unix(m.AutoAccepted, 0).UTC().Format(time.RFC3339))
	}
	b.WriteString(commitField(m.Commit))
	b.WriteString(uidField(m.Uid))
	b.WriteString(" -->\n")
	return b.String()
}

// memoryFilePath is where one memory's own file lives — memories/<kind>/<uid>.md.
func memoryFilePath(dir string, kind Kind, uid string) string {
	return filepath.Join(dir, Dir, string(kind), uid+".md")
}

// renderMemoryFile is the content of one memory's own file: a small
// frontmatter naming which memory it is, then the one bullet renderKind would
// have given it.
func renderMemoryFile(kind Kind, m Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: memory\nkind: %s\nuid: %s\n---\n\n", kind, m.Uid)
	b.WriteString(renderLine(m))
	return b.String()
}

// writeMemoryFile durably writes one memory to its own file. Every write that
// reaches here already has a uid — Store, upsert and migration all compute
// one before calling it — because a memory with no uid cannot be filed or
// merged across vaults, which is the entire reason A2 exists.
func writeMemoryFile(dir string, kind Kind, m Memory) error {
	if m.Uid == "" {
		return fmt.Errorf("memory #%d has no uid to file it under", m.ID)
	}
	return vault.WriteAtomic(memoryFilePath(dir, kind, m.Uid), []byte(renderMemoryFile(kind, m)))
}

// removeMemoryFile deletes one memory's file — called once it is superseded,
// quarantined or forgotten, none of which belong in what the vault shows as
// current knowledge. Already gone is not an error: the file might never have
// existed (a row restored under its old id, or one from before A2's
// migration touched this kind), and the outcome either way is "no file here".
func removeMemoryFile(dir string, kind Kind, uid string) error {
	if uid == "" {
		return nil
	}
	if err := os.Remove(memoryFilePath(dir, kind, uid)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// exportMemoryFile keeps one memory's own file in step with the row a
// mutation just changed — reading that one row back and writing (or
// removing) that one file, never a whole kind's worth. That is what keeps
// A2 from reintroducing the exact dead end the per-kind stamp above was built
// to fix: writing every per-memory file on every Store would be O(n) file
// writes per call where the per-kind digest is one, turning two thousand
// Store calls into millions of syscalls instead of the thousands the stamp
// already made affordable.
func exportMemoryFile(db *sql.DB, kind Kind, id int64) error {
	dir := vaultFor(db)
	if dir == "" || id == 0 {
		return nil
	}
	var m Memory
	var project, source, agent sql.NullString
	var superseded, quarantined int
	err := db.QueryRow(`SELECT text, salience, confidence, project, source, agent,
		created, last_used, uses, pin, auto_accepted, commit_sha, uid, superseded, quarantined
		FROM memories WHERE id = ? AND kind = ?`, id, string(kind)).Scan(
		&m.Text, &m.Salience, &m.Confidence, &project, &source, &agent,
		&m.Created, &m.LastUsed, &m.Uses, &m.Pin, &m.AutoAccepted, &m.Commit, &m.Uid, &superseded, &quarantined)
	if err == sql.ErrNoRows {
		// The row is gone already — forgetRow's own removeMemoryFile call,
		// made while the uid was still readable, is what cleans this up.
		return nil
	}
	if err != nil {
		return err
	}
	m.ID, m.Kind, m.Project, m.Source, m.Agent = id, kind, project.String, source.String, agent.String
	if superseded == 1 || quarantined == 1 {
		return removeMemoryFile(dir, kind, m.Uid)
	}
	if m.Uid == "" {
		// A pre-A1 row upsert hasn't backfilled yet (its own comment says that
		// is A4's job, not this restore path's). No file to write until it has
		// one; the next `logos index` migration pass gives it one.
		return nil
	}
	return writeMemoryFile(dir, kind, m)
}

func blurb(kind Kind) string {
	switch kind {
	case Preference:
		return "How you like things done."
	case Person:
		return "People, and what matters about them."
	case Context:
		return "Standing context — what is going on around you."
	case Procedure:
		return "How to do something here, and the trap in doing it the obvious way. " +
			"Read only by before_you_try — never surfaced by recall or in a context pack."
	default:
		return "Durable facts about you and your work."
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return strings.ReplaceAll(s, " ", "-")
}

// oneLine keeps a memory on one line. Memories are single statements; a stray
// newline would silently split one into two on the next import.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// Import rebuilds the store from the vault. This is what makes `rm -rf .logos`
// survivable.
//
// The files win. Where a memory exists in both places the text, confidence and
// provenance are taken from markdown, because the whole point is that a person
// can open the file, correct a fact, and have the correction stick. Memories
// present in the cache but absent from the file are deleted — that is how
// deleting a line forgets something.
//
// Vectors are not stored in the file; anything without one is re-embedded here,
// which is the same work `logos index` already does for notes.
func Import(db *sql.DB, p *provider.Provider, embedModel, dir string) (imported, rescued int, err error) {
	return importInto(db, p, embedModel, dir, &Edits{})
}

// Edits is what an import changed because of a hand edit: lines with no id
// comment that became memories, and memories whose line is gone. Said by
// `logos index`, because the two together are what a line retyped without its
// comment does — the old memory is forgotten with its project, confidence and
// pin, and a bare one takes its place — and the summary line counts neither.
type Edits struct {
	Added, Forgotten []int64
	// Held is a line left out of memory, each with why and what to do: its
	// text belongs to a memory awaiting review or superseded, so it can be
	// neither that memory nor a new one.
	Held []string
	// Migrated counts memories moved from an old one-file-per-kind vault to
	// their own file under memories/<kind>/, this run. Zero on every run after
	// the first for a given kind — migration is idempotent, keyed off which
	// per-memory files already exist, not off a flag that could get stuck if
	// a run is interrupted.
	Migrated int
}

// ImportEdits is Import, also saying which memories a hand edit made or
// removed. A memory restored under its own id after a wipe is neither.
func ImportEdits(db *sql.DB, p *provider.Provider, embedModel, dir string) (imported, rescued int, e Edits, err error) {
	imported, rescued, err = importInto(db, p, embedModel, dir, &e)
	return imported, rescued, e, err
}

func importInto(db *sql.DB, p *provider.Provider, embedModel, dir string, edits *Edits) (imported, rescued int, err error) {
	if err := Init(db); err != nil {
		return 0, 0, err
	}

	// Every kind's lock, held for the whole import.
	//
	// Reading the files and then deleting every row they do not mention needs
	// the same protection any such read-then-reap pass does, and it did not
	// have it.
	// `logos index` running while an agent's MCP server stores a memory read the
	// file before that memory reached it and forgot the row. The kinds are
	// locked in the fixed order of `kinds`, so an import and a store can never
	// take two of them in opposite orders.
	//
	// dir is the caller's rather than vaultFor's, because `logos index` imports
	// a vault it has not bound to a store yet.
	for _, kind := range kinds {
		g, err := vault.Lock(dir, "memory-"+string(kind))
		if err != nil {
			return 0, 0, err
		}
		defer g.Unlock()
	}

	keep := map[int64]bool{}

	// Only kinds whose file was read and found intact are authoritative. A kind
	// whose file is missing or truncated says nothing about what the user wants
	// forgotten, so its cached memories are left alone below.
	authoritative := map[Kind]bool{}
	var damaged []string

	for _, kind := range kinds {
		raw, existed, n, err := readKindFiles(dir, kind)
		var dmg kindDamaged
		if errors.As(err, &dmg) {
			damaged = append(damaged, string(dmg))
			continue
		}
		if err != nil {
			return imported, rescued, err
		}
		edits.Migrated += n
		if !existed {
			// Absence of information, not an instruction to delete. A sync client
			// mid-restore, a partial rsync, or a crash between two flushes all
			// produce exactly this, and reaping here forgot the whole kind.
			continue
		}

		// A record that ends mid-line was interrupted rather than edited —
		// readKindFiles already caught a torn flat file; this catches a torn
		// per-memory file the same way, since it concatenates into the same
		// raw shape. Acting on it would delete every memory the missing tail
		// held.
		if looksTruncated(raw) {
			damaged = append(damaged, string(kind)+" has a record that ends mid-line")
			continue
		}

		parsed := parseKind(kind, raw)
		for _, m := range parsed {
			id, created, err := upsert(db, p, embedModel, m)
			var held heldLine
			if errors.As(err, &held) {
				edits.Held = append(edits.Held, string(held))
				continue
			}
			if err != nil {
				return imported, rescued, err
			}
			if created {
				edits.Added = append(edits.Added, id)
				// A hand-typed line, or one just migrated out of a legacy flat
				// file, gets upsert's real id and uid — different from
				// whatever renderLine baked into the text parsed above, since
				// that was written before this id existed. Writing the
				// per-memory file now, from the row upsert actually produced,
				// is what keeps this new memory from being truth only in the
				// digest ExportKind regenerates below: without this, the next
				// readKindFiles call (once that digest carries the banner)
				// would find no file for it and silently drop it.
				if err := exportMemoryFile(db, kind, id); err != nil {
					return imported, rescued, err
				}
			}
			keep[id] = true
			imported++
		}
		authoritative[kind] = true
	}

	// A damaged file is reported and never acted on. Importing what survived
	// would be the same silent deletion this guard exists to prevent, and the
	// user needs to know before they overwrite the file by writing a new memory.
	if len(damaged) > 0 {
		return imported, rescued, fmt.Errorf(
			"refusing to import an incomplete memory store (%s); "+
				"restore the file or delete the count line to accept it as-is",
			strings.Join(damaged, "; "))
	}

	// Nothing on disk means nothing was ever exported — a store predating this,
	// or a vault that has never held memories. Deleting the cache in that case
	// would destroy the very data this function exists to protect, so it exports
	// instead of importing.
	if len(authoritative) == 0 {
		// dropStamps after, not before: Export writes every file without
		// recording what it wrote, so any stamp still held describes bytes that
		// are no longer on disk — and a stamp is a claim the file is ours, which
		// makes flushLocked's hand-edit guard let the next write through instead
		// of refusing it.
		defer dropStamps(db)
		// The count before the write, because the write is what settles it: a
		// vault whose very first export failed has no files at all, so this is
		// the branch a stranded memory comes back through, and it is as much a
		// rescue as the one below. Reporting 0 here would have `logos index`
		// stay silent about writing out data that was one wipe from gone.
		rescued, _ = Unflushed(db)
		if err := Export(db, dir); err != nil {
			return 0, 0, err
		}
		db.Exec("UPDATE memories SET unflushed = 0 WHERE quarantined = 0")
		return 0, rescued, nil
	}

	// quarantined = 0: a pending memory was never written to the file (see
	// ExportKind), so it cannot appear in `keep` no matter how thoroughly the
	// file was read. Without this exclusion, every `logos index` would read
	// that absence as "the user deleted this line" and Forget the whole review
	// queue out from under them before they ever saw it.
	rows, err := db.Query("SELECT id, kind, unflushed FROM memories WHERE superseded = 0 AND quarantined = 0")
	if err != nil {
		return imported, rescued, err
	}
	var orphans []int64
	alreadyRecovered := map[Kind][]int64{}
	rescue := map[Kind]bool{}
	rescueIDs := map[Kind][]int64{}
	for rows.Next() {
		var id int64
		var kind string
		var unflushed int
		if err := rows.Scan(&id, &kind, &unflushed); err != nil {
			rows.Close()
			return imported, rescued, err
		}
		if keep[id] {
			// Already recovered — not by the rescue below, but by its own
			// per-memory file, found and read during the directory scan
			// above. That happens when exportMemoryFile succeeded for this
			// id but the kind's digest export failed right after (the usual
			// shape: a read-only top-level memories/ directory still lets a
			// write land inside an existing kind subdirectory). The memory
			// was never actually lost, only its unflushed flag and this
			// count do not know that yet. The clearing update is deferred
			// until this kind's own digest export below actually succeeds —
			// not run eagerly here, because the vault can still be unwritable
			// for the digest even though the per-memory file landed, and
			// clearing the flag before that write is confirmed would call a
			// memory durable that the next crash could still lose. It also
			// cannot run inside this loop regardless: rows is still open, and
			// a write on the same connection while it iterates is how this
			// deadlocked the first time.
			if unflushed == 1 {
				rescued++
				alreadyRecovered[Kind(kind)] = append(alreadyRecovered[Kind(kind)], id)
			}
			continue
		}
		// Absent from the file, and we know why: the export that should have
		// written it failed, and nothing since has succeeded. This is the one
		// case where the file's silence is not the user deleting a line, and
		// treating it as one is how the command documented as safe — delete the
		// index, reindex, lose nothing — became the command that destroys the
		// memory. Put it in the vault instead of reaping it.
		//
		// Ahead of the authoritative gate, not behind it, because the commonest
		// way to strand a memory is a vault that was unwritable for the very
		// first export of its kind — which leaves no file at all, and a kind
		// with no file is exactly what that gate excludes. Behind the gate,
		// those memories were never rescued and never lost the flag, so doctor
		// reported the same at-risk rows after every repair.
		if unflushed == 1 {
			rescue[Kind(kind)] = true
			rescueIDs[Kind(kind)] = append(rescueIDs[Kind(kind)], id)
			rescued++
			continue
		}
		if !authoritative[Kind(kind)] {
			continue
		}
		orphans = append(orphans, id)
	}
	rows.Close()
	for _, id := range orphans {
		// forgetRow, not Forget. Forget flushes, and that takes the kind lock
		// this function is already holding — so calling it here wedges the
		// process on the first line the user deleted. It would also rewrite the
		// very file being read as the source of truth.
		if _, err := forgetRow(db, id); err != nil {
			return imported, rescued, err
		}
		edits.Forgotten = append(edits.Forgotten, id)
	}

	// The rescue itself, after the reaping, so the file this writes reflects
	// every decision above. ExportKind rather than flush: this function already
	// holds every kind's lock, and flush would take them again.
	//
	// Each rescued row's own file first, the digest second — the same order
	// Store keeps, and for the same reason: the per-memory file is truth under
	// A2, and ExportKind only ever renders from the database, so a rescue that
	// wrote the digest without also writing memories/<kind>/<uid>.md would
	// leave the memory real in the vault's one generated summary but with
	// nothing for the next readKindFiles to call truth — recoverable only by
	// the digest line surviving untouched until a later index happens to
	// migrate it, not actually written yet.
	for kind, ids := range rescueIDs {
		for _, id := range ids {
			if err := exportMemoryFile(db, kind, id); err != nil {
				return imported, 0, fmt.Errorf("memories are in the cache that the vault still will not take: %w", err)
			}
		}
	}
	for kind := range rescue {
		if err := ExportKind(db, dir, kind); err != nil {
			// Still not writable. The rows keep their flag and stay in the
			// cache, which is the whole point of the flag — nothing is lost,
			// and the next run tries again.
			return imported, 0, fmt.Errorf("memories are in the cache that the vault still will not take: %w", err)
		}
		db.Exec("UPDATE memories SET unflushed = 0 WHERE kind = ? AND quarantined = 0 AND unflushed = 1", string(kind))
		dropStamp(db, kind)
	}

	// `logos index` always wins over a hand edit to a kind's generated digest
	// — unlike a plain Store, which refuses rather than overwrite one (see
	// flushLocked). Regenerating every kind here, unconditionally, through
	// ExportKind rather than flush, is what gives Import that authority: it
	// never goes through the guard at all. Harmless on the common run where
	// nothing changed — ExportKind on an already-correct file writes the same
	// bytes back — and it is what turns a freshly migrated kind's flat file
	// into the generated-digest form for the first time. rescue's kinds are
	// skipped: the loop above already regenerated and stamped them.
	for _, kind := range kinds {
		if rescue[kind] {
			continue
		}
		if err := ExportKind(db, dir, kind); err != nil {
			// Same accounting as the rescue loop above: a kind whose digest
			// still will not write keeps every flag it walked in with,
			// alreadyRecovered included, and 0 is reported rather than a
			// rescued count this call could not actually confirm.
			return imported, 0, fmt.Errorf("could not refresh the %s digest: %w", kind, err)
		}
		// This kind's digest write just succeeded, so any id this call found
		// already durable in its own per-memory file — but still marked
		// unflushed from an earlier failed digest write — truly is caught up
		// now. Cleared here rather than eagerly in the rows loop above,
		// because clearing the flag only means something once this write is
		// known to have landed.
		for _, id := range alreadyRecovered[kind] {
			db.Exec("UPDATE memories SET unflushed = 0 WHERE id = ?", id)
		}
		if raw, err := os.ReadFile(filepath.Join(dir, Dir, string(kind)+".md")); err == nil {
			stamp(db, kind, sha256.Sum256(raw))
		} else {
			dropStamp(db, kind)
		}
	}
	return imported, rescued, nil
}

// upsert writes one parsed memory back, preserving its id so that references in
// memory_log, and anything the user has cited, keep pointing at the same fact.
// created says the line had no id and became a new memory, which `logos index`
// announces: it is also what a line retyped without its comment turns into.
// heldLine is a hand-typed line that can be neither its text's memory nor a new
// one. It is left out of memory rather than failing the pass: as an error it
// stopped every store's reconcile, so one stray line kept every agent from
// remembering anything until somebody edited the file.
type heldLine string

func (h heldLine) Error() string { return string(h) }

func upsert(db *sql.DB, p *provider.Provider, embedModel string, m Memory) (id int64, created bool, err error) {
	var vec []byte
	if p != nil {
		if vecs, err := p.Embed(embedModel, []string{m.Text}); err == nil && len(vecs) == 1 {
			vec = floatsToBlob(vecs[0])
		}
	}

	if m.ID > 0 {
		res, err := db.Exec(
			`UPDATE memories SET text=?, kind=?, salience=?, confidence=?, project=?,
			 source=?, agent=?, created=?, last_used=?, uses=?, fingerprint=?, vec=COALESCE(?, vec), pin=?, auto_accepted=?, commit_sha=?
			 WHERE id = ?`,
			m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project, m.Source,
			m.Agent, m.Created, m.LastUsed, m.Uses, fingerprint(m.Text), vec, m.Pin, m.AutoAccepted, m.Commit, m.ID)
		if err != nil {
			return 0, false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return m.ID, false, nil
		}
		// The row is gone — the cache was wiped. Restore it under its old id.
		// uid is whatever the vault line said (parsed by applyMeta), never
		// recomputed here: a line written before A1 has none, and inventing one
		// on a cache rebuild would mean the same vault produces a different uid
		// depending on whether the index happened to survive. Backfilling the
		// pre-A1 rows is A4's job, not this restore path's.
		if err := checkUIDCollision(db, m.Uid, m.Text); err != nil {
			return 0, false, err
		}
		_, err = db.Exec(
			`INSERT INTO memories (id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, vec, fingerprint, pin, auto_accepted, commit_sha, uid)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			m.ID, m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project,
			m.Source, m.Agent, m.Created, m.LastUsed, m.Uses, vec, fingerprint(m.Text), m.Pin, m.AutoAccepted, m.Commit, m.Uid)
		// No event. Restoring a row the rebuild deleted is not a creation, and
		// logging one stamped with time.Now() is how `logos memory log` came to
		// report every fact you had ever learned as learned today. The real
		// beginning is either already in memories/log.md or synthesised from the
		// memory's own `created` by backfillCreations — both of them honest dates.
		return m.ID, false, err
	}

	// No id in the comment, but this text is already a memory: the same bare
	// line, read again. Import adopts a typed line without writing its comment
	// back, so every later pass sees it like this, and inserting it a second
	// time hit the unique fingerprint — failing every `logos index`, and every
	// store through its reconcile, until something rewrote the file.
	var owner int64
	var kind string
	var superseded, quarantined int
	if db.QueryRow("SELECT id, kind, superseded, quarantined FROM memories WHERE fingerprint = ?",
		fingerprint(m.Text)).Scan(&owner, &kind, &superseded, &quarantined) == nil {
		switch {
		case quarantined == 1:
			return 0, false, heldLine(fmt.Sprintf("a line in memories/%s.md is the text of memory #%d, which is awaiting review — accept it with `logos review`, or delete the line", m.Kind, owner))
		case superseded == 1:
			return 0, false, heldLine(fmt.Sprintf("a line in memories/%s.md is the text of memory #%d, which was superseded — reword the line, or delete it", m.Kind, owner))
		}
		// Found in another kind's file: the line was moved there, and the move
		// is kept, as it is for a line moved with its comment. Left as the old
		// kind, the next export of that kind wrote the line back where it was.
		if kind != string(m.Kind) {
			if _, err := db.Exec("UPDATE memories SET kind = ? WHERE id = ?", string(m.Kind), owner); err != nil {
				return 0, false, err
			}
		}
		return owner, false, nil
	}

	// No id in the comment: a line somebody typed by hand. Give it one — a fresh
	// one, never a number some earlier memory has already been known by. See
	// nextID.
	//
	// Retried for the same reason insertMemory is: nextID reads MAX(id), and a
	// single `logos index` run can reach this for several hand-typed lines in
	// a row — one per kind, all under the same import — so two of them can
	// land here holding the same number before the first's INSERT commits.
	// There the collision would silently drop a memory; here it is a primary
	// key error that fails the import that triggered it. Taking the next
	// number is the whole fix.
	// A brand-new hand-typed line carries no project/git context — one kind
	// file holds memories from every project, so there is no single dir to
	// resolve a remote from. Always the no-remote marker, never a guess; see
	// computeUID. If the hand-typed line somehow already carries a valid uid
	// field (pasted from elsewhere), that stated value is kept instead.
	if m.Uid == "" {
		m.Uid = computeUID(m.Kind, m.Text, "")
	}
	if err := checkUIDCollision(db, m.Uid, m.Text); err != nil {
		return 0, false, err
	}
	var lastErr error
	for range idAttempts {
		id := nextID(db)
		_, err := db.Exec(
			`INSERT INTO memories (id, text, kind, salience, confidence, project, source, agent, created, last_used, uses, vec, fingerprint, pin, auto_accepted, commit_sha, uid)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project, m.Source,
			m.Agent, m.Created, m.LastUsed, m.Uses, vec, fingerprint(m.Text), m.Pin, m.AutoAccepted, m.Commit, m.Uid)
		if err == nil {
			logEvent(db, id, EvCreated, m.Text, 0)
			return id, true, nil
		}
		lastErr = err
		// Anything but a taken id is a real failure and must not be retried.
		if !strings.Contains(err.Error(), "UNIQUE constraint failed: memories.id") {
			return 0, false, err
		}
	}
	return 0, false, lastErr
}

// parseKind reads back what renderKind wrote.
//
// Tolerant on purpose: a hand-written line with no metadata comment is a valid
// memory. Telling someone their file is truth and then rejecting what they type
// into it would be a lie.
func parseKind(kind Kind, raw string) []Memory {
	var out []Memory
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		body := strings.TrimSpace(line[2:])

		m := Memory{Kind: kind, Salience: 0.5, Confidence: 0.7, Created: time.Now().Unix()}
		// LastIndex, not Index, for the reason logstore.go's parser documents:
		// the text is free-form and may itself quote "<!--", while logos's own
		// bookkeeping comment is always the last thing on the line. Splitting
		// at the first one hands the user's text to applyMeta and loses it.
		if i := strings.LastIndex(body, "<!--"); i >= 0 {
			meta := body[i:]
			body = strings.TrimSpace(body[:i])
			applyMeta(&m, meta)
		}
		if body == "" {
			continue
		}
		m.Text = body
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

// readKindFiles returns one kind's memories as parseKind-ready raw bullet
// lines, migrating any still in the old one-file-per-kind format as it reads.
//
// existed distinguishes "nothing here" from "the user deleted everything":
// false means neither a per-memory directory nor a flat file was found, which
// importInto must never read as permission to forget a kind it simply hasn't
// migrated yet.
//
// migrated counts memories moved to their own file this call. Resumability
// comes from never trusting the directory's existence as a migration flag —
// a crash partway through would leave the directory there but incomplete,
// and a flag would make the next run treat the kind as fully migrated and
// quietly drop whatever legacy lines never got written. Instead, have is
// rebuilt from the directory's actual current contents on every call, and the
// flat file is re-parsed every time; a line whose uid is already represented
// by a file is skipped — correctly handling both "already migrated, nothing
// to do" and "someone hand-edited an existing line in what is now a generated
// digest" (that edit is not adopted here; Import's final unconditional
// ExportKind pass overwrites the digest back to truth instead). Migrating
// never deletes or truncates the flat file, only reads it, so a crash mid-
// migration leaves it exactly as readable as it was before the run started.
func readKindFiles(dir string, kind Kind) (raw string, existed bool, migrated int, err error) {
	kindDir := filepath.Join(dir, Dir, string(kind))
	have := map[string]bool{}
	var b strings.Builder

	entries, derr := os.ReadDir(kindDir)
	switch {
	case derr == nil:
		existed = true
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			content, rerr := os.ReadFile(filepath.Join(kindDir, e.Name()))
			if rerr != nil {
				// Unreadable, not missing — treated the same as linesElsewhere
				// treats one: costs this memory its place in the digest, never
				// a reason to call the whole kind damaged over one bad file.
				continue
			}
			have[strings.TrimSuffix(e.Name(), ".md")] = true
			b.Write(content)
			if len(content) > 0 && content[len(content)-1] != '\n' {
				b.WriteByte('\n')
			}
		}
	case !os.IsNotExist(derr):
		return "", false, 0, derr
	}

	flat, ferr := os.ReadFile(filepath.Join(dir, Dir, string(kind)+".md"))
	if ferr != nil {
		if os.IsNotExist(ferr) {
			return b.String(), existed, 0, nil
		}
		return "", existed, 0, ferr
	}
	existed = true
	// Truncation is checked before the banner: a torn write is damage
	// whether or not the kind has crossed over to per-memory files, and the
	// banner sits near the top of a generated digest, so a tear partway
	// through the record list would otherwise still contain it and this
	// function would return clean before ever looking at the missing tail.
	if looksTruncated(string(flat)) {
		// Reported, not acted on: a torn flat file might be hiding legacy
		// lines a hand-typed-line branch has never seen. Letting migration
		// run anyway risks writing a truncated record as somebody's memory.
		return "", existed, 0, kindDamaged(string(kind) + ".md ends mid-record")
	}
	if strings.Contains(string(flat), generatedDigestBanner) {
		// This flat file is logos's own generated output, not a legacy
		// format waiting to be migrated — renderKind only ever writes this
		// banner after a kind has its own per-memory files. Reading it as a
		// source here would undo a real deletion: a memory whose file the
		// user removed by hand is still named on this stale digest line
		// until the next regen, and treating that leftover line as
		// "not yet migrated" would recreate the file Import is supposed to
		// let go. Once a kind has crossed over, only memories/<kind>/ counts.
		return b.String(), existed, 0, nil
	}
	for _, m := range parseKind(kind, string(flat)) {
		if m.Uid == "" {
			// Pre-A1 line: never had a uid, so give it the same one upsert's
			// hand-typed-line branch would compute for identical text. Same
			// path, so a line already migrated by one route is recognized by
			// the other instead of being written twice under two different
			// names.
			m.Uid = computeUID(kind, m.Text, noRemoteMarker)
		}
		if have[m.Uid] {
			continue
		}
		if err := writeMemoryFile(dir, kind, m); err != nil {
			return "", existed, migrated, err
		}
		have[m.Uid] = true
		b.WriteString(renderLine(m))
		migrated++
	}
	return b.String(), existed, migrated, nil
}

// kindDamaged is a kind's flat file ending mid-record, caught during
// migration before any line of it is trusted. Reported by importInto as a
// reason it refuses to act on that kind — never silently.
type kindDamaged string

func (k kindDamaged) Error() string { return string(k) }

// linesElsewhere is the fingerprint of every line in the other kinds' files.
// An unreadable file adds nothing, which can only cost a moved line its
// memory's metadata — never keep a deleted one.
func linesElsewhere(dir string, kind Kind) map[string]bool {
	out := map[string]bool{}
	for _, k := range kinds {
		if k == kind {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, Dir, string(k)+".md"))
		if err != nil {
			continue
		}
		for _, m := range parseKind(k, string(raw)) {
			out[fingerprint(m.Text)] = true
		}
	}
	return out
}

// looksTruncated reports that a store file was cut off rather than edited.
//
// The distinction is the whole difficulty here, because the two look identical
// from a count: deleting a line is the documented way to forget something, and
// it leaves the frontmatter's count stale by exactly the amount a torn write
// would. Enforcing the count breaks the feature the format exists to provide.
//
// What a hand edit never does is leave the file ending in the middle of a
// record. So that is what is checked: unterminated frontmatter, or a final
// bullet whose metadata comment was opened and never closed. A write
// interrupted at an arbitrary byte lands mid-line almost every time.
//
// A tear that happens to land exactly on a line boundary is indistinguishable
// from a deliberate deletion and is accepted. That is a real gap, and it is the
// narrower one: logos's own writes go through vault.WriteAtomic, which replaces
// the file by rename and cannot tear, so this only arises when something else —
// a sync client, a crashed editor — is writing the file.
func looksTruncated(raw string) bool {
	trimmed := strings.TrimRight(raw, " \t\r\n")
	if trimmed == "" {
		return false
	}
	// Frontmatter opened and never closed.
	if rest, ok := strings.CutPrefix(trimmed, "---"); ok && !strings.Contains(rest, "\n---") {
		return true
	}
	lines := strings.Split(trimmed, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	// A record whose bookkeeping comment was opened and never closed.
	return strings.HasPrefix(last, "- ") &&
		strings.Contains(last, "<!--") && !strings.Contains(last, "-->")
}

func applyMeta(m *Memory, meta string) {
	meta = strings.TrimSuffix(strings.TrimPrefix(meta, "<!--"), "-->")
	for _, field := range strings.Fields(meta) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			m.ID, _ = strconv.ParseInt(value, 10, 64)
		case "conf":
			m.Confidence, _ = strconv.ParseFloat(value, 64)
		case "sal":
			m.Salience, _ = strconv.ParseFloat(value, 64)
		case "src":
			if value != "-" {
				m.Source = value
			}
		case "uses":
			n, _ := strconv.Atoi(value)
			m.Uses = n
		case "used":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				m.LastUsed = t.Unix()
			}
		case "kind":
			// Only the review queue writes this: memories/<kind>.md takes the
			// kind from its filename, but pending.md holds every kind at once.
			// Validated rather than trusted, so a typo in a hand-edited line
			// cannot invent a kind nothing else in the system knows about.
			for _, k := range kinds {
				if Kind(value) == k {
					m.Kind = k
				}
			}
		case "project":
			// Escaped as log.md escapes it. Written bare, "billing api" came
			// back as "billing" at the space, and the reconcile before the next
			// write moved the memory into that other project. A bare name from
			// an older file unescapes to itself.
			m.Project = unLogField(value)
		case "agent":
			m.Agent = value
		case "turn":
			m.Turn = unLogField(value)
		case "contests":
			m.Contests, _ = strconv.ParseInt(value, 10, 64)
		case "accepted":
			// Only auto@<time> means anything. A hand edit to anything else,
			// or deleting the field, is the user saying a person accepted it.
			if at, ok := strings.CutPrefix(value, "auto@"); ok {
				if t, err := time.Parse(time.RFC3339, at); err == nil {
					m.AutoAccepted = t.Unix()
				}
			}
		case "commit":
			// Vault text, which anyone sharing the vault wrote, and later
			// handed to git. Hex only; anything else is as if absent.
			if isSha(value) {
				m.Commit = value
			}
		case "uid":
			// The vault is truth: a hand edit to this line must not change what
			// the line asserts its uid is. Validated the same way commit is —
			// anything malformed is as if absent, leaving the caller to compute
			// a fresh one rather than trust a corrupted field.
			if isUID(value) {
				m.Uid = value
			}
		case "pin":
			// Tolerant of a hand-typed value out of range: a person editing the
			// file might type anything, and refusing to import over it would
			// contradict "the file is the record, not the database".
			if n, err := strconv.Atoi(value); err == nil && n >= PinNone && n <= PinNever {
				m.Pin = n
			}
		case "created":
			// RFC3339 now; the bare date is what older files carry. Recording
			// only the date collapsed two memories written hours apart onto the
			// same timestamp, and supersession decides by which came later.
			for _, layout := range []string{time.RFC3339, "2006-01-02"} {
				if t, err := time.Parse(layout, value); err == nil {
					m.Created = t.Unix()
					break
				}
			}
		}
	}
}

// commitField is the vault line's commit= field, or nothing: a memory with no
// recorded commit is written exactly as it was before the field existed.
func commitField(commit string) string {
	if !isSha(commit) {
		return ""
	}
	return " commit=" + commit
}

// uidField mirrors commitField: omitted when empty, so a memory stored before
// A1 (uid="") round-trips through the vault exactly as it always has.
func uidField(uid string) string {
	if !isUID(uid) {
		return ""
	}
	return " uid=" + uid
}

// isUID is the shape computeUID produces: exactly uidLen lowercase hex
// characters. The field is hand-editable like commit, so a stray or
// truncated value is dropped rather than trusted.
func isUID(s string) bool {
	if len(s) != uidLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isSha is gitstate's rule for a recorded commit, applied where the vault is
// read: the field is hand-editable and ends up as an argument to git.
func isSha(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
