package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/ingest"
)

// autoDefaultsTopic is the "said:" line in auto.md for the notice below.
const autoDefaultsTopic = "auto-defaults"

// newlyOn are the switches that shipped off and now ship on. Their state is
// what the notice reports, because it is the thing that changed for someone who
// upgraded: archive and redact were never off for anyone, so they are not news.
var newlyOn = []auto.Switch{auto.Dream, auto.Index, auto.Import, auto.Review}

// noteAutoDefaults says, once per vault, that automatic behaviour is now on
// until turned off. A user who upgraded would otherwise find a model busy in
// the background, or a copied transcript, with no word of where it came from
// (invariant 3), and the sentence that ends it is the point of the notice.
//
// On stderr, as the other startup notices are, so it cannot land inside a
// command's stdout JSON; and not at all for --json, a hook, or the MCP server,
// whose stderr is a host log nobody reads at a terminal and where "once" would
// be spent on no one. That the notice was shown is recorded in auto.md, not in
// the index, so a deleted index cannot make it repeat (invariant 1).
func noteAutoDefaults(stderr io.Writer, cmd string, args []string) {
	switch cmd {
	case "hook", "mcp", "version", "--version", "-v", "help", "--help", "-h", "setup", "migrate":
		return
	}
	if inHook || os.Getenv("LOGOS_HOOK") != "" || hasFlag(args, "--json") || hasFlag(args, "--auto") ||
		hasFlag(args, "--help") || hasFlag(args, "-h") {
		return
	}
	v := vaultPath()
	if fi, err := os.Stat(v); err != nil || !fi.IsDir() {
		return // nothing to upgrade, and saying it would create the vault
	}
	if auto.Said(v, autoDefaultsTopic) {
		return
	}
	cfg, err := auto.Load(v)
	if err != nil {
		fmt.Fprintf(stderr, "logos: could not read %s to tell what is on by default: %v\n", auto.Path(v), err)
		return
	}
	var on []string
	for _, s := range newlyOn {
		if cfg.On(s) {
			on = append(on, string(s))
		}
	}
	if ingest.KeepsTranscripts(v) {
		on = append(on, "archive (a redacted copy of each transcript it reads)")
	}
	if len(on) > 0 {
		fmt.Fprintf(stderr, "logos: automatic behaviour is now on by default — %s. "+
			"`logos config auto status` lists it; `logos config auto off` turns all of it off, and this is said once.\n",
			strings.Join(on, ", "))
	}
	if err := auto.MarkSaid(v, autoDefaultsTopic); err != nil {
		fmt.Fprintf(stderr, "logos: could not record that this was said, so it will be said again: %v\n", err)
	}
}
