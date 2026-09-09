package mcpserver

// The read-only, cacheable half of the memory-tools surface, exposed a second
// way: as MCP resources rather than only as tools.
//
// list_memories, list_projects and memory_diff already cost every session the
// same tools/list tokens whether or not that session ever calls them — none of
// the three mutates anything, so a host that supports resources can list and
// read them without paying for a tool-schema entry. The three stay tools too
// (see tools.go): not every MCP host lets its model read a resource on its own
// the way it calls a tool — several only let a person attach one by hand — and
// a host without that support must not lose access to these three just because
// a more capable host has a cheaper path to them.
//
// Kept deliberately small: pin_memory, exclude_memory and forget mutate the
// vault, and MCP resources have no write verb, so those stay tools-only.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var resourceDefs = []map[string]any{
	{
		"uri":         "logos://memories",
		"name":        "memories",
		"description": "Everything currently in the user's memory, with ids. Same content as the list_memories tool.",
		"mimeType":    "text/plain",
	},
	{
		"uri":         "logos://projects",
		"name":        "projects",
		"description": "Projects brain has detected from the user's activity, most recently active first. Same content as the list_projects tool.",
		"mimeType":    "text/plain",
	},
}

// resourceTemplateDefs covers memory_diff, the one of the three that takes
// arguments — an optional subject and an optional day window — so it cannot be
// one static URI the way the other two are. RFC 6570's query-form expansion
// ({?subject,days}) lets both stay optional in the one template.
var resourceTemplateDefs = []map[string]any{
	{
		"uriTemplate": "logos://memory-diff{?subject,days}",
		"name":        "memory-diff",
		"description": "What the user's memory has learned, dropped, or corroborated over a recent window, optionally narrowed to one subject. Same content as the memory_diff tool; subject and days default the same way (all subjects, 7 days).",
		"mimeType":    "text/plain",
	},
}

// readResource dispatches a resources/read call by URI. It is the read side of
// the same three operations dispatch (server.go) already exposes as tools,
// kept as its own switch rather than folded into dispatch so a change to one
// surface's argument defaults cannot silently drift the other's without a diff
// showing it.
func (s *Session) readResource(rawURI string) (string, error) {
	if rawURI == "logos://memories" {
		return s.listMemories()
	}
	if rawURI == "logos://projects" {
		return s.listProjects()
	}
	if rest, ok := strings.CutPrefix(rawURI, "logos://memory-diff"); ok {
		q, err := parseResourceQuery(rest)
		if err != nil {
			return "", fmt.Errorf("bad memory-diff resource uri: %w", err)
		}
		days := 7
		if d := q.Get("days"); d != "" {
			n, err := strconv.Atoi(d)
			if err != nil {
				return "", fmt.Errorf("memory-diff days must be a number, got %q", d)
			}
			days = n
		}
		return s.memoryDiff(q.Get("subject"), days)
	}
	return "", fmt.Errorf("unknown resource %q", rawURI)
}

// parseResourceQuery reads the "?a=b&c=d" tail a client leaves after expanding
// a {?subject,days}-style template. No tail at all — the bare "logos://memory-
// diff" a client sends when it wants every default — is not an error; it is
// the same as an empty query string.
func parseResourceQuery(tail string) (url.Values, error) {
	if !strings.HasPrefix(tail, "?") {
		return url.Values{}, nil
	}
	return url.ParseQuery(tail[1:])
}
