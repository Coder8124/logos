package rollup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/vault"
)

// Writing to the vault.
//
// Obsidian may have the same file open, so writes are atomic temp-then-rename
// and no handle is ever held. Prose the user wrote is immutable: appends land
// in a dedicated Observations section and never touch anything above it.

// ObservationsHeading is the only part of a note this system may append to.
const ObservationsHeading = "## Observations"

// Apply commits an accepted proposal to the vault.
func Apply(db *sql.DB, vaultDir string, p Proposal) error {
	if err := p.Validate(); err != nil {
		return err
	}

	switch p.Kind {
	case NewNote:
		return applyNewNote(vaultDir, p)
	case Append:
		return applyAppend(vaultDir, p)
	case NewEdge:
		return applyEdge(vaultDir, p)
	case Merge:
		// Merges rewrite two notes and every inbound link. Deliberately left
		// to the user in the editor for now: getting it wrong loses
		// information, and a half-correct automatic merge is worse than none.
		return fmt.Errorf("merge must be applied by hand — open %s and %s", p.Target, p.Payload.Into)
	}
	return fmt.Errorf("unknown proposal kind %q", p.Kind)
}

// notePath is where a proposal's target lands, and it is the last place that
// can stop a slug from leaving the vault. Validate already refuses these, but
// this path is what actually opens a file, so it checks the result rather than
// trusting that every caller validated first.
func notePath(vaultDir, slug string) (string, error) {
	if err := checkTarget(slug); err != nil {
		return "", err
	}
	path := filepath.Join(vaultDir, filepath.FromSlash(slug)+".md")
	root, err := filepath.Abs(vaultDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("proposal target %q resolves to %s, outside the vault", slug, abs)
	}
	return path, nil
}

// yamlValue renders a frontmatter scalar so a name cannot end the frontmatter
// block and continue as keys of its own. Validate rejects the line breaks that
// make that possible; this is what keeps the quotes, colons and leading markers
// in an ordinary name from breaking the file anyway.
//
// Plain values are left plain. These notes are read and edited by hand in
// Obsidian, and quoting every title to defend against the rare one makes every
// file worse to read.
func yamlValue(s string) string {
	if s == "" || s != strings.TrimSpace(s) || strings.ContainsAny(s, ":#\"'{}[]&*!|>%@`,\n\r\t") {
		return strconv.Quote(s)
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return strconv.Quote(s) // a name that would otherwise parse as a number
	}
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		return strconv.Quote(s)
	}
	return s
}

// writeAtomic delegates to the vault's single writer, so proposals and agent
// checkpoints land with identical crash guarantees.
func writeAtomic(path string, data []byte) error { return vault.WriteAtomic(path, data) }

func applyNewNote(vaultDir string, p Proposal) error {
	path, err := notePath(vaultDir, p.Target)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		// Resolution should have caught this upstream; refuse rather than
		// clobber a note that already exists.
		return fmt.Errorf("%s already exists — this should have been an append", p.Target)
	}

	kind := p.Payload.Type
	if kind == "" {
		kind = "note"
	}
	today := time.Unix(p.Created, 0).Format("2006-01-02")

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", yamlValue(kind))
	if p.Payload.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", yamlValue(p.Payload.Title))
	}
	fmt.Fprintf(&b, "first_seen: %s\n", today)
	fmt.Fprintf(&b, "observations: %d\n", len(p.Evidence))
	b.WriteString("---\n\n")
	b.WriteString(ObservationsHeading + "\n\n")
	fmt.Fprintf(&b, "- %s %s\n", today, strings.TrimSpace(p.Payload.Body))

	return writeAtomic(path, []byte(b.String()))
}

func applyAppend(vaultDir string, p Proposal) error {
	path, err := notePath(vaultDir, p.Target)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot append to %s: %w", p.Target, err)
	}

	today := time.Unix(p.Created, 0).Format("2006-01-02")
	line := fmt.Sprintf("- %s %s\n", today, strings.TrimSpace(p.Payload.Body))
	content := string(raw)

	if idx := strings.Index(content, ObservationsHeading); idx >= 0 {
		// Append at the end of the file; the Observations section is by
		// convention last, and anything after it the user added deliberately.
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += line
	} else {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + ObservationsHeading + "\n\n" + line
	}

	return writeAtomic(path, []byte(content))
}

// applyEdge adds a typed relation to frontmatter, carrying the confidence and
// provenance that keep the graph honest.
func applyEdge(vaultDir string, p Proposal) error {
	path, err := notePath(vaultDir, p.Target)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot add edge to %s: %w", p.Target, err)
	}
	content := string(raw)

	// The object is wrapped in a wiki link inside a quoted scalar, so it is
	// quoted as a whole rather than interpolated: an obj containing a quote or a
	// closing bracket would otherwise end the entry and start writing keys.
	rel := fmt.Sprintf("  - { pred: %s, obj: %s, conf: %.2f, src: inferred }\n",
		yamlValue(p.Payload.Pred), strconv.Quote("[["+p.Payload.Obj+"]]"), p.Conf)

	if !strings.HasPrefix(content, "---\n") {
		// No frontmatter at all — give the note one rather than silently
		// dropping the edge.
		return writeAtomic(path, []byte("---\nrelations:\n"+rel+"---\n\n"+content))
	}

	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return fmt.Errorf("%s has an unterminated frontmatter block", p.Target)
	}
	end += 4

	fm, body := content[4:end], content[end:]
	if strings.Contains(fm, "relations:") {
		fm = strings.Replace(fm, "relations:\n", "relations:\n"+rel, 1)
	} else {
		if !strings.HasSuffix(fm, "\n") {
			fm += "\n"
		}
		fm += "relations:\n" + rel
	}

	return writeAtomic(path, []byte("---\n"+fm+body))
}
