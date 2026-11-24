package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Locate says which conversation a transcript path names and the folder it ran
// in, from the file's name and its first lines alone, without parsing it.
//
// It is for a caller that must decide whether a session may be read before it
// is read (the exclusion list). Each host spells both differently: a Claude
// Code file is named for its session and says its cwd on nearly every line, a
// Codex rollout carries the id as the tail of its name and the cwd once, in
// the session_meta payload, and a Cursor chat is a row in a database shared
// with every other chat, addressed <storage file>#<composer id>, with no
// folder on the record at all. Asking Claude Code's question of all three
// read a Codex session's folder as unknown and a Cursor chat's id as the name
// of the whole database.
//
// cwd is "" when the session does not say; the caller treats that as
// unknown, never as "somewhere harmless".
func Locate(path string) (id, cwd string) { return LocateID(path), LocateCwd(path) }

// LocateID is the session id a path names, from its name alone: nothing is
// opened.
func LocateID(path string) string {
	if _, composer, ok := strings.Cut(path, cursorPathSep); ok {
		return composer
	}
	base := filepath.Base(path)
	if isCodexRollout(base) {
		return codexIDFromName(base)
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// LocateCwd is the folder a session ran in, from the first lines of its file
// only, or "" when it does not say (always, for a Cursor chat).
func LocateCwd(path string) string {
	if strings.Contains(path, cursorPathSep) {
		return ""
	}
	if isCodexRollout(filepath.Base(path)) {
		return codexCwd(path)
	}
	return FirstCwd(path)
}

func isCodexRollout(base string) bool {
	return strings.HasPrefix(base, "rollout-") && strings.HasSuffix(base, ".jsonl")
}

// codexCwd is the working directory a Codex rollout's session_meta line
// records, read from its first lines only. Bounded like FirstCwd, so a rollout
// with no meta near its top costs a short read and the answer "unknown".
func codexCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for i := 0; i < 64 && sc.Scan(); i++ {
		var ln codexLine
		if json.Unmarshal(sc.Bytes(), &ln) != nil || ln.Type != "session_meta" {
			continue
		}
		var p codexPayload
		if json.Unmarshal(ln.Payload, &p) == nil {
			return p.Cwd
		}
		return ""
	}
	return ""
}
