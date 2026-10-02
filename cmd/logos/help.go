package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// The help is two surfaces, and the split is a product decision rather than a
// tidying one. logos grew about forty verbs, and printing all of them was an
// honest inventory that answered the wrong question: a first-time reader wants
// to know what this is *for*, and forty lines of episodic capture, voice and
// benchmarks say "a grab-bag" no matter what the first line claims.
//
// So the default is the three journeys the product is actually about — hand
// off, brief, intercept — plus the three commands that get you there. Nothing
// is hidden: `logos help all` is the old inventory, grouped, and every verb
// still works exactly as it did. This changes what help prints, not what logos
// does.

// helpShort is what `logos`, `logos help` and `logos --help` all print. It is
// deliberately one screen: the handoff is the centre of the product, and it is
// what a reader should be able to try in the next thirty seconds.
func helpShort(w io.Writer) {
	fmt.Fprint(w, `logos — local-first memory and continuity for AI agents

Agents forget the moment a session ends. logos is the memory they hand to one
another: one stops, the next picks up exactly where it left off.

THE HANDOFF — an agent finishes, and another continues
    logos note [project] <what you did>
                                      record progress; uncommitted until you checkpoint
    logos checkpoint [project] [--task ..] [--next ..] [--failed ..] [--agent <name>] [--handoff <agent>]
                                      commit where you stopped, as a note in the vault
    logos resume [project]            pick up where the last agent left off
                                      the project defaults to the directory you are in

THE BRIEF — what bears on the work, before the work starts
    logos context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)

THE INTERCEPT — the dead end nobody remembers recording
    logos tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing
    logos tried <approach> --ruled-out <what happened> [--layer L] [--scope S]
                                      record one now, without waiting for a checkpoint

GETTING THERE
    logos setup [--vault DIR] [--host NAME] [--no-hosts] [--dry-run] [--yes] [--downgrade]
                                      connect logos to the AI agents on this machine
    logos mcp serve | mcp install     serve the memory to MCP hosts; wire the ones found
    logos mcp uninstall [--host NAME] take logos back out of the hosts; the vault is left alone
    logos doctor [--verbose] [--probe] [--integration] [--report]
                                      health of vault, index, hosts; --integration proves reach
                                      --report prints a paste-able bundle for a bug report
    logos update [--check]            check GitHub for a newer release, verify it, replace this binary

    LOGOS_VAULT points at the vault (default ~/logos)

`+"`logos help all`"+` lists the rest — memory, retrieval, benchmarks.
`)
}

// helpAll is the full inventory, grouped. It exists so that demoting the
// general surface does not amount to hiding it: everything logos has ever
// accepted is here, spelled the way you type it.
func helpAll(w io.Writer) {
	fmt.Fprintf(w, `logos — local-first memory and continuity for AI agents

CONTINUITY
    logos note [project] <what you did>
                                      record progress; uncommitted until you checkpoint
    logos checkpoint [project] [--task ..] [--intent ..] [--state ..] [--next ..] [--decided ..]
                     [--verified ..] [--failed ..] [--blocker ..] [--ran ..]
                     [--question ..] [--file ..] [--agent <name>] [--handoff <agent>]
                                      commit where you stopped, as a note in the vault
                                      repeat --decided, --verified, --failed, --blocker,
                                      --ran, --question and --file to add more than one
    logos resume [project]            pick up where the last agent left off
                                      the project defaults to the directory you are in
    logos ingest [project] [--harness N] [--path FILE|ID] [--dry-run] [--all-projects]
                                      harvest other agents' transcripts into checkpoint candidates
                                      --path is a file, or for a txcript harness a session id
    logos ingest review [--promote <id> | --reject <id>]
                                      review candidates before they become checkpoints
    logos ingest status               candidates by tier, and how many can still be distilled
    logos ingest archive <dir>        copy the cited transcripts somewhere you keep them
    logos sessions [project]          checkpoint history for a project, and any abandoned ones
    logos plans [project]             plan-mode plans saved when ExitPlanMode is approved
    logos continuity                  vault-wide: which projects checkpoint, which have gone quiet
    logos bootstrap [project] [--dir DIR] [--dry-run] [--months N]
                                      seed a cold vault from this repo's git history
    logos context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)
    logos tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing
    logos tried <approach> --ruled-out <what happened> [--layer L] [--scope S]
                                      record one now, without waiting for a checkpoint
    logos insights [project]          patterns already in the vault: a recurring blocker, a dormant memory
    logos usage [project] [--usd RATE]
                                      what the budget left out of context packs, and
                                      dead ends handed back before a retry
    logos usage off | on              stop or resume counting (LOGOS_USAGE=off for one process)
    logos why <file> [--limit N]      what was being decided when this file was touched
    logos projects | project <name>   auto-detected projects and their dossiers
    logos project-name [dir]          the project name for a directory, as the hooks compute it
    logos hook <cursor|codex> session-start
                                      what a host's session-start hook runs; prints the handoff as JSON
    logos plugin autoupdate [--notice]
                                      update the Claude Code plugin when it is older than this
                                      binary, at most once a day; --notice prints what it did, once
    logos project rename <old> <new> [--dry-run] [--merge]
                                      rename a project, carrying its history with it;
                                      --merge combines it into an existing project instead of refusing

MEMORY
    logos memory [add <fact>|forget <id>|log|history <id>|graph|diff]   persistent memory
    logos memory [health|consolidate|pin <id>|unpin <id>|exclude <id>]
                                      what it knows about itself, and what to keep or ignore
    logos memory log [--project P] [--n N]   what changed in what it knows, newest first
    logos activity [--project P] [--kind K] [--tool T] [--days N] [--json]
                                      every prompt, tool call and turn the host reported —
                                      recorded automatically, not by the model's choice
    logos activity --projects         which projects are being recorded
    logos activity [off|on]           stop or resume recording it, for this vault
    logos announce [on|quiet|off]     how loudly Logos reports its own work
    logos prompt                      the instructions agents are given (LOGOSPROMPT.md)
    logos demo [--fast]               ninety seconds showing what this is for, in a scratch vault
    logos memory diff [subject] [--since D] [--until D] [--days N]   what changed, instant & offline
    logos loop [list|add|done|drop]   list or manage open loops (commitments)
    logos graph [focus] [--hops N] [--similar] [--list]
                                      draw a project or note with its checkpoints and memories
                                      (default: this directory's project); --list prints it as text

RETRIEVAL
    logos search <query…>             retrieve only, no generation
    logos ask <question…>             retrieve and answer from the vault
    logos index [--watch]             sync vault into the cache and embed
    logos replay [--peek]             catch up on what changed since you were last here
    logos reflect                     descriptive stats over your memory (composition, growth, what it leans on)
    logos review [--all]              accept or reject quarantined memories
    logos dream [--date YYYY-MM-DD] [--phase nrem|rem] [--dry-run]
                                      nightly consolidation: replay, fade, recombine
    logos dream review | accept|reject <id>
                                      review the connections REM proposed overnight
    logos think [off|low|medium|high]  how much the model reasons before answering

SETUP AND DIAGNOSTICS
    logos setup [--vault DIR] [--host NAME] [--no-hosts] [--dry-run] [--yes] [--downgrade]
                                      connect logos to the AI agents on this machine
    logos setup --print-config [--vault DIR] [--format json|toml]
                                      print the server block by hand, for any MCP client not listed above
    logos setup --config <path> [--vault DIR]
                                      merge logos into a config file at a location logos does not know by convention
    logos mcp serve                   serve the memory layer to MCP hosts (Claude Desktop, Cursor, your own apps)
    logos mcp serve --tools continuity  serve 11 of the 17 tools, for hosts that load every tool on every turn
    logos mcp serve --http [--port N] serve over a local WebSocket for the browser extension (ChatGPT/Claude.ai/
                                      Perplexity web UIs) — needs LOGOS_BRIDGE_ORIGIN set; never leaves localhost
    logos mcp install [--vault DIR] [--host NAME] [--dry-run] [--yes]
                                      register this logos with the MCP hosts found
    logos mcp uninstall [--host NAME] remove logos from the MCP hosts found; never touches the vault
    logos migrate [--dry-run] [--yes] move a 0.4 vault from ~/brain to ~/logos, leaving a link behind,
                                      and re-pin the hosts that named the old path
    logos doctor [--verbose] [--probe] [--integration] [--report]
                                      health of vault, index, hosts; --verbose adds runtimes and tiers; --integration proves a host can reach it
    logos key set|rm <ref>            manage API keys in the macOS keychain
    logos update [--check]            check GitHub for a newer release, verify it, replace this binary
    logos version                     which build this is
    logos help [all]                  the three core journeys, or this list

BENCHMARKS
    logos bench continuity [list] [--only X] [--verbose] [--logos-only] [--variants]
                                      the handoff + memory suite, against every system installed
    logos bench memory <file> | bench pipeline
                                      LongMemEval retrieval recall; the extract→recall loop

ENV
    LOGOS_VAULT     path to the vault (default ~/logos)
    LOGOS_MODEL     chat model (default %s)
    LOGOS_EMBED     embed model (default %s); "off" disables embeddings, search stays lexical
    LOGOS_RUNTIME   OpenAI-compatible base URL to use instead of auto-discovery
                    (LOGOS_RUNTIME_KEY for a bearer token)
`, defaultChatModel, defaultEmbedModel)
}

// commandHelp prints the lines of the full help that describe cmd, each with
// the explanation indented under it, and reports whether there were any.
func commandHelp(w io.Writer, cmd string) bool {
	if cmd == "" || strings.HasPrefix(cmd, "-") {
		return false
	}
	var all strings.Builder
	helpAll(&all)
	var out strings.Builder
	inEntry := false
	for _, line := range strings.Split(all.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "logos "):
			inEntry = trimmed == "logos "+cmd || strings.HasPrefix(trimmed, "logos "+cmd+" ") ||
				strings.Contains(trimmed, "| "+cmd+" ")
		case trimmed == "" || !strings.HasPrefix(line, " "):
			inEntry = false
		}
		if inEntry {
			out.WriteString(line + "\n")
		}
	}
	if out.Len() == 0 {
		return false
	}
	fmt.Fprint(w, out.String())
	return true
}

// usage is the failure path — no arguments, or a verb nobody recognises. It
// prints the short help to stderr and exits non-zero, because a command line
// that could not be parsed is an error even though the text is identical to
// what `logos help` prints on success.
func usage() {
	helpShort(os.Stderr)
	os.Exit(2)
}
