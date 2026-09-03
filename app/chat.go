package main

import (
	"time"

	"github.com/Coder8124/brain/internal/agent"
	"github.com/Coder8124/brain/internal/consent"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Chat is the conversational surface — the part that makes this feel like an
// assistant that responds rather than an archive you query.
//
// The conversation lives on the app for the session. Replies stream: each token
// is emitted as a "chat:token" event so the frontend fills the bubble in live,
// and "chat:done" / "chat:error" close it out. A 40-second local generation that
// appears word by word reads as thinking; the same answer in one silent lump
// read as broken, which is exactly what prompted this.

var conversation = &agent.Conversation{}

// Send streams a reply to a user message. It returns immediately; the answer
// arrives over events.
func (a *App) Send(message string) {
	go func() {
		ix, err := a.open()
		if err != nil {
			runtime.EventsEmit(a.ctx, "chat:error", err.Error())
			return
		}
		defer ix.Close()

		rt, err := a.router()
		if err != nil {
			runtime.EventsEmit(a.ctx, "chat:error", err.Error())
			return
		}

		_, err = agent.Reply(ix.DB, ix, rt, conversation, message,
			func(tok string) { runtime.EventsEmit(a.ctx, "chat:token", tok) })
		if err != nil {
			runtime.EventsEmit(a.ctx, "chat:error", err.Error())
			return
		}
		runtime.EventsEmit(a.ctx, "chat:done", "")

		// The reply is delivered; now quietly learn any durable facts from it,
		// while this handle is still open. Persistent memory is what makes the
		// next session start already knowing the user — but writing to that
		// memory without asking is the exact thing Stage 4 exists to stop.
		// consent.Allowed reports whether the user has already said "go ahead"
		// (a one-off grant or a timed "stop asking for an hour"); if not, skip
		// Learn entirely and tell the frontend to ask, rather than learning
		// silently and rather than blocking the reply on a prompt no one has
		// answered yet.
		if consent.Allowed() {
			if n, _ := agent.Learn(ix.DB, rt, conversation); n > 0 {
				runtime.EventsEmit(a.ctx, "memory:learned", n)
			}
		} else {
			runtime.EventsEmit(a.ctx, "memory:consent-needed", "")
		}
	}()
}

// GrantLearning allows automatic learning from chat without asking again,
// for the next `minutes` minutes. minutes <= 0 means for the rest of this
// run — see consent.Grant. This is the backend half of "disable asks for an
// hour and allow all writes": the frontend calls it once the user answers
// the consent prompt, and every Send after that skips the prompt until the
// grant runs out.
func (a *App) GrantLearning(minutes int) {
	consent.Grant(time.Duration(minutes) * time.Minute)
}

// RevokeLearning withdraws any standing grant, so the next chat exchange
// asks again before it learns.
func (a *App) RevokeLearning() {
	consent.Revoke()
}

// History returns the conversation so the UI can restore it when reopened.
func (a *App) History() []agent.Turn {
	return conversation.Turns
}

// ResetChat clears the conversation.
func (a *App) ResetChat() {
	conversation.Turns = nil
}
