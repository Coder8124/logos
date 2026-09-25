package setup

import (
	"fmt"
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
		t := strings.TrimSpace(stripComment(line))
		if t == "" {
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
			if r.Server.Env == nil {
				r.Server.Env = map[string]string{}
			}
			r.Server.Env[key] = tomlUnquote(val)
			if key == "LOGOS_VAULT" {
				r.Vault = tomlUnquote(val)
			}
		case key == "command":
			r.Command = tomlUnquote(val)
			r.Server.Bin = r.Command
		case key == "args":
			args[name] = tomlArray(val)
		}
	}

	out := make([]Registration, 0, len(order))
	for _, n := range order {
		r := byName[n]
		r.Server.Args = args[n]
		r.Command = strings.TrimSpace(strings.Join(append([]string{r.Command}, args[n]...), " "))
		out = append(out, *r)
	}
	return out, nil
}

// mergeTOMLServer writes logos's [mcp_servers.logos] table into a TOML config,
// replacing the one already there and leaving every other line as it was.
//
// Line-based, like the reader, so it is held to the reader's limits: a file
// that declares servers as an inline table, dotted keys or a bare
// [mcp_servers] table is refused rather than edited, because appending a
// second definition of the same table makes a file the host will not load at
// all — every other server in it gone with ours. What was written is read back,
// so a registration nobody can see is a failure, not a success.
func mergeTOMLServer(path string, s Server) (Outcome, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Failed, err
	}
	if form := unsupportedServerForm(string(raw)); form != "" {
		return Failed, fmt.Errorf("%s declares its MCP servers as %s, which logos does not edit, so it was left alone — add the block `logos setup --print-config --format toml` prints by hand", path, form)
	}
	kept, had := dropServerTables(string(raw), Name)
	out := strings.TrimRight(kept, "\n")
	if out != "" {
		out += "\n\n"
	}
	if err := writeHostFile(path, []byte(out+renderConfigTOML(s))); err != nil {
		return Failed, err
	}
	regs, err := readCodexServers(path)
	if err != nil {
		return Failed, fmt.Errorf("wrote %s but could not read it back: %w", path, err)
	}
	for _, r := range regs {
		if r.Name == Name && isLogosServer(r.Command) {
			if had {
				return Updated, nil
			}
			return Registered, nil
		}
	}
	return Failed, fmt.Errorf("wrote %s but no logos server reads back from it", path)
}

// removeTOMLServer takes out the table called name when it runs logos.
func removeTOMLServer(path, name string) (bool, error) {
	regs, err := readCodexServers(path)
	if err != nil {
		return false, err
	}
	for _, r := range regs {
		if r.Name != name || !isLogosServer(r.Command) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		kept, _ := dropServerTables(string(raw), name)
		if err := writeHostFile(path, []byte(kept)); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// dropServerTables removes [mcp_servers.<name>] and its env table, each from
// its header to its last key, and reports whether there was one.
//
// Comments and blank lines after a dropped table's last key are held back
// rather than dropped with it: in a file someone edits by hand they are the
// note on the table below, and a re-run of setup that deletes them does so with
// nothing to say — the TOML hosts are not checked for lost comments, because
// this is meant to keep them.
func dropServerTables(toml, name string) (string, bool) {
	var b, held strings.Builder
	in, had := false, false
	for _, line := range strings.SplitAfter(toml, "\n") {
		t := strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(t, "[") {
			server, _ := codexTable(t)
			in = server == name
			had = had || in
		}
		switch {
		case !in:
			b.WriteString(held.String())
			held.Reset()
			b.WriteString(line)
		case t == "":
			held.WriteString(line)
		default:
			held.Reset()
		}
	}
	b.WriteString(held.String())
	return b.String(), had
}

// unsupportedServerForm names the way toml declares servers that
// dropServerTables cannot see, or "" when it has none.
func unsupportedServerForm(toml string) string {
	top := true
	for _, line := range strings.Split(toml, "\n") {
		t := strings.TrimSpace(stripComment(line))
		switch {
		case t == "[mcp_servers]":
			return "a bare [mcp_servers] table"
		case strings.HasPrefix(t, "["):
			top = false
		case top && strings.HasPrefix(t, "mcp_servers") && strings.Contains(t, "="):
			return "an inline table or dotted keys"
		}
	}
	return ""
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

// stripComment drops a TOML comment, which runs from an unquoted # to the end
// of the line. Quotes are tracked because a path may contain a #, and cutting
// there would hand a host half a binary path — and because a comment after a
// value used to be read as part of it, producing a command no host could
// launch and a header no reader recognised as a server.
func stripComment(line string) string {
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}
