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
		return fmt.Errorf("usage: logos plugin autoupdate [on|off|status] [--notice]")
	}
	// Software that changes itself needs a way to be told not to. The check is
	// bounded and announced, which makes it defensible, but a user who does not
	// want their editor's plugins moving under them is not asking for a better
	// announcement — they are asking for it to stop.
	if len(args) > 1 {
		switch args[1] {
		case "off":
			if err := setup.SetAutoUpdate(false); err != nil {
				return err
			}
			fmt.Println("plugin auto-update off — Logos will not move its Claude Code plugin.")
			fmt.Println("update it yourself with `claude plugin marketplace update logos && claude plugin update logos@logos`, or turn this back on with `logos plugin autoupdate on`.")
			return nil
		case "on":
			if err := setup.SetAutoUpdate(true); err != nil {
				return err
			}
			fmt.Println("plugin auto-update on — Logos checks once a day, at session start, and says so when it moves the plugin.")
			// Saying so here rather than letting the setting read as taking
			// effect: the environment wins, and a user who turned it on and saw
			// nothing happen would have no way to find out why.
			if setup.AutoUpdateDisabled() {
				fmt.Println("but LOGOS_PLUGIN_AUTOUPDATE is set to off in this environment, which wins — unset it for this to take effect.")
			}
			return nil
		case "status":
			if setup.AutoUpdateDisabled() {
				fmt.Println("plugin auto-update: off")
			} else {
				fmt.Println("plugin auto-update: on — once a day at session start, announced in the next session")
			}
			return nil
		}
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
