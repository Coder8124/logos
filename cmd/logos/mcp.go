package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/mcpserver"
	vaultmod "github.com/Coder8124/logos/internal/vault"

	_ "modernc.org/sqlite"
)

// serveVault resolves the vault this server will serve, creating the default
// one when it is not there.
//
// `/plugin install logos@logos` is the first route the README offers, and it
// wires the MCP server with no LOGOS_VAULT and no setup step. On a laptop that
// had never run `logos setup` the server started, found no ~/logos and exited,
// which the host shows as a failed connection with no cause attached: the first
// thing a new user saw the product do was fail to start.
//
// Creating it is only right where nobody chose the path. An explicit
// LOGOS_VAULT, or a vault recorded by setup, that is not there is a typo or a
// directory that moved — and building a fresh empty one over either is the
// silent half-vault requireVault exists to prevent.
func serveVault() (string, error) {
	dir, explicit := vaultmod.Chosen()
	if _, err := os.Stat(dir); err == nil {
		// A vault under a temp root is there today and gone after a reboot, and
		// while it is there everything reports a healthy zero — which is what an
		// empty scratch vault and a working install with nothing in it both look
		// like. doctor checks this, but only when somebody runs doctor; the
		// server runs every session, so it is the one that can say so in time.
		// LOGOS_VAULT set for this process is someone doing it deliberately.
		if os.Getenv("LOGOS_VAULT") == "" && health.UnderTempDir(dir) {
			fmt.Fprintf(os.Stderr, "logos: this machine's vault is %s, a temporary directory — it will be empty or gone after a reboot; `logos setup --vault <path>` moves it\n", dir)
		}
		return dir, nil
	} else if explicit {
		return "", missingVaultError(dir)
	}
	if err := vaultmod.MkdirPrivate(dir); err != nil {
		return "", fmt.Errorf("creating a vault at %s: %w", dir, err)
	}
	// stderr, because stdout is the JSON-RPC transport. Announced rather than
	// done quietly (invariant 3): a directory made on the user's disk without
	// them asking is a thing they are told about, and the host logs it.
	fmt.Fprintf(os.Stderr, "logos: no vault found — created one at %s\n", dir)
	return dir, nil
}

// runMCPServe runs the memory MCP server on stdio, so any MCP host (Claude
// Desktop, Claude Code, Cursor) can plug into the user's local memory.
func runMCPServe() error { return serveMCP(os.Stdin, os.Stdout) }

func serveMCP(in io.Reader, out io.Writer) error {
	srv, closeIndex, err := buildStdioServer()
	if err != nil {
		// Serve anyway: a server that exits before the handshake is cached as
		// failed by Claude Code for 15 minutes; see mcpserver.Unavailable.
		// stderr, because stdout is the JSON-RPC transport.
		fmt.Fprintf(os.Stderr, "logos: %v\n", err)
		return mcpserver.Unavailable(err).Serve(in, out)
	}
	defer closeIndex()
	return srv.Serve(in, out)
}

func buildStdioServer() (*mcpserver.Server, func(), error) {
	vault, err := serveVault()
	if err != nil {
		return nil, nil, err
	}

	// index.Open rather than sql.Open on the file directly. Opening the raw path
	// skipped four things this server needs, and each failed silently or late:
	//
	//   - it does not create .logos/, so a vault that has never been indexed
	//     failed with "unable to open database file (14)" — which is what a host
	//     shows a user who ran `logos setup` and nothing else;
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
		return nil, nil, err
	}

	srv, err := newMCPServer(ix.DB, vault)
	if err != nil {
		ix.Close()
		return nil, nil, err
	}
	if self, err := selfPath(); err == nil {
		srv.Shell, _ = terminalCommand(self)
	}
	return srv, func() { ix.Close() }, nil
}

// newMCPServer builds the server both transports run. A missing runtime is
// not fatal here. Continuity — checkpoint, resume, note_progress,
// before_you_try — needs no model, and retrieval falls back to lexical, so the
// useful half of the server still runs. Refusing to start meant `logos setup`
// could wire four hosts, report success, and leave the user with a host that
// fails to connect.
func newMCPServer(db *sql.DB, vault string) (*mcpserver.Server, error) {
	rt, err := openRouterOptional()
	if err != nil {
		return nil, err
	}
	if rt == nil {
		// stderr, not stdout: stdout is the JSON-RPC transport and anything
		// written there corrupts the stream. The host surfaces this in its logs.
		fmt.Fprintln(os.Stderr,
			"logos: no local model runtime found — memories and notes are matched by keyword, "+
				"not meaning; checkpoint, resume and before_you_try are unaffected")
	}
	srv := mcpserver.New(db, rt, vault)
	// LOGOS_EMBED chooses the model `logos index` embeds the vault with, so the
	// server has to query with the same one — see SetEmbedModel. Unset, both
	// sides use the default and the router's choice stands.
	if _, set := os.LookupEnv("LOGOS_EMBED"); set {
		model, _ := embedModel()
		srv.SetEmbedModel(model)
	}
	if rt != nil {
		if model := srv.EmbedModel(); model != "" {
			fmt.Fprintf(os.Stderr, "logos: %s at %s, embedding with %s\n", rt.Local().Name, rt.Local().BaseURL, model)
		} else {
			fmt.Fprintf(os.Stderr, "logos: %s at %s, no embedding model — lexical retrieval\n", rt.Local().Name, rt.Local().BaseURL)
		}
	}
	return srv, nil
}

// runMCPServeHTTP is runMCPServe's local-network sibling: same Server, a
// second transport (internal/mcpserver/http.go) so a client that isn't the
// process that started us — a browser extension bridging logos into a web
// AI's chat UI, see extension/ — can reach the same memory an MCP host on
// stdio does.
func runMCPServeHTTP(port int) error {
	vault := vaultPath()
	if _, err := os.Stat(vault); err != nil {
		return missingVaultError(vault)
	}

	ix, err := index.Open(vault)
	if err != nil {
		return err
	}
	defer ix.Close()

	token, err := mcpserver.LoadOrCreateToken(vault)
	if err != nil {
		return fmt.Errorf("pairing token: %w", err)
	}

	// The extension id is not knowable in advance the way a CLI default can
	// be: Chrome assigns a different id to every unpacked/dev load, and only a
	// published listing gets a stable one. Rather than guess or accept "*"
	// (which would defeat the whole point of an Origin allowlist), require it
	// explicitly — LOGOS_BRIDGE_ORIGIN, comma-separated for more than one.
	origins := splitCSV(os.Getenv("LOGOS_BRIDGE_ORIGIN"))
	if len(origins) == 0 {
		return fmt.Errorf(
			"LOGOS_BRIDGE_ORIGIN is not set — the web bridge refuses to accept " +
				"connections from an unnamed origin; set it to the extension's " +
				"chrome-extension://<id> (see extension/README.md)")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("logos: web bridge listening on %s, paired for %v\n", addr, origins)
	fmt.Printf("logos: pairing token: %s\n", token)

	srv, err := newMCPServer(ix.DB, vault)
	if err != nil {
		return err
	}
	if self, err := selfPath(); err == nil {
		srv.Shell, _ = terminalCommand(self)
	}
	return srv.ServeHTTP(mcpserver.HTTPConfig{Addr: addr, Token: token, Origins: origins})
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
