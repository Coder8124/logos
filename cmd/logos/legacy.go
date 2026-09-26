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
	// its vault recorded under the new name, and would otherwise open an empty
	// ~/logos as though the old one had never existed.
	if os.Getenv("LOGOS_VAULT") == "" && vault.Pointer() == "" {
		if dir := oldVault(); dir != "" {
			fmt.Fprintf(stderr, "logos: the 0.4 vault at %s is not read since 0.5.0 — `logos setup --vault %s` keeps using it, `logos migrate` moves ~/brain to ~/logos\n", dir, dir)
		}
	}

	// .brain holds model config and ingest consent as well as the index; the
	// index rebuilds, the other two would be lost without a word. Said for as
	// long as .brain is there, not only until .logos appears: the first command
	// after the warning creates .logos, and one mention is easy to miss.
	v := vault.Path()
	was, next := filepath.Join(v, ".brain"), filepath.Join(v, ".logos")
	switch {
	case isDir(was) && !isDir(next):
		fmt.Fprintf(stderr, "logos: %s is from 0.4 and not read since 0.5.0 — `mv %s %s` keeps its model config and ingest consent\n", was, was, next)
	case isDir(was):
		fmt.Fprintf(stderr, "logos: %s is from 0.4 and not read since 0.5.0 — logos uses %s; copy any model config or ingest consent you need from it, then delete it\n", was, next)
	}
}

// oldVault is the vault a 0.4 install used: the location it recorded, or
// ~/brain when nothing was recorded and ~/logos does not exist yet.
func oldVault() string {
	if cfg, err := os.UserConfigDir(); err == nil {
		if raw, err := os.ReadFile(filepath.Join(cfg, "brain", "vault-path")); err == nil {
			if dir := strings.TrimSpace(string(raw)); dir != "" {
				return dir
			}
		}
	}
	h, err := os.UserHomeDir()
	if err != nil || isDir(filepath.Join(h, "logos")) || !isDir(filepath.Join(h, "brain")) {
		return ""
	}
	return filepath.Join(h, "brain")
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
