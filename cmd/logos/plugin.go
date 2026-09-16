package main

import (
	"fmt"
	"time"

	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/setup"
)

// `logos plugin autoupdate` is what keeps the Claude Code plugin level with
// this binary. The session-start hook runs it detached, so the hook itself
// never waits on the network, and reads the result of the *previous* run with
// --notice — which is the only reason the update is ever visible to anyone.
func pluginCmd(args []string) error {
	if len(args) == 0 || args[0] != "autoupdate" {
		return fmt.Errorf("usage: logos plugin autoupdate [--notice]")
	}
	if hasFlag(args, "--notice") {
		if n := setup.UpdateNotice(); n != "" {
			fmt.Println(n)
		}
		return nil
	}
	// The failure is returned, so a person running this by hand sees it; the
	// hook backgrounds the command and reads the recorded failure next session.
	_, err := setup.AutoUpdatePlugin(buildinfo.Version, time.Now())
	return err
}
