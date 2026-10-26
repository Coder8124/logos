package index

// RebuildPaths names, for every table index.db can hold, what puts its rows
// back after .logos is deleted — or why losing them is safe. Invariant 1 broke
// four times (memories, working notes, checkpoints, the review queue) and every
// time the same way: a writer added a table, nothing rebuilt it from markdown,
// and the loss was silent until someone deleted the index. Listing every table
// here makes the question "what rebuilds this?" one a new table cannot skip:
// TestEveryIndexTableNamesWhatRebuildsItFromTheVault fails on any CREATE TABLE
// this map does not name.
//
// "cache:" entries are tables whose loss costs time or one missed detection,
// never a record. Anything else must name the vault file a Sync pass reads.
var RebuildPaths = map[string]string{
	"notes":     "every .md in the vault, walked by Sync",
	"aliases":   "the aliases: frontmatter of each note, read by Sync",
	"edges":     "the wikilinks and typed links in each note's body, read by Sync",
	"notes_fts": "the notes table, refilled by Sync and by migrate's backfill",

	"embeddings": "cache: re-embedded from the notes by EmbedPending",
	"meta":       "cache: last_sync only; the next Sync writes it again",

	// A superseded memory is not rebuilt: its file goes when it is replaced,
	// and the replacement's supersedes= and log.md are what remember it.
	"memories":   "memories/<kind>/ files, read by SyncMemories (quarantined rows from the review queue's files, by SyncPending)",
	"memory_log": "memories/log.md, read by SyncLog",
	// Losing a stamp only means the next write has no hash to compare against,
	// so one hand edit made in that gap goes undetected — see their comments in
	// memory.Schema.
	"digest_stamps":      "cache: the hand-edit guard's last-written hash per digest",
	"memory_file_stamps": "cache: the hand-edit guard's last-written hash per memory file",

	"commitments":    "loops.md, read by SyncLoops",
	"dream_insights": "dream-insights.md, read by SyncInsights",

	// Open sessions only. An ended session's rows are history its checkpoint
	// file already holds, and every reader of these tables filters ended = 0.
	// The notes come back with their agent and time; the session around them
	// is opened afresh, so its id, start time and task are new.
	"sessions":      "each open session's uncommitted.md, read by SyncNotes; an ended one lives in its checkpoint under sessions/",
	"session_notes": "each open session's uncommitted.md, read by SyncNotes",

	// The three deadend tables are derived from checkpoints already in the
	// vault, and ruling_shown is deliberately per-session: forgetting it means
	// a warning is shown once more, never that a ruling is lost.
	"ruling_vectors": "cache: embeddings of failed entries, recomputed on the next before_you_try",
	"ruling_files":   "cache: the files each failed entry names, re-read from sessions/ checkpoints by its next lookup",
	"ruling_shown":   "cache: which rulings this session already showed",

	// The one table that is cache-only on purpose: a read cursor. Losing it
	// replays from the start, which is the safe direction.
	"replay_state": "cache: replay's read cursor",
}
