package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/mcpserver"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/secretary"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/setup"
)

func doctor(probe, verbose bool) error {
	// The product first, the model plumbing second. This used to be the other
	// way round — and in fact only ever reported the plumbing, so a vault that
	// did not exist and an index a week stale both passed silently.
	//
	// It also used to return an error when no runtime answered, which made the
	// one command a confused user reaches for refuse to run precisely when
	// something was wrong.
	rep := gatherHealth()
	fmt.Println("─── logos ───")
	// Width from the longest check name rather than a constant. "abandoned
	// sessions" is eighteen characters and used to push its own state out of the
	// column every other row lined up in, which reads as a rendering bug in the
	// one command someone runs when they already suspect something is wrong.
	w := 0
	for _, c := range rep.Checks {
		if len(c.Name) > w {
			w = len(c.Name)
		}
	}
	for _, c := range leadWith(rep.Checks, "vault", "agent hosts", "continuity") {
		fmt.Printf("  %-*s %s\n", w, c.Name, renderState(c.State))
		if c.Detail != "" {
			fmt.Printf("  %-*s   %s\n", w, "", c.Detail)
		}
		if c.Fix != "" {
			fmt.Printf("  %-*s   → %s\n", w, "", c.Fix)
		}
	}
	ok, warn, failed, unknown := rep.Counts()
	fmt.Printf("\n  %d ok · %d to do · %d failed · %d unchecked\n", ok, warn, failed, unknown)

	// Runtimes, tiers and the web bridge are for `ask`, the rollup and the
	// browser extension. No continuity tool uses them, and printed by default
	// they were most of the report and made logos look like it needed a model.
	if !verbose && !probe {
		fmt.Println("\nrun `logos doctor --verbose` for the web bridge, model runtimes and tiers")
		return doctorVerdict(failed)
	}

	if mcpserver.HasToken(vaultPath()) {
		fmt.Println("\nweb bridge: paired — `logos mcp serve --http` will reuse the existing token")
	} else {
		fmt.Println("\nweb bridge: not paired — `logos mcp serve --http` will mint a token on first run")
	}

	found := provider.Resolve()
	if len(found) == 0 && provider.Configured() != nil {
		// The user named a runtime and it is down. The check above already
		// failed on it; closing on "nothing depends on one" would tell them
		// to ignore the one failure they asked for.
		fmt.Printf("\nLOGOS_RUNTIME names %s, and it did not answer — search is lexical until it does.\n", provider.Configured().BaseURL)
		return doctorVerdict(failed)
	}
	if len(found) == 0 {
		// Not an error. Every continuity tool works without a model, and search
		// falls back to lexical; the report above already said so.
		fmt.Println("\nNo local model runtime — nothing above depends on one.")
		return doctorVerdict(failed)
	}
	fmt.Println("\n─── runtimes ───")
	for _, d := range found {
		fmt.Printf("%s — %s\n", d.Provider.Name, d.Provider.BaseURL)
		for _, m := range d.Models {
			fmt.Printf("    %s\n", m)
		}
	}

	cfg, err := router.Load(vaultPath())
	if err != nil {
		return err
	}
	rt, err := router.New(cfg, vaultPath())
	if err != nil {
		return err
	}

	fmt.Println("\n─── tiers ───")
	for _, line := range rt.Available() {
		fmt.Println(" ", line)
	}

	if !probe {
		fmt.Println("\nrun `logos doctor --probe` to verify each model actually loads")
		return doctorVerdict(failed)
	}

	// Listing a model proves nothing: a corrupt pull lists fine and fails on
	// load. Probing is what catches it before a rollup does at 3am.
	//
	// And a model that fails to load counts towards the verdict, or the probe
	// is the one check whose result nothing can act on: `failed` is totalled
	// before this loop runs, so `logos doctor --probe && deploy` used to print
	// FAILS TO LOAD in red and then exit 0 into the next command.
	fmt.Println("\n─── probe ───")
	for _, t := range []router.Tier{router.T1, router.T2} {
		model, err := rt.Model(t)
		if err != nil {
			fmt.Printf("  %s  %v\n", t, err)
			continue
		}
		line, broken := probeRow(t, model, rt.Probe(model))
		fmt.Println(line)
		if broken {
			failed++
		}
	}
	return doctorVerdict(failed)
}

// leadWith moves the named checks to the front, in that order, and keeps the
// rest as they were: what a coding-agent user runs doctor for is whether the
// vault is there, whether their agents are wired to it, and where the last
// session stopped.
func leadWith(checks []health.Check, names ...string) []health.Check {
	out := make([]health.Check, 0, len(checks))
	for _, n := range names {
		for _, c := range checks {
			if c.Name == n {
				out = append(out, c)
			}
		}
	}
	for _, c := range checks {
		if !slices.Contains(names, c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// probeRow renders one probe result and says whether it counts as a failure.
// It is a function of its own so the verdict can be tested without a live model
// runtime: the bug it exists to stop — FAILS TO LOAD printed in the report while
// the command exits 0 — is only visible where the row and the count are decided
// together.
//
// A model that loads but ignores JSON schemas is not a failure. Every tier
// degrades to prose in that case, which is worse output, not a broken install.
func probeRow(t router.Tier, model string, cap router.Capability) (line string, failed bool) {
	switch {
	case !cap.Loads:
		return fmt.Sprintf("  %s  %-24s FAILS TO LOAD — %s", t, model, truncate(cap.Err, 70)), true
	case !cap.StructuredOutput:
		return fmt.Sprintf("  %s  %-24s loads, but ignores JSON schemas", t, model), false
	default:
		return fmt.Sprintf("  %s  %-24s ok, honours JSON schemas", t, model), false
	}
}

// doctorVerdict turns the report into an exit code. The rows already say what
// is wrong in words; this is for everything that reads the status instead — a
// pre-flight check, a CI step, a shell `&&`. An unchecked row is not a failure:
// doctor deliberately does not fail because no model runtime answered, since
// every continuity verb works without one.
func doctorVerdict(failed int) error {
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%d check(s) failed — see the report above", failed)
}

// doctorIntegration is the difference between "logos is installed" and "your
// agents can reach this vault". It is the same probe setup runs, exposed so it
// can be re-run after a host update or a config edit.
func doctorIntegration() error {
	vault := vaultPath()
	// The same description setup writes into every host config, so this check
	// launches what the hosts launch — under npx that is the `npx` command, not
	// the cached binary this process happens to be running from.
	srv, err := logosServer(vault)
	if err != nil {
		return err
	}

	self, err := selfPath()
	if err != nil {
		return err
	}

	// What the hosts have registered, not what this process happens to be
	// running from (#89). Someone who moved the binary onto their PATH, as the
	// end of setup told them to, left every host naming a file that is gone —
	// and this check said "Working", because it rebuilt the command from the
	// binary it found itself in. The question being asked is whether the
	// agents can reach the vault, and only their own entries can answer it.
	targets, unreadable := registeredTargets(vault, srv)
	failed := len(unreadable)
	for _, u := range unreadable {
		fmt.Printf("─── integration ───\n  host    %s\n  its registrations could not be read, so nothing here says whether it reaches this vault\n\n", u)
	}
	for i, t := range targets {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("─── integration ───\n  host    %s\n  binary  %s %s\n  vault   %s\n",
			t.host, t.srv.Bin, strings.Join(t.srv.Args, " "), t.vault)
		probeBin, probeArgs, note := probeTarget(self, t.srv)
		if note != "" {
			fmt.Printf("  note    %s\n", note)
		}
		fmt.Println()
		for _, c := range integrationChecks(probeBin, probeArgs, t.vault) {
			fmt.Printf("  %-12s %s\n", c.Name, renderState(c.State))
			if c.Detail != "" {
				fmt.Printf("  %-12s   %s\n", "", c.Detail)
			}
			if c.Fix != "" {
				fmt.Printf("  %-12s   → %s\n", "", c.Fix)
			}
			if c.State == health.Failed {
				failed++
			}
		}
	}
	if failed > 0 {
		// Named by count, not by "no host": one unreadable config among several
		// that read perfectly well sent the user to look for a permission
		// problem that was not there.
		if len(unreadable) > 0 {
			return fmt.Errorf("%d host(s) could not say what they have registered", len(unreadable))
		}
		return fmt.Errorf("integration is not working")
	}
	fmt.Println("\n  Working. The hosts launching these commands reach this vault.")
	return nil
}

// probe is one command to launch and the vault it is expected to reach, named
// by whoever registered it.
type probe struct {
	host  string
	srv   setup.Server
	vault string
}

// registeredTargets is the logos entry each detected host actually holds, and
// separately the hosts that could not be asked. A host with no logos in its
// config contributes nothing — it is not wired, so there is no wiring to check.
// A host whose config cannot be read is not that: it is the question going
// unanswered, so it is returned to be reported rather than dropped.
//
// Falling back to the command setup would write is what makes this check usable
// on a machine with no host registered yet: without it, `doctor --integration`
// on a fresh install would have nothing to probe and would report success by
// having asked nothing.
func registeredTargets(vault string, srv setup.Server) ([]probe, []string) {
	var out []probe
	var unreadable []string
	seen := map[string]bool{}
	for _, h := range detectHosts() {
		if h.List == nil || (h.Detect != nil && !h.Detect()) {
			continue
		}
		regs, err := h.List()
		if err != nil {
			// A host that cannot say what it has registered is not a host with
			// nothing registered. Swallowing this left the fallback probing
			// the command setup would write and the check closing "Working",
			// having failed to ask the only question it exists to ask.
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", h.Name, err))
			continue
		}
		for _, r := range regs {
			if !strings.Contains(r.Command, "mcp serve") {
				continue
			}
			// Split on spaces, which is how the command was joined. A binary
			// path with a space in it is not reconstructed, and lands as a
			// command that fails to launch — visibly, which is the point.
			fields := strings.Fields(r.Command)
			v := r.Vault
			if v == "" {
				v = vault
			}
			// Keyed by the vault as well as the command: two hosts commonly
			// register the same binary against different vaults, and that
			// split is the thing this check exists to catch. Keyed by command
			// alone, the second host's vault was never probed.
			key := r.Command + "\x00" + v
			if len(fields) == 0 || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, probe{h.Name, setup.Server{Bin: fields[0], Args: fields[1:]}, v})
		}
	}
	if len(out) == 0 && len(unreadable) == 0 {
		return []probe{{"none registered — probing what setup would write", srv, vault}}, nil
	}
	return out, unreadable
}

// gatherHealth assembles what the checks need, tolerating every piece of it
// being missing. A vault that will not open, an index that is not there and a
// runtime that is not running each become Unknown rather than an early return —
// the point of the report is to work when things are broken.
func gatherHealth() health.Report {
	vault := vaultPath()
	in := health.Input{Vault: vault, EmbedModel: env("LOGOS_EMBED", defaultEmbedModel), Hosts: setup.Hosts(), Version: buildinfo.Version}
	if self, err := selfPath(); err == nil {
		in.Self = self
	}

	// Stat before opening, because index.Open creates <vault>/.logos and that
	// brings the vault itself into existence. Opening it here meant doctor made
	// the vault it was about to check and then pronounced it healthy — the
	// "does not exist" branch in checkVault could not fire from the CLI at all.
	// A mistyped LOGOS_VAULT, or doctor run before setup, produced a second
	// empty vault with a clean bill of health, which is exactly the "healthy
	// zero of everything" that internal/vault/path.go exists to prevent.
	//
	// A vault that exists but has never been indexed is a different case, and
	// index.Open creating .logos for that one is wanted.
	if _, err := os.Stat(vault); err == nil {
		if ix, err := index.Open(vault); err == nil {
			defer ix.Close()
			session.Init(ix.DB) // so the abandonment check reads a table rather than an error
			// And so durability counts loops and insights rather than skipping them
			// as stores this vault never used.
			secretary.Init(ix.DB)
			dream.InitQueue(ix.DB)
			in.DB = ix.DB
		}
	}
	// Resolve, not Discover: doctor has to report the runtime the server will
	// use, and with LOGOS_RUNTIME set that is never whatever is on localhost.
	if found := provider.Resolve(); len(found) > 0 {
		in.Runtime = found[0].Provider
	}
	in.Configured = os.Getenv("LOGOS_RUNTIME")

	return health.Run(in)
}

func renderState(s health.State) string {
	switch s {
	case health.OK:
		return "ok"
	case health.Warn:
		// Lower case and unshouted on purpose: this row is a chore waiting for
		// the user, and rendering it the way a broken index is rendered is what
		// made people stop reading the report.
		return "to do"
	case health.Failed:
		return "FAILED"
	default:
		// Spelled out, because the whole point is that this is not "fine".
		return "unchecked"
	}
}
