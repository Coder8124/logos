package setup

import (
	"os"
	"strings"
)

// readCodexServers reads back the servers Codex has registered in its
// config.toml, in the same shape readServerBlock returns for the JSON hosts.
//
// #90: Codex is the one host in this package's list whose config is TOML, and
// while nothing could read it, a Codex still wired to a vault the machine has
// moved off reported nothing — the split doctor exists to catch was invisible
// on that host. The pin lives in the same place for Codex as for everybody
// else; only the syntax around it differs.
//
// Scanned line by line rather than parsed with a TOML library, for the reason
// codexHasEntry already gives: this file and the [mcp_servers.<name>] tables
// inside it are the only TOML logos reads, and a dependency that can parse all
// of TOML is a large answer to a small question. That limit is deliberate and
// has an edge: inline tables (mcp_servers = { logos = { … } }) and multi-line
// arrays are not understood. Codex's own `codex mcp add` writes the expanded
// form this reads, so the case that matters is covered, and anything else
// reads as "nothing to say" — the caller keeps the machine pointer, which is
// the same answer an unreadable JSON config gives.
func readCodexServers(path string) ([]Registration, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Insertion-ordered, so two servers never swap places between two runs of
	// the same command. A caller printing this to a user is reading a file.
	order := []string{}
	byName := map[string]*Registration{}
	args := map[string][]string{}
	server := func(name string) *Registration {
		if r, ok := byName[name]; ok {
			return r
		}
		r := &Registration{Name: name}
		byName[name], order = r, append(order, name)
		return r
	}

	name, env := "", false
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if i := strings.Index(t, "#"); i == 0 {
			continue
		}
		if strings.HasPrefix(t, "[") {
			name, env = codexTable(t)
			continue
		}
		// Only inside an [mcp_servers.*] table. config.toml holds the whole of
		// Codex's configuration — model, approval policy, the user's own
		// tables — and a stray `command =` out there is not a server.
		if name == "" {
			continue
		}
		key, val, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		r := server(name)
		switch {
		case env:
			if key == "LOGOS_VAULT" {
				r.Vault = tomlUnquote(val)
			}
		case key == "command":
			r.Command = tomlUnquote(val)
		case key == "args":
			args[name] = tomlArray(val)
		}
	}

	out := make([]Registration, 0, len(order))
	for _, n := range order {
		r := byName[n]
		r.Command = strings.TrimSpace(strings.Join(append([]string{r.Command}, args[n]...), " "))
		out = append(out, *r)
	}
	return out, nil
}

// codexTable reads a table header, returning the server it belongs to and
// whether it is that server's env table. Anything that is not under
// mcp_servers is "" — not a server, so not something to read keys out of.
func codexTable(line string) (name string, env bool) {
	t := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	rest, ok := strings.CutPrefix(strings.TrimSpace(t), "mcp_servers.")
	if !ok {
		return "", false
	}
	if base, isEnv := strings.CutSuffix(rest, ".env"); isEnv {
		return tomlUnquote(base), true
	}
	return tomlUnquote(rest), false
}

// tomlArray reads a single-line array of basic strings, which is the shape
// `codex mcp add` writes args in.
func tomlArray(v string) []string {
	v, ok := strings.CutPrefix(v, "[")
	if !ok {
		return nil
	}
	v, ok = strings.CutSuffix(strings.TrimSpace(v), "]")
	if !ok {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, tomlUnquote(part))
		}
	}
	return out
}

// tomlUnquote is the inverse of tomlString, and covers what tomlString can
// produce: a basic string escaping backslash and quote. A bare key, which a
// hand-edited file may carry, is returned as it stands.
func tomlUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s)
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1] // literal string: no escapes by definition
	}
	return s
}
