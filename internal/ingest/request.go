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
func PersonWrote(prompt string) bool {
	for _, line := range personsLines(prompt) {
		if strings.HasPrefix(line, "/") && !strings.ContainsAny(line, " \t") {
			continue
		}
		if onlyKeptOut(line) {
			continue
		}
		return true
	}
	return false
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
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
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

var (
	commandName = regexp.MustCompile(`<command-name>\s*(/?[^<\s]+)\s*(?:</command-name>|$)`)
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)(?:</command-args>|$)`)
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
