package mcpserver

// Which agent is calling, decided without asking it.
//
// A vault shared by several coding agents records what was learned but not who
// learned it, which is fine right up until two of them disagree — then a
// reviewer, or another agent, has no way to tell whose claim a memory is. The
// obvious fix is a tool argument the model fills in with its own name, and it
// is the wrong one for exactly the reason scope.go gives for project: a string
// the model has to remember to pass is a string it will sometimes get wrong or
// skip, and a wrong name here does not fail loudly — it silently misattributes
// a fact to the wrong agent.
//
// MCP already carries this without asking: every client identifies itself in
// the initialize handshake's clientInfo, before a single tool is called and
// with no cooperation required from whatever model is driving it. That is the
// same kind of observed, protocol-level fact the roots and cwd reads elsewhere
// in this package are, so it is read the same way.

import (
	"encoding/json"
	"strings"
)

// clientInfoFromInitialize pulls the calling application's name out of an
// initialize request — "claude-code", "cursor", "codex", whatever the host
// identifies itself as. Absent or unparsable params yield "", which is a
// legitimate answer: a host that omits clientInfo is not lying about its
// identity, it simply did not say, and the memory it writes carries no agent
// rather than a guessed one.
func clientInfoFromInitialize(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var p struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return strings.TrimSpace(p.ClientInfo.Name)
}
