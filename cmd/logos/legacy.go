package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/vault"
)

// warnOldNames says, on stderr, which parts of a 0.4 install this logos no
// longer reads. Through 0.4.x they were read under the old name; from 0.5.0
// nothing is, and a vault that silently stopped being found would read as
// memory that vanished on upgrade. So the old names are reported, never
// adopted: stderr, which the MCP stream never reads, and each line names the
// command that ends it.
func warnOldNames(stderr io.Writer) {
	var old []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "BRAIN_") {
			continue
		}
		if _, set := os.LookupEnv("LOGOS_" + strings.TrimPrefix(key, "BRAIN_")); !set {
			old = append(old, key)
		}
	}
	sort.Strings(old)
	for _, key := range old {
		fmt.Fprintf(stderr, "logos: %s is not read since 0.5.0 — rename it LOGOS_%s\n", key, strings.TrimPrefix(key, "BRAIN_"))
	}

	// A machine that went from 0.4.2 or earlier straight to 0.5.0 never had
	// its vault recorded under the new name. Said until a vault is recorded,
	// not only until ~/logos exists: the first run — often a host's MCP server,
	// whose stderr nobody reads — creates an empty ~/logos, and a warning that
	// stopped there left the user looking at a vault with nothing in it.
	if os.Getenv("LOGOS_VAULT") == "" && vault.Pointer() == "" {
		if dir := oldVault(); dir != "" {
			fmt.Fprintf(stderr, "logos: the 0.4 vault at %s is not read since 0.5.0 — %s\n", dir, oldVaultFix(dir))
		}
	}

	// .brain holds model config and ingest consent as well as the index; the
	// index rebuilds, the other two would be lost without a word. Said for as
	// long as .brain is there, not only until .logos appears: the first command
	// after the warning creates .logos, and one mention is easy to miss. For
	// the same reason the fix is a copy, not a move: `mv .brain .logos` once
	// .logos exists nests it at .logos/.brain, where nothing reads it.
	v := vault.Path()
	if was := filepath.Join(v, ".brain"); isDir(was) {
		next := filepath.Join(v, ".logos")
		fmt.Fprintf(stderr, "logos: %s is from 0.4 and not read since 0.5.0 — `mkdir -p %s && cp -n %s/*.json %s/` keeps its model config and ingest consent, then delete it\n", was, next, was, next)
	}
}

// oldVault is the vault a 0.4 install used: the location it recorded, or
// ~/brain when nothing was recorded and it holds a 0.4 index. The index is the
// test because "brain" is a common name for a folder of someone's own notes,
// and this warning runs on every command until a vault is recorded.
func oldVault() string {
	if cfg, err := os.UserConfigDir(); err == nil {
		if raw, err := os.ReadFile(filepath.Join(cfg, "brain", "vault-path")); err == nil {
			if dir := strings.TrimSpace(string(raw)); dir != "" && isDir(dir) {
				return dir
			}
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(h, "brain")
	// A link is what migrate leaves behind, and names a vault already moved.
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || !isDir(filepath.Join(dir, ".brain")) {
		return ""
	}
	return dir
}

// oldVaultFix is the command that stops the warning, which is whichever one
// records a vault. migrate only moves ~/brain, and refuses once ~/logos exists,
// so it is offered only when it would work.
func oldVaultFix(dir string) string {
	h, _ := os.UserHomeDir()
	keep := fmt.Sprintf("`logos setup --vault %s` keeps using it", dir)
	if filepath.Clean(dir) != filepath.Join(h, "brain") {
		return keep
	}
	if logos := filepath.Join(h, "logos"); isDir(logos) {
		return fmt.Sprintf("this run used %s instead; %s, `logos setup --vault %s` keeps the new one", logos, keep, logos)
	}
	return keep + ", `logos migrate` moves it to ~/logos"
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
