package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/rollup"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/secretary"
)

// Braindump: the shortest path from a thought to the vault. These lived in the
// old persona file only because that is where the flavor commands were; they are
// core memory input and have nothing to do with personas.

func openRouter() (*router.Router, error) {
	cfg, err := router.Load(vaultPath())
	if err != nil {
		return nil, err
	}
	return router.New(cfg, vaultPath())
}

// openRouterOptional is openRouter for the commands that do not actually need a
// model. A missing runtime comes back as (nil, nil) and the caller carries on
// degraded; a malformed config is still an error, because that is a mistake the
// user can fix and silently ignoring it would hide it.
//
// The distinction matters most for `mcp serve`. A host launches it in the
// background and shows the user a connection failure, not our message — so
// refusing to start over an absent model is how "brain has no embeddings here"
// becomes "brain is broken".
func openRouterOptional() (*router.Router, error) {
	rt, err := openRouter()
	if errors.Is(err, router.ErrNoRuntime) {
		return nil, nil
	}
	return rt, err
}

func jotCmd(text string) error {
	if text == "" {
		return fmt.Errorf("usage: brain jot <thought>")
	}
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := rollup.InitQueue(ix.DB); err != nil {
		return err
	}
	if err := secretary.Init(ix.DB); err != nil {
		return err
	}
	rt, err := openRouter()
	if err != nil {
		return err
	}

	prop, kind, err := rollup.Braindump(ix.DB, rt, text)
	if err != nil {
		return err
	}
	if kind == "task" {
		// A task becomes an open loop directly — that is where the secretary
		// looks, and it is exactly the "thing I need to do" case.
		secretary.Add(ix.DB, &secretary.Commitment{Text: strings.TrimSpace(text)})
		fmt.Println("filed as an open loop — see `brain brief`")
		return nil
	}
	fmt.Printf("filed as %s → %s — `brain review` to confirm\n", kind, prop.Target)
	return nil
}
