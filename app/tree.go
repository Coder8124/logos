package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/contextpack"
	"github.com/Coder8124/brain/internal/vault"
)

// The folder tree is the same vault a hand-edit already trusts — memories/,
// sessions/<project>/, whatever notes live at the root, ingest/ candidates —
// so this binds no new storage, only a walk of it plus the pin/exclude rules
// contextpack.Build already enforces (see internal/contextpack/pathrules.go).
// A node's pin state is read from that one file, and setting it writes that
// same file, so the tree view and `brain context --pin/--exclude` are two
// faces of one durable record, not two.

// TreeNode is one file or directory in the vault, relative to its root.
type TreeNode struct {
	Path     string     `json:"path"` // slash-separated, relative to the vault root
	Name     string     `json:"name"`
	IsDir    bool       `json:"isDir"`
	Pin      string     `json:"pin"` // "pin", "exclude", or "" — see contextpack.PathPin.String
	Children []TreeNode `json:"children,omitempty"`
}

// VaultTree lists the vault as a tree, each node annotated with the pin/
// exclude rule that currently covers it (inherited from the nearest ancestor
// rule, the same precedence contextpack.Build applies when it assembles a
// pack). Dot-directories are skipped — the same rule index.Sync uses to keep
// .brain and .context out of search — so the rules file that drives this view
// never appears as a node inside it.
func (a *App) VaultTree() ([]TreeNode, error) {
	if _, err := os.Stat(a.vault); err != nil {
		return nil, fmt.Errorf("no vault at %s", a.vault)
	}
	rules, err := contextpack.LoadPathRules(a.vault)
	if err != nil {
		return nil, err
	}
	return buildTree(a.vault, "", rules)
}

func buildTree(root, rel string, rules []contextpack.PathRule) ([]TreeNode, error) {
	dir := filepath.Join(root, rel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di // directories before files, alphabetical within each
		}
		return entries[i].Name() < entries[j].Name()
	})

	var out []TreeNode
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // .brain (cache), .context (this feature's own rules file), .git
		}
		childRel := e.Name()
		if rel != "" {
			childRel = rel + "/" + e.Name()
		}
		node := TreeNode{
			Path:  childRel,
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Pin:   contextpack.MatchPathRule(childRel, rules).String(),
		}
		if e.IsDir() {
			children, err := buildTree(root, childRel, rules)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		out = append(out, node)
	}
	return out, nil
}

// SetTreePin sets or clears the pin/exclude rule for one node, and writes it
// to .context/rules.md — the vault, not the index, per invariant 1: a rule
// nobody rebuilds from anywhere else has to be the thing that is actually
// durable. mode is "pin", "exclude", or "" to clear.
func (a *App) SetTreePin(path, mode string) error {
	if _, err := os.Stat(a.vault); err != nil {
		return fmt.Errorf("no vault at %s", a.vault)
	}
	var pin contextpack.PathPin
	switch mode {
	case "pin":
		pin = contextpack.PathPinAlways
	case "exclude":
		pin = contextpack.PathPinNever
	case "":
		pin = contextpack.PathPinNone
	default:
		return fmt.Errorf("unknown pin mode %q — want \"pin\", \"exclude\", or \"\"", mode)
	}
	return contextpack.SetPathRule(a.vault, path, pin)
}

// resolveVaultPath turns a tree path into an absolute one, refusing anything
// that would land outside the vault. The tree only ever hands this function
// paths it produced itself, but the edit pane's save is one text field away
// from a typed path, and a vault-write function that trusts its caller's
// arithmetic is the traversal bug waiting to happen.
func resolveVaultPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q is not a vault-relative path", rel)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return "", fmt.Errorf("path %q escapes the vault", rel)
		}
	}
	abs := filepath.Join(root, filepath.Clean(rel))
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the vault", rel)
	}
	return abs, nil
}

// ReadVaultFile returns one file's raw content for the edit pane. Markdown
// only — the tree can list anything, but the promise this pane makes
// ("edit any line to correct it") is a promise about the hand-editable vault
// files brain itself writes, not an invitation to open arbitrary binaries.
func (a *App) ReadVaultFile(path string) (string, error) {
	abs, err := resolveVaultPath(a.vault, path)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(abs, ".md") {
		return "", fmt.Errorf("only markdown files can be opened in the edit pane")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteVaultFile saves an edit back through vault.WriteAtomic and reindexes
// just that file's content — the same order every durable write in this
// codebase follows: the vault first, so a crash mid-write never leaves an
// index pointing at something the vault never got.
func (a *App) WriteVaultFile(path, content string) error {
	abs, err := resolveVaultPath(a.vault, path)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(abs, ".md") {
		return fmt.Errorf("only markdown files can be saved from the edit pane")
	}
	if err := vault.WriteAtomic(abs, []byte(content)); err != nil {
		return err
	}
	ix, err := a.open()
	if err != nil {
		// The vault write already succeeded — invariant 2. Say so rather than
		// reporting a save that silently left the cache stale.
		return fmt.Errorf("saved, but could not reindex: %w", err)
	}
	defer ix.Close()
	if _, err := ix.Sync(); err != nil {
		return fmt.Errorf("saved, but reindexing failed: %w", err)
	}
	return nil
}
