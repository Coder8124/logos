package ingest

import (
	"path"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/transcript"
)

// Harvest derives a candidate from a session using only mechanically-checkable
// facts: which commands ran and whether they exited non-zero, which files were
// edited, how big the session was.
//
// verified and failed stay empty. Deriving them needs judgement, and a
// fabricated failed entry is worse than an absent one — the next agent treats a
// checkpoint's failed list as a paid-for ruling and will not re-try what it
// names. B2 (local model) and B3 (the calling agent) fill those fields under a
// citation filter; a harvest does not guess at them.
func Harvest(s *transcript.Session) Candidate {
	c := Candidate{
		Harness:   s.Harness,
		SessionID: s.ID,
		Source:    s.Path,
		Hash:      s.Hash,
		Project:   s.Project,
		Tier:      TierHarvest,
		Status:    StatusPending,
		Started:   s.Started,
		Ended:     s.Ended,
		Turns:     len(s.Turns),
		Skipped:   s.Skipped,
	}

	seenCmd := map[string]bool{}
	seenFile := map[string]bool{}
	for _, t := range s.Turns {
		if t.Role != "tool" || strings.TrimSpace(t.Input) == "" {
			continue
		}
		inv := collapse(t.Input)
		switch {
		case isShellTool(t.Tool):
			line := inv
			switch t.Status {
			case "error":
				line += " — failed"
			case "ok":
				line += " — ok"
			}
			if !seenCmd[line] {
				seenCmd[line] = true
				c.Commands = append(c.Commands, line)
			}
			for _, f := range filesInCommand(inv) {
				if !seenFile[f] {
					seenFile[f] = true
					c.Files = append(c.Files, f)
				}
			}
		case isFileTool(t.Tool), looksLikePath(inv):
			f := inv
			if !seenFile[f] {
				seenFile[f] = true
				c.Files = append(c.Files, f)
			}
		}
	}
	sort.Strings(c.Files)
	return c
}

func isShellTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "exec", "run_command", "run_terminal_cmd", "terminal", "sh":
		return true
	}
	return false
}

func isFileTool(name string) bool {
	switch strings.ToLower(name) {
	case "edit", "write", "multiedit", "notebookedit", "str_replace_editor", "apply_patch", "create_file", "update_file":
		return true
	}
	return false
}

// looksLikePath is the conservative fallback for a tool this code does not know
// by name: a single token that has a path separator or a file extension and no
// shell metacharacters.
func looksLikePath(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t|&;<>$`") {
		return false
	}
	return strings.Contains(s, "/") || (strings.Contains(s, ".") && !strings.HasPrefix(s, "."))
}

// filesInCommand pulls path-shaped arguments out of a shell command so an
// edit done with `sed`/`tee`/redirection still shows up under Files. Best
// effort: it is a harvest, and review sees the command line too.
func filesInCommand(cmd string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '>' || r == '<' || r == '|'
	}) {
		tok = strings.TrimSpace(tok)
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if !strings.Contains(tok, "/") && !hasSourceExt(tok) {
			continue
		}
		if strings.ContainsAny(tok, "*?$`") {
			continue
		}
		out = append(out, path.Clean(tok))
	}
	return out
}

func hasSourceExt(tok string) bool {
	for _, ext := range []string{".go", ".rs", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".c", ".h", ".cpp", ".md", ".yaml", ".yml", ".json", ".toml", ".sh", ".sql"} {
		if strings.HasSuffix(tok, ext) {
			return true
		}
	}
	return false
}

// collapse flattens whitespace so a multi-line heredoc command lands as one
// readable bullet rather than breaking the markdown list.
func collapse(s string) string {
	f := strings.Fields(s)
	j := strings.Join(f, " ")
	if len(j) > 300 {
		j = j[:297] + "…"
	}
	return j
}
