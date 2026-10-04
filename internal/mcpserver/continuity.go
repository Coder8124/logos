package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/advice"
	"github.com/Coder8124/logos/internal/announce"
	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/untrusted"
	"github.com/Coder8124/logos/internal/usage"
)

// contextPack assembles everything relevant to a file, project, or topic — the
// project dossier, standing preferences, and related memories — as one markdown
// bundle a host can drop straight into its model's context.
func (s *Server) context(req contextpack.Request) (string, error) {
	if strings.TrimSpace(req.Task) == "" && strings.TrimSpace(req.Hint) == "" {
		return "", fmt.Errorf("context needs a task (what you are trying to do) or a project")
	}
	pack, err := contextpack.Build(s.index(), s.embed, s.embedModel, req)
	if err != nil {
		return "", err
	}
	body := pack.Render()
	return s.lead(pack) + body + s.awaitingReview() + s.ledgerPack("mcp:context", req.Hint, pack), nil
}

// lead puts the receipt above the pack rather than below it. A person skimming
// a tool result reads the first line and stops; a summary underneath a page of
// markdown is a summary nobody sees.
func (s *Server) lead(pack contextpack.Pack) string {
	carried := pack.Carried()
	if carried == "" {
		return ""
	}
	r := announce.Say(s.vault, "recalled "+carried)
	if r == "" {
		return ""
	}
	return r + "\n\n"
}

// resume is context aimed at one question: where did the last agent stop. It is
// the same assembly as context, told to lead with the checkpoint, so an agent
// that has just been handed a project can start with one call.
// beforeYouTry is the one tool here that is not retrieval.
//
// Everything else answers a question the host's model already has. This answers
// two it does not know to ask: whether the approach was already ruled out, and
// whether there is a known-good way to do it with a trap the obvious way falls
// into. Which is why the tool description is written as an instruction: the
// model has no way of knowing either on its own.
//
// here is the project the check is counted under in the usage ledger, which is
// not project: the search stays unscoped, the count belongs to the work here.
func (s *Server) beforeYouTry(approach, project, here string) (string, error) {
	if strings.TrimSpace(approach) == "" {
		return "", fmt.Errorf("before_you_try needs the approach you are considering")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	hits, semanticErr, err := deadend.CheckNoting(s.vault, s.DB, s.embed, s.embedModel, approach, project, 6)
	if err != nil {
		return "", err
	}
	// The corpus is gathered unranked and unfiltered (p=nil, so RecallProcedures
	// takes its All()-backed fallback with no reinforcement side effect) — Check
	// does its own lexical-plus-semantic scoring below, and a candidate the
	// embedding pass here dropped early is exactly the one the lexical arm is
	// for. Mirrors deadend's own Collect-then-Check split, and for the same
	// reason: ranking degrades to lexical-only with no embedder, gathering must
	// not have degraded it already.
	corpus, err := memory.RecallProcedures(s.DB, nil, "", "", 0)
	if err != nil {
		return "", err
	}
	procHits, err := procedure.Check(corpus, s.embed, s.embedModel, approach, project, 4)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(untrusted.Boundary)
	b.WriteString("\n\n")
	b.WriteString(deadend.Render(approach, hits))
	b.WriteString(deadend.SemanticSkipped(semanticErr))
	if section := procedure.Render(procHits); section != "" {
		b.WriteString("\n")
		b.WriteString(section)
	}
	if len(hits) > 0 {
		b.WriteString(s.ledger(usage.Event{Kind: usage.KindDeadEnd, Via: "mcp:before_you_try", Project: here, Rulings: len(hits)}))
	}
	return b.String(), nil
}

// why reports what was being decided when a file was worked on.
//
// Reads markdown out of the vault and needs no model and no index, so it works
// on a machine with neither — which matters, because the moment it is useful is
// the moment an agent is about to change something it does not understand.
//
// here is the project a returned dead end is counted under in the usage ledger.
func (s *Server) why(file string, limit int, here string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("why needs a file path")
	}
	if s.vault == "" {
		return "", fmt.Errorf("why reads checkpoints from the vault, and no vault is configured")
	}
	mentions, err := session.Touching(s.vault, file, limit)
	if err != nil {
		return "", err
	}
	if len(mentions) == 0 {
		// Distinguish the two nothings. "Nothing was recorded" is a fact about
		// the record; "there is no reason" is a claim about the code, and this
		// tool is not entitled to make it.
		return fmt.Sprintf(
			"No checkpoint mentions %s.\n\nNothing was written down while this file was worked on, or it was "+
				"recorded under a different path. Do not read this as evidence the code is arbitrary.", file), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# What was decided around %s\n\n", file)
	ruled := 0
	for _, m := range mentions {
		ruled += len(nonBlank(m.Failed))
		when := "an unknown date"
		if m.TS > 0 {
			when = time.Unix(m.TS, 0).Format("2 Jan 2006")
		}
		who := m.Agent
		if who == "" {
			who = "an unrecorded author"
		}
		fmt.Fprintf(&b, "## %s — %s", when, who)
		if m.Project != "" {
			fmt.Fprintf(&b, " · %s", m.Project)
		}
		b.WriteString("\n\n")
		if m.Task != "" {
			// "While:" is a label, so its value is one line. A task recorded with
			// a heading in it could otherwise close the label and open a section
			// of its own, directly above the evidence why exists to present.
			fmt.Fprintf(&b, "While: %s\n\n", untrusted.Inline(m.Task))
		}
		if m.Intent != "" {
			fmt.Fprintf(&b, "Because: %s\n\n", untrusted.Inline(m.Intent))
		}
		// Ruled out first: a decision explains the shape of the code, and a dead
		// end explains why it is not some other shape — which is what someone
		// about to "fix" it needs.
		writeList(&b, "Ruled out", m.Failed)
		writeList(&b, "Decided", m.Decisions)
		writeList(&b, "Still open", m.Questions)
		fmt.Fprintf(&b, "Source: %s\n\n", m.Slug)
	}
	b.WriteString("Recorded while the file was touched, not an analysis of the code: check it still holds.\n")
	// Counted only when a ruled-out approach came back: a why that returned
	// decisions alone kept nobody from repeating anything.
	if ruled > 0 {
		b.WriteString(s.ledger(usage.Event{Kind: usage.KindDeadEnd, Via: "mcp:why", Project: here, Rulings: ruled}))
	}
	return b.String(), nil
}

func nonBlank(items []string) []string {
	var kept []string
	for _, it := range items {
		if s := strings.TrimSpace(it); s != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

func writeList(b *strings.Builder, label string, items []string) {
	kept := nonBlank(items)
	if len(kept) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s:**\n", label)
	for _, it := range kept {
		// Every item here is a checkpoint field somebody else's agent wrote. One
		// bullet, one line — a newline in a recorded dead end was enough to end
		// the list and start a heading of logos's own.
		fmt.Fprintf(b, "- %s\n", untrusted.Inline(it))
	}
	b.WriteString("\n")
}

// resume takes the project argument unresolved, because whether it was given at
// all decides whether the worktree narrows it — see resolveContinuity.
func (s *Session) resume(projectArg, agent string, budget int, since contextpack.Since) (string, error) {
	project, worktree := s.resolveContinuity(projectArg)
	chose := ""
	// A folder name the vault has never heard of is the ordinary way to reach
	// this branch, not a launch in /: a scratch directory, a fresh clone under
	// another name. The guard used to test only for an empty name, and a
	// working directory almost always has a basename, so the fallback below
	// was dead code while the agent was told, emphatically, that nothing was
	// recorded (#169). A name the caller gave, or LOGOS_PROJECT set, is still
	// honoured as asked — there the caller asserted something, and is told it
	// was wrong below rather than handed another project's work.
	guess := s.inferredProject()
	swept, sweptFor := "", ""
	if strings.TrimSpace(projectArg) == "" && guess != "" && !s.projectExists(project) {
		// The folder's own project is swept before it is judged unknown. One
		// with no checkpoint yet is the one whose killed sessions are recorded
		// nowhere else, and falling back first swept the other project instead:
		// in a host with no hooks they were never recorded, and after a week
		// they were gone (#193). If the sweep recorded one, the project exists
		// now and there is nothing to fall back from.
		swept, sweptFor = ingest.SweepNotice(ingest.Sweep(s.vault, project, time.Now())), project
	}
	if strings.TrimSpace(projectArg) == "" && guess != "" && !s.projectExists(project) {
		if ps := s.checkpointedProjects(); len(ps) > 0 {
			chose = fmt.Sprintf("_Nothing in this vault is filed under %s, the folder this host was launched in — resuming %s, the most recently checkpointed project%s. Pass project to resume a different one, or checkpoint to start %s._\n\n",
				untrusted.Inline(guess), untrusted.Inline(ps[0].name), s.knownProjects(), untrusted.Inline(guess))
			project, worktree = ps[0].name, ""
		}
	}
	if strings.TrimSpace(project) == "" {
		// Hosts without hooks (Cursor, Codex, Claude Desktop) launched outside
		// any repository give no project, and an error sent the user off to
		// learn the name another tool filed the work under. The most recent
		// checkpoint is the likeliest thing they mean by "resume"; saying which
		// was picked lets the agent correct course if it was not. The worktree
		// is dropped because it was read from where the host stands, not from
		// the project being resumed.
		ps := s.checkpointedProjects()
		if len(ps) == 0 {
			return "", fmt.Errorf("resume needs a project%s", s.knownProjects())
		}
		project, worktree = ps[0].name, ""
		chose = fmt.Sprintf("_No project given and none to tell from where this host was launched — resuming %s, the most recently checkpointed project%s. Pass project to resume a different one._\n\n",
			untrusted.Inline(project), s.knownProjects())
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	// Before the pack, so a session whose host was killed before the shutdown
	// path could record it is in the handoff it is missing from (#134).
	if project != sweptFor {
		swept += ingest.SweepNotice(ingest.Sweep(s.vault, project, time.Now()))
	}
	pack, err := contextpack.Build(s.index(), s.embed, s.embedModel, contextpack.Request{
		Task: "resume work on " + project, Hint: project, Worktree: worktree, Dir: s.repoDir(project),
		Agent: s.agentFor(map[string]any{"agent": agent}), Budget: budget, Since: since,
	})
	if err != nil {
		return "", err
	}
	// First, above the pack: the pack's own last word on an unmatched name is
	// "say so rather than inferring", and an agent that stops there reports
	// nothing about "Saathi backend" while saathi holds the work. Suggested,
	// not substituted — the name given is still the one resumed.
	if pack.Checkpoint == nil && strings.TrimSpace(projectArg) != "" && !s.projectExists(project) {
		if names, err := session.Scopes(s.vault); err == nil {
			if near := session.NameInside(project, names); near != "" {
				chose = fmt.Sprintf("_Did you mean %s? Nothing is filed under %s itself, and %s is the one known project whose name is inside it — call resume with project %q before reporting that nothing is recorded._\n\n",
					untrusted.Inline(near), untrusted.Inline(project), untrusted.Inline(near), near) + chose
			}
		}
	}
	out := chose + swept + s.lead(pack) + pack.Render()
	out += s.ledgerPack("mcp:resume", project, pack)
	if pack.Checkpoint == nil {
		// Say so plainly. An agent that assumes there was a checkpoint and
		// finds none will invent continuity that never existed.
		out += "\n_No checkpoint has been written for this project yet — " +
			"this is context, not a handoff. Call checkpoint before you stop._\n"
		// "Nothing recorded" is true of this name and false of the vault. An
		// agent told only the first stops; one told what exists recovers in a
		// single call.
		if known := s.knownProjects(); known != "" && !s.projectExists(project) {
			out += fmt.Sprintf("_Nothing in this vault is filed under %s%s._\n", untrusted.Inline(project), known)
		}
	}
	out += s.awaitingReview()
	// Filed under the scope the pack itself read, so the note lands in the same
	// session a checkpoint will later close — in this worktree, not in the
	// project the worktree belongs to. Not when the project was a guess: the
	// agent is standing somewhere else and will most likely work there, so the
	// note opened a session in another project's history that nothing closed.
	if scope := pack.Continuity(); strings.TrimSpace(agent) != "" && scope != "" && chose == "" {
		session.AddNote(s.DB, scope, agent, "resumed the project")
	}
	return out, nil
}

func (s *Server) noteProgress(project, agent, text string) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("note_progress needs a project and some text%s", s.knownProjects())
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("note_progress needs a project and some text")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	n, err := session.AddNote(s.DB, project, agent, text)
	if err != nil {
		return "", err
	}
	msg := s.receipt("noted in logos — uncommitted until you checkpoint")
	if said := secret.Summary(n.Redactions); said != "" {
		msg += " " + said + "."
	}
	return msg, nil
}

// receipt marks a line as ours so the person watching the transcript can find
// it without reading it. See internal/announce for why this is a setting and
// not a constant.
//
// It lives on Server rather than Session because the tools that write are split
// across both, and a receipt that appeared on half of them would be worse than
// none: an inconsistent marker teaches people the absence of a marker means
// nothing happened.
func (s *Server) receipt(what string) string {
	if r := announce.Say(s.vault, what); r != "" {
		return r
	}
	// At LOGOS_ANNOUNCE=off the model still needs to know what happened, even
	// though the user has asked not to be told about it. Silence towards the
	// user is not silence towards the caller.
	return upperFirst(what)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// agentFor names who wrote a checkpoint or note: the model's own agent
// argument when it gave one, otherwise the host's handshake name. Without the
// fallback an agent that skipped the optional argument was filed as "agent",
// and a handoff could not say which host stopped there.
func (s *Session) agentFor(args map[string]any) string {
	if a := argStr(args, "agent"); a != "" {
		// "claude" under claude-code is the host's name cut short, not another
		// agent; recording it as typed split one session's trail in two.
		if s.clientAgent != "" && strings.HasPrefix(s.clientAgent, strings.ToLower(a)+"-") {
			return s.clientAgent
		}
		return a
	}
	return s.clientAgent
}

// checkpoint commits the session to the vault. handoffTo is set when the caller
// came in through the handoff tool — same mechanism, stated intent.
func (s *Session) checkpoint(args map[string]any, handoffTo string) (string, error) {
	proj := s.resolveScope(argStr(args, "project"))
	if strings.TrimSpace(proj) == "" {
		return "", fmt.Errorf("checkpoint needs a project, and none could be inferred from the working directory%s", s.knownProjects())
	}
	key, _ := json.Marshal([]any{proj, handoffTo, args})
	// The file is checked too: a receipt for a checkpoint that is no longer on
	// disk would be a success-shaped failure.
	if last := s.lastCheckpoint; last.key == string(key) && time.Since(last.at) < checkpointRetryWindow &&
		checkpointOnDisk(s.vault, last.slug) {
		msg := s.receipt(fmt.Sprintf("checkpoint already saved to logos — %s.md; this identical retry was not written again", last.slug))
		if handoffTo != "" {
			msg += fmt.Sprintf(" Handed off to %s — they can call resume(%q).", handoffTo, proj)
		}
		return msg, nil
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	c := &session.Checkpoint{
		Project:   proj,
		Agent:     s.agentFor(args),
		Task:      argStr(args, "task"),
		Intent:    argStr(args, "intent"),
		State:     argStr(args, "state"),
		Decisions: argList(args, "decisions"),
		Failed:    argList(args, "failed"),
		Verified:  argList(args, "verified"),
		Blockers:  argList(args, "blockers"),
		Commands:  argList(args, "commands"),
		Questions: argList(args, "questions"),
		Files:     argList(args, "files"),
		Next:      argStr(args, "next"),
		HandoffTo: handoffTo,
	}
	var dropped int
	c.Failed, dropped = session.DropPlaceholders(c.Failed)
	// Read before the commit, so the history is what came before this checkpoint.
	earlier, _ := session.History(s.vault, c.Project, session.IntentDepth)
	if err := session.Commit(s.DB, s.vault, c); err != nil {
		return "", err
	}
	s.lastCheckpoint.key, s.lastCheckpoint.slug, s.lastCheckpoint.at = string(key), c.Slug, time.Now()
	s.checkpointed(c.Project)
	msg := s.receipt(fmt.Sprintf("checkpoint saved to logos — %s.md", c.Slug))
	if said := secret.Summary(c.Redactions); said != "" {
		msg += " " + said + "."
	}
	for _, said := range advice.Checkpoint(*c, earlier, dropped, advice.MCP) {
		msg += " " + said
	}
	if handoffTo != "" {
		msg += fmt.Sprintf(" Handed off to %s — they can call resume(%q).", handoffTo, c.Project)
	}
	// No "run `logos index`": resume and before_you_try read the checkpoint off
	// disk, so it is usable the moment this returns. See cmd/logos/session.go.
	return msg, nil
}
