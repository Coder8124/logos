package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/vault"
)

// Keeping the plugin level with the binary, without anyone having to think
// about it.
//
// `logos update` replaces the binary and nothing else. Claude Code does not
// refresh a third-party marketplace on its own, and has no auto-update switch
// for plugins, so an install that started on 0.1.2 kept running 0.1.2's hooks
// against a 0.4 server indefinitely. doctor learned to warn about it, but a
// warning is only read by someone who runs doctor, and the people most likely
// to be stale are exactly the people who never do.
//
// So the plugin updates itself: the session-start hook asks for a check, and
// the check runs `claude plugin marketplace update` + `claude plugin update`
// when the installed plugin is behind this binary. Three things keep that from
// being the kind of silent background work this codebase treats as a bug:
//
//   - it is bounded to once a day, so a session start is not a network call.
//   - the hook backgrounds it, so nothing can stall a session on it; the result
//     is read at the *next* session start, off a stamp file.
//   - it announces itself. The next session opens saying the plugin updated
//     itself and to what, and a failed update says that instead of nothing.

// AutoUpdateEvery is how long a check is good for. A day: the plugin is not
// released more often than that, and a check that reached the network every
// session would be a network call in the one place that has to stay instant.
const AutoUpdateEvery = 24 * time.Hour

// updateStamp is where the last check is recorded, beside the vault pointer in
// the user's config directory. Deliberately not in the vault: which plugin this
// machine's Claude Code has is a fact about the machine, and a vault carried to
// a second machine would otherwise arrive claiming its plugin was up to date.
const updateStamp = "plugin-update.json"

// An UpdateRecord is the outcome of the last check. Announced is what keeps the
// announcement to one session: the notice is read once and then marked.
type UpdateRecord struct {
	Checked   time.Time `json:"checked"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Failed    string    `json:"failed,omitempty"`
	Announced bool      `json:"announced,omitempty"`
}

// updatePlugin is the side effect, in one variable, so the decision above it
// can be tested on a machine with no Claude Code on it.
var updatePlugin = func() error {
	if !SupportsPluginCommands() {
		return fmt.Errorf("this Claude Code cannot update plugins from the command line")
	}
	return RunPluginSteps(UpdatePluginSteps())
}

// AutoUpdatePlugin updates the Logos plugin if it is behind version and has not
// been checked since now-AutoUpdateEvery. It reports whether it ran an update.
//
// Everything that is not "the plugin is installed, connected, and older than
// this binary" is a reason to do nothing rather than to fail: a machine with no
// plugin, a disabled one, or a dev build with no release number to rank is not
// a machine with a problem to fix. The error is returned only when an update
// was attempted and the update itself failed — and that is recorded too, so the
// next session says so out loud rather than retrying in silence forever.
func AutoUpdatePlugin(version string, now time.Time) (updated bool, err error) {
	rec, _ := readUpdateRecord()
	if now.Sub(rec.Checked) < AutoUpdateEvery {
		return false, nil
	}
	r := LogosPluginRecord()
	if !r.Installed || !r.Connects {
		return false, nil
	}
	stale, ranked := buildinfo.Older(r.Version, version)
	if !ranked || !stale {
		// Still a check: not writing the stamp here would reach for the plugin
		// record on every session start of an install that is already current.
		return false, writeUpdateRecord(UpdateRecord{Checked: now, Announced: true})
	}
	// Both versions are recorded without their "v", since the notice puts them
	// side by side and "0.4.1 to v0.4.4" reads like two different schemes.
	next := UpdateRecord{Checked: now, From: strings.TrimPrefix(r.Version, "v"), To: strings.TrimPrefix(version, "v")}
	if err := updatePlugin(); err != nil {
		next.Failed = err.Error()
		_ = writeUpdateRecord(next)
		return false, err
	}
	return true, writeUpdateRecord(next)
}

// UpdateNotice is the line the next session says, and says once — an empty
// string when there is nothing to announce. Reading it marks it announced, so
// a second session does not repeat a week-old update as though it just
// happened.
func UpdateNotice() string {
	rec, err := readUpdateRecord()
	if err != nil || rec.Announced {
		return ""
	}
	rec.Announced = true
	_ = writeUpdateRecord(rec)
	switch {
	case rec.Failed != "":
		return fmt.Sprintf("The Logos plugin is %s and this logos is %s, and updating it failed: %s. Run `claude plugin marketplace update logos && claude plugin update logos@logos` by hand.", rec.From, rec.To, rec.Failed)
	case rec.To != "":
		return fmt.Sprintf("Logos updated its own Claude Code plugin from %s to %s; restart Claude Code if its hooks look stale.", rec.From, rec.To)
	}
	return ""
}

func readUpdateRecord() (UpdateRecord, error) {
	var rec UpdateRecord
	p, err := updateStampPath()
	if err != nil {
		return rec, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return UpdateRecord{}, err
	}
	return rec, nil
}

func writeUpdateRecord(rec UpdateRecord) error {
	p, err := updateStampPath()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := vault.MkdirPrivate(filepath.Dir(p)); err != nil {
		return err
	}
	return vault.WriteAtomic(p, append(raw, '\n'))
}

func updateStampPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "logos", updateStamp), nil
}
