package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/pragun/brain/internal/mcpserver"

	_ "modernc.org/sqlite"
)

// runMCPServe runs the memory MCP server on stdio, so any MCP host (Claude
// Desktop, Claude Code, Cursor) can plug into the user's local memory.
func runMCPServe() error {
	rt, err := openRouter()
	if err != nil {
		return err
	}
	vault := vaultPath()
	if _, err := os.Stat(vault); err != nil {
		return fmt.Errorf("vault not found at %s — set BRAIN_VAULT", vault)
	}
	db, err := sql.Open("sqlite", vault+"/.brain/index.db")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	srv := mcpserver.New(db, rt, vault)
	return srv.Serve(os.Stdin, os.Stdout)
}
