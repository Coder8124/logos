// Package setup connects logos to the agents that will use it.
//
// The engine has never been the hard part of adopting this. The hard part is
// the nine steps between cloning the repo and an agent actually answering from
// your vault — install a runtime, pull models, choose a vault, index it, find
// the right config file for your host, and hand-write JSON full of absolute
// paths. Every one of those is a place to give up, and none of them is the part
// worth having.
//
// So this package does the wiring. It finds the MCP hosts installed on the
// machine and registers logos with each one, preferring the host's own
// registration command where there is one and merging its config file where
// there is not.
//
// # Why the host's CLI comes first
//
// Claude Code and Codex both ship a command for this. Using it means their
// config format stays their problem: when they change it, their command changes
// with it and logos keeps working. Hand-writing another application's config is
// a standing bet that its format will not move, and that bet is only worth
// taking when there is no alternative — which is the case for Claude Desktop
// and Cursor — or when the CLI would register at the wrong scope or cannot be
// exercised by a test, which is why Cline, Devin and GitHub Copilot are merged.
//
// Where a file does have to be written, it is read, merged, backed up and then
// replaced atomically. Someone's other MCP servers are not ours to lose.
package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/vault"
)

// Name is what logos calls itself in a host's server list.
const Name = "logos"

// OldName is what 0.4 setup registered the server as. Setup replaces such an
// entry through 0.4.x; this goes before 0.5.0.
const OldName = "brain"

// isLogosServer reports whether a registration's command line starts logos's
// MCP server, whatever the binary is called — the same test doctor uses for a
// duplicate, so an entry named brain that belongs to something else is kept.
func isLogosServer(command string) bool { return strings.Contains(command, "mcp serve") }

// A Server is the command a host should run to reach this logos.
type Server struct {
	// Bin is the absolute path to the logos binary. Absolute because a host
	// launches it from a working directory nobody chose.
	Bin  string
	Args []string
	// Env is what the host must set. LOGOS_VAULT belongs here and must be
	// absolute for the same reason Bin is.
	Env map[string]string
}

// Outcome says what happened to one host.
type Outcome string

const (
	// Registered means the host now points at this logos.
	Registered Outcome = "registered"
	// Updated means it already had a logos entry and it was replaced.
	Updated Outcome = "updated"
	// Unchanged means the host was already pointed at exactly this logos, so
	// the registration rewrote its config with the bytes already in it. Setup
	// is run more than once — after moving a vault, after an update, or just to
	// check — and calling that "updated" reports work that did not happen.
	Unchanged Outcome = "already connected"
	// Skipped means the host is not installed here.
	Skipped Outcome = "not installed"
	// Failed means it is installed and something went wrong.
	Failed Outcome = "failed"
	// Pending means the host is installed and would be wired. Only Plan produces
	// it — it is what the user is being asked to approve.
	Pending Outcome = "would wire"
)

// A Result is one host's line in the report.
type Result struct {
	Host    string
	Outcome Outcome
	Where   string // config path or command, for the report
	// Backup is the copy of this host's config taken before it was changed,
	// empty when there was nothing to copy. Reported, because a backup nobody
	// is told about is a backup nobody uses.
	Backup string
	// CommentsOnlyInBackup is set when the original config had comments.
	// Writing it back as JSON drops them, and the backup is the one place
	// they still exist.
	CommentsOnlyInBackup bool
	Err                  error
	// Replaced is set when the entry 0.4 setup wrote under OldName was
	// removed, and ReplaceErr when that was tried and failed. Neither fails the
	// host: logos is registered either way, and the leftover is reported.
	Replaced   bool
	ReplaceErr error
}

// A Host is one application that can talk to an MCP server.
type Host struct {
	Name string
	// Detect reports whether this host is installed. Presence-based: a command
	// on PATH, or a config directory that exists. A host nobody has installed
	// is skipped, never failed — "not installed" is not an error.
	Detect func() bool
	// Where is the config path or command shown in the report.
	Where func() string
	// Config is the file this host's registration rewrites, so it can be
	// backed up first. Empty means the file does not exist yet or logos does
	// not know where it lives — a host registered through its own CLI still
	// rewrites a file, and knowing which one is the only way back from a wrong
	// --vault.
	Config func() string
	// Register points the host at this server.
	Register func(Server) (Outcome, error)
	// List reports every MCP server this host currently has registered, read
	// back rather than assumed. Nil means this host exposes no way to read
	// that back — not an error, just a question this host cannot answer.
	List func() ([]Registration, error)
	// Remove removes the entry registered under name when it runs logos, and
	// reports whether there was one. Setup uses it for OldName, uninstall for
	// both names. Nil for a host logos cannot take an entry back out of.
	Remove func(name string) (bool, error)
}

// Registration is one server as a host currently reports it — the name it was
// given and the command line the host will actually run.
type Registration struct {
	Name    string
	Command string
	// Vault is the LOGOS_VAULT the entry sets, empty when the host's listing
	// does not show environment (`claude mcp list` does not) or none is set.
	Vault string
}

// Plan reports what Install would do, without doing any of it.
//
// Registering logos with every AI tool on someone's machine is a large action
// for someone evaluating one integration, and it used to happen with no gate at
// all — not even --yes. Showing the list first costs one function and turns an
// imposition into a choice.
//
// It does not distinguish "would register" from "would update": finding that
// out means reading a config the user has not yet agreed to have touched, and
// for the CLI-based hosts it means asking their command, which is not free.
// Pending is the honest answer to "what will happen here".
func Plan(hosts []Host) []Result {
	out := make([]Result, 0, len(hosts))
	for _, h := range hosts {
		r := Result{Host: h.Name, Where: h.Where(), Outcome: Pending}
		if !h.Detect() {
			r.Outcome = Skipped
		}
		out = append(out, r)
	}
	return out
}

// Only keeps the hosts the user named, matched case-insensitively on a prefix so
// `--host claude-code`, `--host "Claude Code"` and `--host codex` all work. An
// unmatched name is returned so the caller can say so rather than silently
// wiring nothing.
func Only(hosts []Host, names []string) (kept []Host, unmatched []string) {
	if len(names) == 0 {
		return hosts, nil
	}
	norm := func(s string) string {
		return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s))
	}
	for _, want := range names {
		found := false
		for _, h := range hosts {
			if strings.HasPrefix(norm(h.Name), norm(want)) {
				kept = append(kept, h)
				found = true
				break
			}
		}
		if !found {
			unmatched = append(unmatched, want)
		}
	}
	return kept, unmatched
}

// OnOtherVault names each detected host with a logos entry pinned to a vault
// other than dir. Setup records one vault for the whole machine but wires only
// the hosts it was asked to, so the rest can be left writing somewhere the CLI,
// the desktop app and the plugin's hooks no longer read.
func OnOtherVault(hosts []Host, dir string) (names, vaults []string) {
	for _, h := range hosts {
		if h.List == nil || h.Detect == nil || !h.Detect() {
			continue
		}
		regs, err := h.List()
		if err != nil {
			continue
		}
		for _, r := range regs {
			if isLogosServer(r.Command) && r.Vault != "" && filepath.Clean(r.Vault) != filepath.Clean(dir) {
				names, vaults = append(names, h.Name), append(vaults, r.Vault)
				break
			}
		}
	}
	return names, vaults
}

// Names lists every host logos knows how to wire, for error messages.
func Names(hosts []Host) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, h.Name)
	}
	return out
}

// Install registers the server with every host that is present.
//
// One host failing does not stop the others: a user with a broken Cursor config
// still wants Claude wired up, and the report tells them which is which.
func Install(s Server, hosts []Host) []Result {
	out := make([]Result, 0, len(hosts))
	for _, h := range hosts {
		r := Result{Host: h.Name, Where: h.Where()}
		if !h.Detect() {
			r.Outcome = Skipped
			out = append(out, r)
			continue
		}
		backup, err := backupConfig(h)
		if err != nil {
			r.Outcome, r.Err = Failed, err
			out = append(out, r)
			continue
		}
		r.Backup = backup
		outcome, err := h.Register(s)
		r.Outcome, r.Err = outcome, err
		if err != nil {
			r.Outcome = Failed
			out = append(out, r)
			continue
		}
		// After registering, so a failed registration never leaves a host with
		// neither entry; and before the comparison below, which must see it.
		if h.Remove != nil {
			r.Replaced, r.ReplaceErr = h.Remove(OldName)
		}
		// Compared afterwards rather than predicted beforehand: whether a
		// registration changes anything is only knowable once the host's own
		// command or our own merge has run. A backup of a file nobody changed
		// is litter in the user's config directory, so it goes.
		if unchanged(h, backup) {
			r.Outcome = Unchanged
			if err := os.Remove(backup); err == nil {
				r.Backup = ""
			}
		} else if raw, err := os.ReadFile(backup); err == nil && strings.HasSuffix(backup, ".json.logos-backup") {
			// JSON hosts only: a TOML or YAML config is rewritten by its own CLI, and
			// its comments are not ours to report on.
			_, r.CommentsOnlyInBackup = standardJSON(raw)
		}
		out = append(out, r)
	}
	return out
}

// backupConfig copies a host's config aside before anything rewrites it.
//
// This used to happen only inside mergeJSON, which meant it happened only for
// the two hosts with no CLI. The hosts with a CLI rewrite a config file too:
// `codex mcp add logos` replaces an existing logos entry outright — dropping
// its environment block with it — and reports "Added global MCP server". A
// user who ran setup with the wrong --vault had no way back.
//
// A config that does not exist yet needs no backup: Claude Desktop writes its
// file only once it has an MCP server, and the first one is often ours.
func backupConfig(h Host) (string, error) {
	if h.Config == nil {
		return "", nil
	}
	path := h.Config()
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read %s to back it up, so it was left alone: %w", path, err)
	}
	if err := os.WriteFile(path+".logos-backup", raw, 0o600); err != nil {
		return "", fmt.Errorf("could not back up %s, so it was left alone: %w", path, err)
	}
	return path + ".logos-backup", nil
}

// unchanged reports whether the config is byte-for-byte what the backup holds.
// A read that fails answers no: the outcome the host reported stands, because
// claiming nothing happened on the strength of a failed check is exactly the
// silent-success failure invariant 4 exists to prevent.
func unchanged(h Host, backup string) bool {
	if backup == "" || h.Config == nil {
		return false
	}
	before, err := os.ReadFile(backup)
	if err != nil {
		return false
	}
	after, err := os.ReadFile(h.Config())
	if err != nil {
		return false
	}
	return bytes.Equal(before, after)
}

// --- registering through a host's own CLI ------------------------------------

// viaCLI registers through a command the host provides. args is built by the
// caller because each CLI spells the same idea differently.
func viaCLI(bin string, args []string) (Outcome, error) {
	cmd := exec.Command(bin, args...)
	outBytes, err := cmd.CombinedOutput()
	if err == nil {
		return Registered, nil
	}
	// Older versions of both CLIs refused a name that already exists rather
	// than replacing it, and said so on stdout. Codex now replaces it silently
	// instead, which is why Install backs the config up first (backupConfig).
	// Where the refusal does still happen it is not a failure to report — it is
	// the idempotent case, and the user asked for logos to be connected, which
	// it now is.
	if s := strings.ToLower(string(outBytes)); strings.Contains(s, "already exists") ||
		strings.Contains(s, "already configured") {
		return Updated, nil
	}
	return Failed, fmt.Errorf("%s: %v: %s", bin, err, strings.TrimSpace(string(outBytes)))
}

// --- registering by merging a host's config file -----------------------------

// mcpConfig is the shape shared by Claude Desktop and Cursor: a top-level
// object with an mcpServers map. Everything else in the file is preserved
// verbatim through json.RawMessage, so a host that grows new keys does not lose
// them to us.
type mcpConfig map[string]json.RawMessage

type serverEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// MergeFile is mergeJSON, exported for `logos setup --config <path>` — a host
// whose location on disk logos has no convention for, but whose file is the
// same mcpServers-keyed JSON that Claude Desktop and Cursor already read. The
// internal hosts in Hosts() are a closed, curated list on purpose (see the
// package doc); this is the escape hatch for every MCP client that is not on
// it, so that "not one of the four we special-case" does not mean "logos
// cannot help you connect this".
func MergeFile(path string, s Server) (Outcome, error) {
	return mergeJSON(path, s)
}

// RenderConfig renders the server block a host's config needs, in the
// requested format, for printing rather than writing. It exists for the same
// reason MergeFile does: a host logos does not know how to find can still be
// wired, by hand, if the user can see what a working entry looks like. An
// empty format means json — the shape most MCP hosts actually use, and the
// one this package already writes for Claude Desktop and Cursor.
func RenderConfig(s Server, format string) (string, error) {
	switch format {
	case "", "json":
		return renderConfigJSON(s)
	case "toml":
		return renderConfigTOML(s), nil
	default:
		return "", fmt.Errorf("unknown format %q — logos knows: json, toml", format)
	}
}

func renderConfigJSON(s Server) (string, error) {
	wrapper := struct {
		Servers map[string]serverEntry `json:"mcpServers"`
	}{Servers: map[string]serverEntry{
		Name: {Command: s.Bin, Args: s.Args, Env: s.Env},
	}}
	out, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

// renderConfigTOML mirrors the shape Codex's own config.toml uses
// ([mcp_servers.<name>], with env as a nested table) — the one host in this
// package's list that speaks TOML rather than JSON, and so the format most
// likely to make a hand-edited entry actually match what its neighbors expect.
//
// Escaping only backslash and quote, not the full TOML basic-string grammar:
// what lands here is an absolute binary path and a vault directory, never
// arbitrary user text, so control characters and stray unicode are not a case
// this needs to cover.
func renderConfigTOML(s Server) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\n", Name)
	fmt.Fprintf(&b, "command = %s\n", tomlString(s.Bin))
	if len(s.Args) > 0 {
		parts := make([]string, len(s.Args))
		for i, a := range s.Args {
			parts[i] = tomlString(a)
		}
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(parts, ", "))
	}
	if len(s.Env) > 0 {
		fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", Name)
		for _, k := range sortedKeys(s.Env) {
			fmt.Fprintf(&b, "%s = %s\n", k, tomlString(s.Env[k]))
		}
	}
	return b.String()
}

func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// sortedKeys makes the env table's line order deterministic. Map iteration
// order is not, and a config a user is meant to read (or diff, or paste into a
// bug report) should not reshuffle itself between two runs of the same
// command.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mergeJSON writes the server into a host's JSON config without disturbing what
// is already there.
//
// The failure this guards against is not hypothetical: these files hold every
// other MCP server the user has connected, and clobbering one to add ours would
// be a worse bug than never registering at all. So a file that exists is parsed
// before it is touched, a malformed file is refused rather than replaced, and a
// backup is written before the new content goes down (by Install, which does
// that for every host now — see backupConfig).
func mergeJSON(path string, s Server) (Outcome, error) {
	return mergeServers(path, "mcpServers", serverEntry{Command: s.Bin, Args: s.Args, Env: s.Env})
}

// mergeServers is mergeJSON for any host: root is the key its server map sits
// under, and entry is logos's server in that host's own shape. VS Code keys its
// map "servers", and Copilot CLI ignores an entry with no type or tools, so one
// fixed shape would have written files those hosts read as having no logos.
func mergeServers(path, root string, entry any) (Outcome, error) {
	cfg, servers, err := loadServers(path, root)
	if err != nil {
		return Failed, err
	}

	_, had := servers[Name]
	encodedEntry, err := json.Marshal(entry)
	if err != nil {
		return Failed, err
	}
	servers[Name] = encodedEntry

	if err := saveServers(path, root, cfg, servers); err != nil {
		return Failed, err
	}
	if had {
		return Updated, nil
	}
	return Registered, nil
}

// loadServers reads a host config and the server map under root. A file that
// does not exist yet is an empty config: the first MCP server on this host.
func loadServers(path, root string) (mcpConfig, map[string]json.RawMessage, error) {
	cfg := mcpConfig{}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(raw))) > 0 {
			std, _ := standardJSON(raw)
			if err := json.Unmarshal(std, &cfg); err != nil {
				return nil, nil, fmt.Errorf(
					"%s is not valid JSON, so it was left alone; fix or move it and re-run: %w",
					path, err)
			}
		}
	case os.IsNotExist(err):
	default:
		return nil, nil, err
	}

	servers := map[string]json.RawMessage{}
	if rawServers, ok := cfg[root]; ok && len(rawServers) > 0 {
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			return nil, nil, fmt.Errorf("%s has a %s block that is not an object: %w", path, root, err)
		}
	}
	return cfg, servers, nil
}

func saveServers(path, root string, cfg mcpConfig, servers map[string]json.RawMessage) error {
	encoded, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	cfg[root] = encoded

	// Indented, because a person opens these files.
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return vault.WriteAtomic(path, append(out, '\n'))
}

// readMCPServers reads back what a JSON-config host (Claude Desktop, Cursor)
// currently has registered, in the same mcpServers shape mergeJSON writes. A
// missing or empty file is not an error — nothing registered is a valid
// answer — but malformed JSON is, since a caller asking "what's here" should
// not be told "nothing" about a file that actually holds something unreadable.
func readMCPServers(path string) ([]Registration, error) {
	return readServerBlock(path, "mcpServers")
}

// readServerBlock is readMCPServers for a host whose server map sits under root.
func readServerBlock(path, root string) ([]Registration, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	cfg := mcpConfig{}
	std, _ := standardJSON(raw)
	if err := json.Unmarshal(std, &cfg); err != nil {
		return nil, err
	}
	rawServers, ok := cfg[root]
	if !ok || len(rawServers) == 0 {
		return nil, nil
	}
	servers := map[string]serverEntry{}
	if err := json.Unmarshal(rawServers, &servers); err != nil {
		return nil, err
	}
	out := make([]Registration, 0, len(servers))
	for name, s := range servers {
		out = append(out, Registration{Name: name, Command: strings.Join(append([]string{s.Command}, s.Args...), " "), Vault: s.Env["LOGOS_VAULT"]})
	}
	return out, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func inHome(parts ...string) string {
	h := home()
	if h == "" {
		return ""
	}
	return filepath.Join(append([]string{h}, parts...)...)
}

func parent(path string) string { return filepath.Dir(path) }

func joinPath(parts ...string) string { return filepath.Join(parts...) }

// appData is Windows' per-user config root.
func appData() string { return os.Getenv("APPDATA") }

// Removal says what uninstall took out of one host.
type Removal struct {
	Host  string
	Where string
	// Removed names the entries taken out: logos, and brain if 0.4 left one.
	Removed []string
	// Backup is the config as it was before, kept only when it changed.
	Backup string
	Err    error
}

// Uninstall takes every logos entry back out of each host that is present.
// Only entries that run `mcp serve` go, under either name, so someone else's
// server called brain survives, and a host failing does not stop the others.
func Uninstall(hosts []Host) []Removal {
	var out []Removal
	for _, h := range hosts {
		if !h.Detect() || h.Remove == nil {
			continue
		}
		r := Removal{Host: h.Name, Where: h.Where()}
		// Backed up to a side file first, so a config is never changed without
		// one, and moved over the real backup only if something was removed:
		// writing straight over it on a run that found nothing destroyed the
		// earlier run's copy, the one that still had logos in it.
		backup, pending := "", ""
		if h.Config != nil && h.Config() != "" {
			path := h.Config()
			raw, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				r.Err = fmt.Errorf("could not read %s to back it up, so it was left alone: %w", path, err)
				out = append(out, r)
				continue
			}
			if err == nil {
				backup, pending = path+".logos-backup", path+".logos-backup.pending"
				if err := os.WriteFile(pending, raw, 0o600); err != nil {
					r.Err = fmt.Errorf("could not back up %s, so it was left alone: %w", path, err)
					out = append(out, r)
					continue
				}
			}
		}
		for _, name := range []string{Name, OldName} {
			removed, err := h.Remove(name)
			if err != nil {
				r.Err = err
				break
			}
			if removed {
				r.Removed = append(r.Removed, name)
			}
		}
		if pending != "" {
			if unchanged(h, pending) {
				os.Remove(pending)
				backup = ""
			} else if err := os.Rename(pending, backup); err != nil && r.Err == nil {
				r.Err = fmt.Errorf("removed from %s but could not keep its backup, which is at %s: %w", h.Name, pending, err)
				backup = pending
			}
		}
		r.Backup = backup
		out = append(out, r)
	}
	return out
}
