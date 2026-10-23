package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// The help is two surfaces, and the split is a product decision rather than a
// tidying one. logos grew about fifty verbs, and printing all of them was an
// honest inventory that answered the wrong question: a first-time reader wants
// to know what this is *for*, and fifty lines say "a grab-bag" no matter what
// the first line claims.
//
// So the verbs were folded into twenty commands (see surface.go), the short
// help lists those twenty one line each, and `logos help all` is the full
// reference: every form of every command, the retired spellings and what
// replaced them, and the plumbing hooks run.

// helpShort is what `logos`, `logos help` and `logos --help` all print. It is
// deliberately one screen.
func helpShort(w io.Writer) {
	fmt.Fprint(w, `logos — memory and continuity for AI agents: one stops, the next picks up
exactly where it left off

Usage:
  logos [flags]
  logos [command]

Available Commands:
  setup       Connect logos to the AI agents on this machine
  note        Record progress, uncommitted until you checkpoint
  checkpoint  Commit where you stopped: done, failed, verified, next
  resume      Pick up where the last agent left off
  context     Everything bearing on a task, within a token budget
  tried       Ask whether an approach was already ruled out, or record one
  why         What was being decided when a file was touched
  search      Search the vault, or answer from it with --answer
  show        One project on one screen, with its history, plans and graph
  stats       Patterns, usage, and what your memory leans on
  memory      Add, correct, forget and inspect what logos knows
  review      Accept or reject what is waiting for your yes
  import      Bring in CLAUDE.md, Cursor rules, claude-mem, transcripts or git history
  watch       Follow what agents record as it happens
  project     A project's dossier, or rename one
  config      Settings: announce, auto, think, deny, prompt, key, activity
  index       Rebuild the cache from the vault
  doctor      Check the vault, the index and the hosts
  update      Update logos to the latest release
  help        Help about any command

Flags:
  -h, --help      help for logos
  -v, --version   print the version

Use "logos <command> --help" for one command, "logos help all" for every form.
`)
}

// helpAll is the full reference. It exists so that condensing the surface does
// not amount to hiding it: everything logos accepts is here, spelled the way you
// type it, including the old spellings that still work.
func helpAll(w io.Writer) {
	fmt.Fprint(w, `logos — memory and continuity for AI agents

THE HANDOFF
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
    logos context <task> [--project <p>] [--budget <n>]
                                      everything bearing on a task, budgeted (also an MCP tool)
    logos tried <approach> [--project X]
                                      has this already been ruled out? ask before proposing
    logos tried <approach> --ruled-out <what happened> [--layer L] [--scope S]
                                      record one now, without waiting for a checkpoint
    logos why <file> [--limit N]      what was being decided when this file was touched

LOOKING
    logos search <query…>             retrieve only, no generation
    logos search --answer <question…> retrieve and answer from the vault
    logos show [project] [--all]      one project on one screen, for you rather than an agent:
                                      checkpoint, ruled-out approaches, memories, review queue
    logos show --history [project]    checkpoint history for a project, and any abandoned ones
    logos show --plans [project]      plan-mode plans saved when ExitPlanMode is approved
    logos show --continuity           vault-wide: which projects checkpoint, which have gone quiet
    logos show --projects [sync]      auto-detected projects; sync scopes memory to them
    logos show --graph [focus] [--hops N] [--similar] [--list]
                                      draw a project or note with its checkpoints and memories
                                      (default: this directory's project); --list prints it as text
    logos show --since-last [--peek]  catch up on what changed since you were last here
    logos stats [project]             patterns already in the vault: a recurring blocker, a dormant memory
    logos stats --usage [project] [--week] [--usd RATE]
                                      what the budget left out of context packs, dead
                                      ends handed back, handoffs; --week: last 7 days
    logos stats --usage off | on      stop or resume counting (LOGOS_USAGE=off for one process)
    logos stats --memory              composition, growth, and what your memory leans on
    logos watch                       captures, rulings handed back and memory changes as they happen
    logos watch --log [--project P] [--kind K] [--tool T] [--days N] [--json]
                                      every prompt, tool call and turn the host reported —
                                      recorded automatically, not by the model's choice
    logos watch --log --projects      which projects are being recorded
    logos watch --log --compliance [--project P]
                                      how often a checkpoint ask was followed by a checkpoint

MEMORY
    logos memory [add <fact>|forget <id>|log|history <id>|graph|diff]   persistent memory
    logos memory [health|consolidate|pin <id>|unpin <id>|exclude <id>]
                                      what it knows about itself, and what to keep or ignore
    logos memory correct <id> "<what is true now>"   replace a wrong memory; the old one stays in its history
    logos memory log [--project P] [--n N]   what changed in what it knows, newest first
    logos memory diff [subject] [--since D] [--until D] [--days N]   what changed, instant & offline
    logos memory recall <query…> [--project NAME] [--all-projects] [--limit N]
                                      the memories an agent's recall gets, and the review queue
    logos memory dream [--date YYYY-MM-DD] [--phase nrem|rem] [--dry-run]
                                      nightly consolidation: replay, fade, recombine
    logos memory loop [list|add|done|drop]   list or manage open loops (commitments)
    logos review [--all]              accept or reject quarantined memories
    logos review --candidates [--promote <id> | --reject <id>]
                                      transcript candidates, before they become checkpoints
    logos review --connections [accept|reject <id>]
                                      the connections a dream proposed overnight

BRINGING IT IN
    logos import --from claude-mem [--db PATH] [--project P] [--dry-run] [--yes]
                                      bring claude-mem's observations over as memories
    logos import --from claude-md|cursor-rules|claude-auto-memory [--dir DIR] [--dry-run] [--yes]
                                      queue CLAUDE.md / AGENTS.md, Cursor rules or Claude Code's auto memory for review
    logos import --from transcripts [project] [--harness N] [--path FILE|ID] [--dry-run] [--all-projects]
                                      harvest other agents' transcripts into checkpoint candidates
                                      --path is a file, or for a txcript harness a session id
    logos import --from transcripts status | archive <dir>
                                      candidates by tier; copy the cited transcripts somewhere you keep
    logos import --from git [project] [--dir DIR] [--dry-run] [--months N]
                                      seed a cold vault from this repo's git history
    logos project <name>              a project's dossier
    logos project rename <old> <new> [--dry-run] [--merge]
                                      rename a project, carrying its history with it;
                                      --merge combines it into an existing project instead of refusing

SETTINGS
    logos config                      list the settings
    logos config announce [on|quiet|off]   how loudly Logos reports its own work
    logos config auto [on|off|status] what Logos does on its own, as one switch
    logos config auto config [<switch> on|off]   choose which of it; auto.md in the vault holds the answer
    logos config think [off|low|medium|high]   how much the model reasons before answering
    logos config deny [--undo]        keep Logos out of this repository (every repo is tracked
                                      unless it says so); --undo lets it back in
    logos config prompt               the instructions agents are given (LOGOSPROMPT.md)
    logos config key set|rm <ref>     manage API keys in the macOS keychain
    logos config activity [off|on]    stop or resume recording the activity log, for this vault

SETUP AND DIAGNOSTICS
    logos setup [--vault DIR] [--host NAME] [--no-hosts] [--dry-run] [--yes] [--downgrade]
                                      connect logos to the AI agents on this machine
    logos setup --uninstall [--host NAME]
                                      take logos back out of the hosts; the vault is left alone
    logos setup --print-config [--vault DIR] [--format json|toml]
                                      print the server block by hand, for any MCP client not listed above
    logos setup --config <path> [--vault DIR]
                                      merge logos into a config file at a location logos does not know by convention
    logos index [--watch]             sync vault into the cache and embed
    logos doctor [--verbose] [--probe] [--integration] [--report] [--egress]
                                      health of vault, index, hosts; --verbose adds runtimes and tiers; --integration proves a host can reach it;
                                      --report prints a paste-able bundle for a bug report; --egress lists every address logos can contact
    logos update [--check]            check GitHub for a newer release, verify it, replace this binary
    logos help [command|all]          the command list, one command, or this page
    logos version                     which build this is (also --version, -v)

RENAMED — the old spelling still works for now, and says on stderr what replaced it
`)
	var rows [][2]string
	for old, now := range renamed {
		rows = append(rows, [2]string{"logos " + old, now})
	}
	slices.SortFunc(rows, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	for _, r := range rows {
		fmt.Fprintf(w, "    %-34s now %s\n", r[0], r[1])
	}
	fmt.Fprintf(w, `
PLUMBING — run by hooks, hosts and scripts rather than typed
    logos mcp serve                   serve the memory layer to MCP hosts (Claude Desktop, Cursor, your own apps)
    logos mcp serve --tools continuity  serve 11 of the 17 tools, for hosts that load every tool on every turn
    logos mcp serve --http [--port N] serve over a local WebSocket for the browser extension (ChatGPT/Claude.ai/
                                      Perplexity web UIs) — needs LOGOS_BRIDGE_ORIGIN set; never leaves localhost
    logos mcp install [--vault DIR] [--host NAME] [--dry-run] [--yes]
                                      register this logos with the MCP hosts found, without the rest of setup
    logos mcp uninstall [--host NAME] what setup --uninstall runs
    logos hook <cursor|codex> session-start
                                      what a host's session-start hook runs; prints the handoff as JSON
    logos project-name [dir]          the project name for a directory, as the hooks compute it
    logos plugin autoupdate [--notice]
                                      update the Claude Code plugin when it is older than this
                                      binary, at most once a day; --notice prints what it did, once
    logos migrate [--dry-run] [--yes] move a 0.4 vault from ~/brain to ~/logos, leaving a link behind,
                                      and re-pin the hosts that named the old path
    logos demo [--fast]               ninety seconds showing what this is for, in a scratch vault
    logos bench memory <file> [--qa] | bench pipeline
                                      LongMemEval retrieval recall / QA accuracy; the extract→recall loop
                                      (the continuity suite now lives in the sibling logos-bench repo)

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
