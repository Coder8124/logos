package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookProblem is one host whose session-start hook will not do its job, read
// off the file the way that host reads it.
type HookProblem struct {
	Host   string
	Path   string
	Detail string
}

// HookProblems reads the hooks files logos writes and names every way one of
// them fails at session start. Setup fixes each of these, but nobody re-runs
// setup after an upgrade: a Codex hooks file an earlier logos broke stayed
// broken through every release that knew how to fix it, and doctor said the
// hosts were fine, because it only asked whether they were installed.
func HookProblems(hosts []Host) []HookProblem {
	var out []HookProblem
	for _, h := range hosts {
		if h.Hooks == nil || h.Detect == nil || !h.Detect() {
			continue
		}
		host := strings.ToLower(h.Name)
		path := hooksPathFor(h.Name)
		if path == "" {
			continue
		}
		if d := hookFileProblem(path, host); d != "" {
			out = append(out, HookProblem{Host: h.Name, Path: path, Detail: d})
		}
	}
	return out
}

// hookFileProblem is "" for a hooks file the host can read whose logos hook,
// if it has one, will run. A file with no logos hook is not a problem here:
// the user may have chosen MCP alone, and setup reports that case itself.
func hookFileProblem(path, host string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ""
	}
	f, err := loadHookFile(path)
	if err != nil {
		return err.Error()
	}
	if host == "codex" {
		if _, has := f.rest["version"]; has {
			return "Codex refuses this file (\"unknown field `version`\") and runs none of the hooks in it"
		}
	}
	var cmds []string
	flat, _ := flatEntries(path, &f)
	for _, e := range flat {
		if isLogosHook(e.command(), host) {
			if host == "codex" {
				return "logos's hook is in Cursor's shape, which Codex never runs"
			}
			cmds = append(cmds, e.command())
		}
	}
	if host == "codex" {
		groups, _ := codexGroups(path, &f)
		for _, g := range groups {
			handlers, _ := groupHandlers(path, g)
			for _, h := range handlers {
				if isLogosHook(h.command(), host) {
					cmds = append(cmds, h.command())
				}
			}
		}
	}
	for _, cmd := range cmds {
		if strings.Contains(cmd, " mcp serve hook ") {
			return "logos's hook starts an MCP server instead of the hook, so no session opens with a handoff"
		}
		if bin := hookBinary(cmd); bin != "" {
			if _, err := os.Stat(bin); os.IsNotExist(err) {
				return fmt.Sprintf("logos's hook runs %s, which no longer exists", bin)
			}
		}
	}
	return ""
}

// hookBinary is the absolute path a hook command starts with, unquoted, or ""
// when it starts with something resolved at run time (npx, a bare logos).
func hookBinary(cmd string) string {
	var bin string
	if strings.HasPrefix(cmd, "'") {
		end := strings.Index(cmd[1:], "'")
		if end < 0 {
			return ""
		}
		bin = cmd[1 : end+1]
	} else {
		bin, _, _ = strings.Cut(cmd, " ")
	}
	if !filepath.IsAbs(bin) {
		return ""
	}
	return bin
}
