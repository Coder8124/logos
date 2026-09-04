package main

import (
	"fmt"
	"os"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/mcpserver"

	_ "modernc.org/sqlite"
)

// runMCPServe runs the memory MCP server on stdio, so any MCP host (Claude
// Desktop, Claude Code, Cursor) can plug into the user's local memory.
func runMCPServe() error {
	// A missing runtime is not fatal here. Continuity — checkpoint, resume,
	// note_progress, before_you_try — needs no model, and retrieval falls back
	// to lexical, so the useful half of the server still runs. Refusing to start
	// meant `brain setup` could wire four hosts, report success, and leave the
	// user with a host that fails to connect.
	rt, err := openRouterOptional()
	if err != nil {
		return err
	}
	if rt == nil {
		// stderr, not stdout: stdout is the JSON-RPC transport and anything
		// written there corrupts the stream. The host surfaces this in its logs.
		fmt.Fprintln(os.Stderr,
			"brain: no local model runtime found — serving with lexical retrieval; "+
				"checkpoint, resume and before_you_try are unaffected")
	}

	vault := vaultPath()
	if _, err := os.Stat(vault); err != nil {
		return missingVaultError(vault)
	}

	// index.Open rather than sql.Open on the file directly. Opening the raw path
	// skipped four things this server needs, and each failed silently or late:
	//
	//   - it does not create .brain/, so a vault that has never been indexed
	//     failed with "unable to open database file (14)" — which is what a host
	//     shows a user who ran `brain setup` and nothing else;
	//   - it does not set busy_timeout, so the CLI or the desktop app touching
	//     the same vault made one of the two fail outright with SQLITE_BUSY,
	//     which is the normal arrangement here rather than an edge case;
	//   - it does not apply the schema, so retrieval had no tables to read;
	//   - worst, it never called memory.SetVault, and flush() returns nil when no
	//     vault is registered. Every memory an agent stored through MCP went into
	//     the cache and never reached the markdown. "Delete the cache, lose
	//     nothing" was not true on the one path agents actually use.
	ix, err := index.Open(vault)
	if err != nil {
		return err
	}
	defer ix.Close()

	srv := mcpserver.New(ix.DB, rt, vault)
	return srv.Serve(os.Stdin, os.Stdout)
}
