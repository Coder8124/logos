package ingest

import (
	"regexp"
	"strings"

	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/text"
)

// Request is what a prompt asks for, as an auto record's task line, and false
// when it asks for nothing.
//
// Every host records some of its own text as the user's turn: Claude Code's
// <local-command-caveat> and slash-command envelope, Codex's AGENTS.md and
// environment, the summary that opens a continued session. Taken as the task,
// those were the line on real records that was meant to say what the session
// was for. The rule is mechanical on purpose — a model's reading of what the
// person meant is a claim, and this line is a quotation.
//
// The hook path keeps a prompt cut to one line of 160 characters, so a wrapper
// may have lost its closing tag; one that is never closed runs to the end.
func Request(prompt string) (string, bool) {
	for _, line := range personsLines(prompt) {
		// "Hi!" on a line of its own opens the request rather than replacing
		// it; the hook path folds the same prompt onto one line and keeps it.
		if asksNothing(line) {
			continue
		}
		return text.Ellipsize(line, 120), true
	}
	return "", false
}

// PersonWrote says whether anything in a user-role turn is the person's own,
// once the host's text is taken out. Unlike Request it keeps a bare "no": as
// an answer to the agent's question it is the person's whole reply, and often
// the correction itself.
//
// A bare slash command is a button, not a reply, and a line that was only a
// kept-out <private> span is something the person hid: a want cited to either
// would be the distiller's guess passed off as their words.
func PersonWrote(prompt string) bool { return PersonsText(prompt) != "" }

// PersonsText is what PersonWrote found: the person's own lines of a user-role
// turn, folded onto one, and "" when none of it is theirs. A paste the host
// marks as one is left out with the rest of the host's text, so this is what
// they wrote and not what they showed; a paste the host does not mark cannot
// be told from typing and stays in.
func PersonsText(prompt string) string { return strings.Join(PersonsLines(prompt), " ") }

// PersonsLines is PersonsText before it is folded: one entry to each line the
// person wrote, for a reader that cares where their lines broke — an error
// pasted from a terminal keeps its shape only here.
func PersonsLines(prompt string) []string {
	var kept []string
	for _, line := range personsLines(prompt) {
		if strings.HasPrefix(line, "/") && !strings.ContainsAny(line, " \t") {
			continue
		}
		if onlyKeptOut(line) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// PastedLines is what the person pasted into a turn, where the host marks a
// paste as one: the lines PersonsLines leaves out. It is what they showed, and
// for a reader asking what came back from a terminal that is the turn.
func PastedLines(prompt string) []string {
	var out []string
	for _, m := range pasted.FindAllStringSubmatch(prompt, -1) {
		for _, line := range strings.Split(m[1], "\n") {
			if line = strings.Join(strings.Fields(line), " "); line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// personsLines is what is left of a prompt once the host's text is taken out,
// one non-empty line each, whitespace folded.
func personsLines(prompt string) []string {
	if strings.HasPrefix(strings.TrimSpace(prompt), continuedSession) {
		return nil
	}
	if m := commandName.FindStringSubmatch(prompt); m != nil {
		args := ""
		if a := commandArgs.FindStringSubmatch(prompt); a != nil {
			args = strings.TrimSpace(a[1])
		}
		// A command with no arguments says which button was pressed, not what
		// for: /clear, /logos:context-connect.
		if args == "" {
			return nil
		}
		prompt = m[1] + " " + args
	}
	for _, w := range hostWrappers {
		prompt = w.ReplaceAllString(prompt, " ")
	}
	prompt = imageMarker.ReplaceAllString(prompt, " ")
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
		// The host's note that the person pressed escape. It marks a turn they
		// cut short and is not a word they wrote.
		if strings.HasPrefix(strings.TrimSpace(line), interrupted) {
			continue
		}
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// FirstRequest is the first of prompts that asks for something. The first is
// the only one reliably the person's own: later ones may be answers to the
// agent's questions.
func FirstRequest(prompts []string) string {
	for _, p := range prompts {
		if r, ok := Request(p); ok {
			return r
		}
	}
	return ""
}

const continuedSession = "This session is being continued from a previous conversation"

// interrupted opens the line Claude Code writes in the person's turn when they
// stop the agent: "[Request interrupted by user]", "… for tool use]".
const interrupted = "[Request interrupted by user"

var (
	commandName = regexp.MustCompile(`<command-name>\s*(/?[^<\s]+)\s*(?:</command-name>|$)`)
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)(?:</command-args>|$)`)
	pasted      = regexp.MustCompile(`(?s)<pasted_content\b[^>]*>(.*?)(?:</pasted_content>|$)`)
	// imageMarker is where Claude Code shows an attached picture: "[Image #1]".
	imageMarker = regexp.MustCompile(`\[Image #\d+\]`)
	// hostWrappers are blocks a host writes into the user's turn. A paste is
	// the person's, but it is what they showed, not what they asked; the
	// words around it are the request.
	hostWrappers = func() []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, tag := range []string{
			"local-command-caveat", "local-command-stdout", "local-command-stderr",
			"command-name", "command-message", "command-args",
			"system-reminder", "environment_context", "user_instructions",
			"pasted_content",
			// A command the person ran themselves with "!" and what it printed,
			// and a background task reporting in. The command is theirs, but it
			// was typed at the shell and asks the agent nothing: as a task line
			// it read "<bash-input>pwd</bash-input>", and its output counted as
			// words they wrote.
			"bash-input", "bash-stdout", "bash-stderr", "task-notification",
		} {
			out = append(out, regexp.MustCompile(`(?s)<`+tag+`\b[^>]*>.*?(?:</`+tag+`>|$)`))
		}
		return out
	}()
)

// onlyKeptOut is a line that was nothing but <private> spans the person hid.
func onlyKeptOut(line string) bool {
	return strings.TrimSpace(strings.ReplaceAll(line, secret.PrivateMarker, "")) == ""
}

// asksNothing is a line that opens or acknowledges rather than asks: a
// greeting, a yes, a thanks, a bare slash command with nothing after it, or a
// span the person kept out — which as a task would tell the next agent they
// asked for "[private]".
func asksNothing(line string) bool {
	if strings.HasPrefix(line, "/") && !strings.ContainsAny(line, " \t") {
		return true
	}
	if onlyKeptOut(line) {
		return true
	}
	l := strings.ToLower(strings.TrimRight(line, ".!? "))
	switch l {
	case "hi", "hello", "hey", "yes", "y", "no", "ok", "okay", "k", "sure",
		"continue", "go", "go on", "proceed", "thanks", "thank you", "ty":
		return true
	}
	return false
}
