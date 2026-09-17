package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// `logos doctor --report` — the bundle a maintainer would otherwise have to ask
// for, one question at a time, across three days of a GitHub thread.
//
// Invariant 5 says nothing leaves the machine, which rules out the usual answer
// to this problem: no crash reporter, no opt-in telemetry, not even a "send
// diagnostics?" prompt. What is left is to print it, let the user read every
// line before deciding to share it, and make sure there is nothing in it they
// would regret sharing — which is why the home directory is folded to ~ and no
// vault content, note, prompt or command line appears here at all.

// runtimeLabel is the platform, which is the first thing to ask: most of what
// is currently broken is broken on exactly one of them.
var runtimeLabel = runtime.GOOS + "/" + runtime.GOARCH

func doctorReport() error {
	v := vaultPath()
	fmt.Println("### logos support report")
	fmt.Println()
	fmt.Println("```")
	fmt.Printf("logos      %s  (%s, go %s)\n", buildinfo.Version, runtimeLabel, strings.TrimPrefix(runtime.Version(), "go"))

	rec := setup.LogosPluginRecord()
	switch {
	case !rec.Installed:
		fmt.Println("plugin     not installed")
	case rec.Version == "":
		fmt.Println("plugin     installed, version not recorded")
	default:
		fmt.Printf("plugin     %s%s\n", rec.Version, map[bool]string{false: " (installed but not connecting)", true: ""}[rec.Connects])
	}

	// Where the vault is, and whether it is the recorded one — a vault set by
	// an environment variable in one shell and not another is the shape of
	// half the "my memory is empty" reports.
	fmt.Printf("vault      %s", redactHome(v))
	if _, err := os.Stat(v); err != nil {
		fmt.Print("  MISSING")
	}
	if rc := vault.Recorded(); rc != "" && rc != v {
		fmt.Printf("  (this machine recorded %s)", redactHome(rc))
	}
	if os.Getenv("LOGOS_VAULT") != "" {
		fmt.Print("  (from LOGOS_VAULT)")
	}
	fmt.Println()
	fmt.Printf("activity   %s\n", map[bool]string{true: "recording", false: "off"}[activity.Recording(v)])

	// Two copies of logos on PATH, one of them stale, is the single most common
	// cause of "I updated and nothing changed" — and it is invisible to the
	// user, because both answer to the same name.
	fmt.Println("binaries")
	for _, line := range logosCopies() {
		fmt.Printf("    %s\n", line)
	}

	fmt.Println("hosts")
	var detected []setup.Host
	for _, h := range setup.Hosts() {
		if h.Detect != nil && h.Detect() {
			detected = append(detected, h)
		}
	}
	// A bare heading reads as a rendering fault in a bundle whose whole job is
	// to be believed, and "no host detected" is itself the answer to a good
	// share of the reports this command exists to shorten.
	if len(detected) == 0 {
		fmt.Println("    none detected")
	}
	w := 0
	for _, h := range detected {
		if len(h.Name) > w {
			w = len(h.Name)
		}
	}
	for _, h := range detected {
		where := ""
		if h.Config != nil {
			where = redactHome(h.Config())
		}
		fmt.Printf("    %-*s %s\n", w, h.Name, where)
	}

	fmt.Println("checks")
	checks := gatherHealth().Checks
	// Width from the longest name, as the report proper does: a row whose
	// verdict falls outside the column everything else lines up in reads as a
	// rendering bug.
	w = 0
	for _, c := range checks {
		if len(c.Name) > w {
			w = len(c.Name)
		}
	}
	for _, c := range checks {
		// The name and the verdict, not the detail: details carry counts,
		// paths and the odd project name, and this is the part of the report
		// most likely to be pasted somewhere public.
		fmt.Printf("    %-*s %s\n", w, c.Name, renderState(c.State))
	}
	fmt.Println("```")
	fmt.Println()
	fmt.Println("Nothing above was sent anywhere: logos has no telemetry, and this command")
	fmt.Println("made no network call. Read it, then paste it into your issue.")
	return nil
}

// logosCopies lists every logos on PATH, in the order the shell would find
// them, marking the one running now.
func logosCopies() []string {
	self, _ := selfPath()
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "logos")
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		resolved := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			resolved = r
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		line := redactHome(p)
		if resolved != p {
			line += " → " + redactHome(resolved)
		}
		if resolved == self {
			line += "  (running)"
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		// Reached through an absolute path, or through npx: still worth saying
		// which binary is talking, since it is the one nothing else can see.
		if self != "" {
			return []string{redactHome(self) + "  (running; not on PATH)"}
		}
		return []string{"none on PATH"}
	}
	return out
}

// redactHome folds the home directory to ~. The user's name is in it, and on a
// work machine so is their employer's; neither helps anybody answer a bug, and
// this text exists to be pasted in public.
func redactHome(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}
