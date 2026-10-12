package main

import (
	"fmt"
	"net/netip"
	"net/url"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/selfupdate"
	"github.com/Coder8124/logos/internal/setup"
)

// doctorEgress lists every address this binary is configured to contact, and
// when. TestOnlyTheListedFilesCanOpenANetworkConnection pins the code that can
// open a connection, so a new client fails the build until it is listed here.
func doctorEgress() error {
	fmt.Println("Every address this logos can contact, and when:")
	fmt.Println()

	fmt.Println("  Model runtime — embeddings, distilling, every model call")
	switch {
	case provider.Off():
		fmt.Println("      none: LOGOS_RUNTIME=off, no runtime is contacted")
	case provider.Configured() != nil:
		u := provider.Configured().BaseURL
		where := "this machine"
		if !loopbackURL(u) {
			where = "not this machine: what you embed and distil is sent there"
		}
		fmt.Printf("      %s  (LOGOS_RUNTIME; %s)\n", u, where)
	default:
		for _, ep := range provider.LocalEndpoints {
			fmt.Printf("      %-28s %s, probed on this machine\n", ep.URL, ep.Name)
		}
	}

	cfg, err := router.Load(vaultPath())
	if err != nil {
		return fmt.Errorf("reading the model config: %w", err)
	}
	t3 := cfg.Tiers["T3"]
	state := "off"
	if t3.CloudOK {
		state = "marked enabled in .logos/config.json, though no command in this version calls it"
	}
	fmt.Printf("  The cloud tier (T3, %s) — %s\n", t3.Model, state)
	if t3.BaseURL != "" {
		fmt.Printf("      %s is configured and never contacted\n", t3.BaseURL)
	}

	fmt.Println("  `logos update` — only when you run it")
	fmt.Printf("      %s, then the release download from github.com and *.githubusercontent.com\n", selfupdate.ReleasesFrom)

	fmt.Println("  `logos setup` — only when you run it")
	fmt.Println("      the runtime's /api/pull, if you agree to download a model (the runtime fetches it)")
	fmt.Printf("      `claude plugin marketplace add %s`, which Claude Code fetches from GitHub\n", setup.PluginSource)

	auto := "on"
	if setup.AutoUpdateDisabled() {
		auto = "off"
	}
	fmt.Printf("  Plugin auto-update — %s (`logos plugin autoupdate off|on`)\n", auto)
	fmt.Println("      `claude plugin marketplace update` + `claude plugin update`, at most once a day,")
	fmt.Println("      when the plugin is behind this logos; Claude Code makes the request")

	// Not logos's requests, but ones running logos causes: the chaos-tier proxy
	// caught `logos doctor` reaching api.anthropic.com through `claude mcp list`
	// while this list said nothing of it. Every command that reads or changes a
	// host's registration runs them, not only doctor and setup.
	fmt.Println("  Your agent hosts' own CLIs — when `logos doctor`, `logos setup`, `logos mcp install`,")
	fmt.Println("  `logos mcp uninstall` or `logos migrate` runs them")
	fmt.Println("      `claude mcp list`, `claude mcp add`, `claude plugin --help`, `codex mcp …` and the like,")
	fmt.Println("      to read or change each host's registration; those programs make their own requests (Claude Code reaches")
	fmt.Println("      api.anthropic.com when it starts), none of them carrying anything from your vault")

	fmt.Println("  `logos mcp serve-http` — listens on 127.0.0.1 only, and contacts nothing")
	fmt.Println()
	fmt.Println("There are no others: no telemetry, no crash reports, no usage pings.")
	fmt.Println("To watch for yourself: `lsof -i -a -c logos` while it runs (add `-c claude` for the host CLIs),")
	fmt.Println("or a firewall like Little Snitch.")
	return nil
}

// loopbackURL is whether a runtime URL names this machine. A hostname other
// than localhost is not resolved: one that happens to resolve locally today is
// still a name someone else's DNS can answer.
func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.IsLoopback()
}
