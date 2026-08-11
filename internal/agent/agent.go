// Package agent is the conversational assistant.
//
// The rest of the system is retrieval and automation; this is the part that
// talks back. It grounds each reply in the vault (so it answers from what you
// actually know) and in the live brief (so it knows what is on your plate right
// now), and it remembers the conversation — which is the difference between an
// assistant and a search box.
package agent

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/secretary"
)

// Turn is one message in the conversation.
type Turn struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// Conversation holds the running dialogue. Kept in memory on the app side; a
// second brain's conversations are ephemeral working context, not vault content
// — anything worth keeping becomes a note through the review queue.
type Conversation struct {
	Turns []Turn
}

// maxHistory bounds how many prior turns we resend. Enough for continuity,
// short enough to leave room for retrieved context on a small local model.
const maxHistory = 8

// persona is the assistant's voice. There was one of these per vertical; there
// is one assistant now, so there is one voice.
func persona() string {
	return "You are a calm, capable personal assistant — a second brain. " +
		"You are proactive and concrete, and you speak plainly."
}

// Reply streams the assistant's response to the latest user message.
//
// It assembles three things into the prompt: who it should be, what it
// already knows that is relevant (vault retrieval), and what is currently on the
// user's plate (the brief). Then the conversation history, then the question.
// onToken receives text as it generates, so the UI fills in live.
func Reply(
	db *sql.DB, ix *index.Index, rt *router.Router,
	conv *Conversation, userMsg string,
	onToken func(string),
) (string, error) {
	conv.Turns = append(conv.Turns, Turn{Role: "user", Content: userMsg})

	// --- retrieve vault context for this question ---
	embed, _ := rt.Model(router.T0)
	var grounding strings.Builder
	if hits, err := ix.HybridSearch(rt.Local(), embed, userMsg, 4); err == nil {
		for _, h := range hits {
			chunk := fmt.Sprintf("## %s [%s]\n%s\n\n", h.Title, h.Slug, strings.TrimSpace(h.Body))
			if grounding.Len()+len(chunk) > 4000 {
				break
			}
			grounding.WriteString(chunk)
		}
	}

	// --- what the assistant already knows about the user (persistent memory) ---
	// This is the continuity that makes it a second brain rather than a chatbot:
	// preferences, people, and standing context recalled from past sessions so
	// the assistant does not ask twice and can act on what it has learned.
	var remembered string
	if err := memory.Init(db); err == nil {
		if mems, err := memory.Recall(db, rt.Local(), embed, userMsg, 5); err == nil {
			remembered = memory.Render(mems)
		}
	}

	// --- what's on the user's plate right now ---
	var context strings.Builder
	if b, err := secretary.Compose(db, time.Now()); err == nil {
		if len(b.Upcoming) > 0 {
			m := b.Upcoming[0]
			fmt.Fprintf(&context, "Next up: %s in %d min. ", m.Title, m.InMin)
		}
		if len(b.Loops) > 0 {
			var loops []string
			for i, l := range b.Loops {
				if i >= 3 {
					break
				}
				loops = append(loops, l.Text)
			}
			fmt.Fprintf(&context, "Open loops: %s.", strings.Join(loops, "; "))
		}
	}

	// --- build the message list ---
	system := persona() + "\n\n" +
		"You have up to three sources below. Treat 'what you know about the user' as " +
		"established facts about them — use them directly and never claim you have no " +
		"information about something that is listed there. Answer from the vault notes " +
		"when relevant, citing the note slug in square brackets. If nothing below covers " +
		"the question, say so and answer from general knowledge, clearly. Be brief unless " +
		"asked to go deep."
	if remembered != "" {
		system += "\n\n--- what you know about the user ---\n" + remembered
	}
	if grounding.Len() > 0 {
		system += "\n--- relevant notes ---\n" + grounding.String()
	}
	if context.Len() > 0 {
		system += "\n--- on the user's plate ---\n" + context.String()
	}

	messages := []provider.Msg{{Role: "system", Content: system}}
	// Replay recent history (excluding the just-appended user turn, added last).
	hist := conv.Turns
	if len(hist) > maxHistory {
		hist = hist[len(hist)-maxHistory:]
	}
	for _, t := range hist {
		messages = append(messages, provider.Msg{Role: t.Role, Content: t.Content})
	}

	chat, err := rt.Model(router.T2)
	if err != nil {
		return "", err
	}

	full, err := rt.Local().ChatStream(chat, messages, onToken)
	if err != nil {
		return "", err
	}
	conv.Turns = append(conv.Turns, Turn{Role: "assistant", Content: full})
	return full, nil
}

// Learn extracts durable memories from the most recent exchange and persists
// them, so the next session starts already knowing them. Kept separate from
// Reply so the caller controls timing and database lifetime — it runs after the
// reply is delivered, and a failure to learn never affects the answer. Returns
// how many new memories were stored.
func Learn(db *sql.DB, rt *router.Router, conv *Conversation) (int, error) {
	if len(conv.Turns) < 2 {
		return 0, nil
	}
	if err := memory.Init(db); err != nil {
		return 0, err
	}
	last := conv.Turns[len(conv.Turns)-2:] // the user turn and the reply
	exchange := last[0].Role + ": " + last[0].Content + "\n" + last[1].Role + ": " + last[1].Content
	return memory.Learn(db, rt, exchange, "conversation")
}
