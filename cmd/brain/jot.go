package main

import (
	"fmt"
	"strings"

	"github.com/pragun/brain/internal/rollup"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/secretary"
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
