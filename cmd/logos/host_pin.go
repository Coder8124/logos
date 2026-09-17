package main

import (
	"os"

	"github.com/Coder8124/logos/internal/setup"
)

const (
	// logosHostEnv names the host a run is happening inside. Only the plugin's
	// hooks set it; a shell never does.
	logosHostEnv  = "LOGOS_HOST"
	logosVaultEnv = "LOGOS_VAULT"
)

// adoptHostPin gives a run inside a host the vault that host's config pins on
// its logos server, when nothing more specific was asked for.
//
// #95: setup records the vault twice — as LOGOS_VAULT in each host's MCP
// config, and once for the machine as the recorded pointer. Tool calls arrive
// through the host and get the pin; the plugin's hooks run with a bare
// environment and get the pointer. When the two disagree, a single session
// restores from one vault and checkpoints into another, each side silently
// consistent with itself, and the user reports that their work stopped being
// saved. Inside a host the pin is the more specific answer, so a hook takes it.
//
// Three deliberate limits. An explicit LOGOS_VAULT still wins, because that is
// somebody naming a vault for this one process and demoting it to a suggestion
// would break every scratch run. A pin naming a directory that is not there is
// ignored: a stale config would otherwise move the hooks onto a path logos
// creates and then reports as a healthy zero (#96), and the pointer is the
// safer answer. And nothing happens without LOGOS_HOST, so a plain `logos`
// in a terminal never reads a config file to answer a question it already has.
// adoptedPin is the vault adoptHostPin put into the environment, so a later
// caller can tell it apart from a LOGOS_VAULT somebody typed. `logos setup`
// reads a set LOGOS_VAULT as "this process only" and records no machine
// pointer; without this, running setup inside a host made it decline to do the
// one thing it is for, citing a variable the user never set.
var adoptedPin string

func adoptHostPin(hosts []setup.Host) {
	adoptedPin = ""
	name := os.Getenv(logosHostEnv)
	if name == "" || os.Getenv(logosVaultEnv) != "" {
		return
	}
	pin := expandHome(setup.PinnedVault(hosts, name))
	if pin == "" {
		return
	}
	if fi, err := os.Stat(pin); err != nil || !fi.IsDir() {
		return
	}
	os.Setenv(logosVaultEnv, pin)
	adoptedPin = pin
}

// vaultCameFromHostPin reports whether a set LOGOS_VAULT is this process's own
// adopted pin rather than a choice the user made for this run.
func vaultCameFromHostPin(v string) bool {
	return v != "" && v == adoptedPin
}
