// Package agentprompt carries the instructions an agent should read before it
// uses Logos.
//
// It is embedded rather than read from disk because the moment it is needed is
// the moment a host has just launched us from an npm package or a Homebrew
// cellar, and "the docs were next to the binary" is not a thing that survives
// packaging. A prompt that is sometimes missing is worse than no prompt: the
// agent's behaviour then depends on how the user installed us.
//
// Three ways out, one source:
//
//   - the MCP `initialize` response's `instructions` field, which every
//     conforming host puts in front of its model without the user doing
//     anything at all. This is the one that matters; the others are for people
//     who want the text somewhere they can edit it.
//   - `brain prompt`, to paste into a CLAUDE.md or a system prompt.
//   - systemmd/BRAINPROMPT.md in the repository, which is the same bytes.
package agentprompt

import _ "embed"

//go:embed BRAINPROMPT.md
var text string

// Text is the full instruction document.
func Text() string { return text }
