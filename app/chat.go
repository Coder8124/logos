package main

import (
	"github.com/Coder8124/brain/internal/agent"

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
		// next session start already knowing the user.
		if n, _ := agent.Learn(ix.DB, rt, conversation); n > 0 {
			runtime.EventsEmit(a.ctx, "memory:learned", n)
		}
	}()
}

// History returns the conversation so the UI can restore it when reopened.
func (a *App) History() []agent.Turn {
	return conversation.Turns
}

// ResetChat clears the conversation.
func (a *App) ResetChat() {
	conversation.Turns = nil
}
