package main

import (
	"github.com/Coder8124/brain/internal/memory"
)

// The visible face of persistent memory: what the assistant has learned about
// you, and the ability to take any of it back. These were filed under the
// business panel only because that panel happened to render the memory chip —
// they are core memory bindings and belong on their own.

// Memories returns what the assistant has learned about the user.
func (a *App) Memories() ([]memory.Memory, error) {
	ix, err := a.open()
	if err != nil {
		return nil, err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return nil, err
	}
	return memory.All(ix.DB)
}

// ForgetMemory drops one remembered fact. Forgetting has to be as easy as
// remembering, or the memory stops being something you own.
func (a *App) ForgetMemory(id int64) error {
	ix, err := a.open()
	if err != nil {
		return err
	}
	defer ix.Close()
	return memory.Forget(ix.DB, id)
}
