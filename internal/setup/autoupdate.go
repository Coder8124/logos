package setup

import (
	"encoding/json"
	"errors"
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

// AutoUpdateBackoff is how long an update that changed nothing is believed for.
// `claude plugin update` exits 0 with nothing to install, so a plugin the
// marketplace cannot move would otherwise reach the network every day for the
// rest of the install's life. A week still picks up a late release without
// making the failure a daily habit.
const AutoUpdateBackoff = 7 * 24 * time.Hour

// An UpdateRecord is the outcome of the last check. Announced is what keeps the
// announcement to one session: the notice is read once and then marked.
// Stayed is set when an update ran and moved nothing: it holds the version this
// binary wanted, with From still the version the plugin is on. That pair used to
// be written as a completed update.
type UpdateRecord struct {
	Checked   time.Time `json:"checked"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Stayed    string    `json:"stayed,omitempty"`
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
	next, mine, err := claimUpdateCheck(version, now)
	if err != nil || !mine {
		return false, err
	}
	if err := updatePlugin(); err != nil {
		next.Failed = err.Error()
		_ = saveUpdateRecord(next)
		return false, err
	}
	// What the plugin is on now, not what this binary is on. The two commands
	// exit 0 with nothing to install, so the version afterwards is the only
	// evidence that anything happened.
	after := strings.TrimPrefix(LogosPluginRecord().Version, "v")
	if after == next.From {
		next.To, next.Stayed = "", next.To
		return false, saveUpdateRecord(next)
	}
	next.To = after
	return true, saveUpdateRecord(next)
}

// claimUpdateCheck decides whether this process is the one that runs the update,
// and says so in the stamp before it lets go of the lock.
//
// The stamp is the only thing bounding the update to once a day, so deciding and
// recording had to stop being two steps with a network call between them: the
// session-start hook starts one logos in the background and another in the
// foreground milliseconds later, and someone opening four Claude Code windows at
// once started four. All of them read the same stale stamp, all of them ran
// `claude plugin update`, and Claude Code's own installed_plugins.json — not our
// file — had four concurrent writers.
func claimUpdateCheck(version string, now time.Time) (UpdateRecord, bool, error) {
	unlock, err := lockUpdateStamp()
	if err != nil {
		// Another logos holds the stamp, which means another logos is already
		// doing this. There is nothing here worth queueing for.
		if errors.Is(err, errStampBusy) {
			return UpdateRecord{}, false, nil
		}
		return UpdateRecord{}, false, err
	}
	defer unlock()

	rec, _ := readUpdateRecord()
	if now.Sub(rec.Checked) < AutoUpdateEvery {
		return UpdateRecord{}, false, nil
	}
	r := LogosPluginRecord()
	if !r.Installed || !r.Connects {
		return UpdateRecord{}, false, nil
	}
	// An update that ran and moved nothing is worth believing for a while: the
	// plugin is still on the version it stayed at, so the same two commands
	// would reach the network for the same nothing.
	if rec.Stayed != "" && rec.From == strings.TrimPrefix(r.Version, "v") && now.Sub(rec.Checked) < AutoUpdateBackoff {
		return UpdateRecord{}, false, nil
	}
	stale, ranked := buildinfo.Older(r.Version, version)
	if !ranked || !stale {
		// Still a check: not writing the stamp here would reach for the plugin
		// record on every session start of an install that is already current.
		return UpdateRecord{}, false, writeUpdateRecord(UpdateRecord{Checked: now, Announced: true})
	}
	// Both versions are recorded without their "v", since the notice puts them
	// side by side and "0.4.1 to v0.4.4" reads like two different schemes.
	next := UpdateRecord{Checked: now, From: strings.TrimPrefix(r.Version, "v"), To: strings.TrimPrefix(version, "v")}
	return next, true, writeUpdateRecord(next)
}

// UpdateNotice is the line the next session says, and says once — an empty
// string when there is nothing to announce. Reading it marks it announced, so
// a second session does not repeat a week-old update as though it just
// happened.
func UpdateNotice() string {
	unlock, err := lockUpdateStamp()
	if err != nil {
		// Said next session rather than raced for: reading the record and
		// marking it announced is a read-modify-write like any other, and the
		// one it would overwrite is the outcome of the check running right now.
		return ""
	}
	defer unlock()
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
	case rec.Stayed != "":
		return fmt.Sprintf("The Logos plugin is %s and this logos is %s: the update ran and the marketplace had nothing newer, so the plugin stayed where it is. Logos looks again in a week.", rec.From, rec.Stayed)
	}
	return ""
}

// errStampBusy is another logos holding the stamp for longer than it is worth
// waiting for.
var errStampBusy = errors.New("another logos is checking the plugin")

// stampLockStale is when a lock file stops being believed. A logos killed
// between taking the lock and releasing it — ^C at a session start, a laptop
// closed — would otherwise stop this machine ever checking again.
const stampLockStale = 5 * time.Minute

// lockUpdateStamp takes the lock that makes read-modify-writes of the stamp one
// at a time across processes, and returns the way to give it back. O_EXCL
// because this has to hold between separate logos processes, which is exactly
// what a mutex cannot do.
//
// The wait is short: everything done under this lock is a read and a write of
// one small file, so a lock held longer than that is a dead one, not a busy one.
func lockUpdateStamp() (func(), error) {
	p, err := updateStampPath()
	if err != nil {
		return nil, err
	}
	if err := vault.MkdirPrivate(filepath.Dir(p)); err != nil {
		return nil, err
	}
	lock := p + ".lock"
	for deadline := time.Now().Add(2 * time.Second); ; {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lock) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > stampLockStale {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errStampBusy
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// saveUpdateRecord writes the outcome of a check under the lock, so it does not
// land on top of a notice being marked announced at the same moment.
func saveUpdateRecord(rec UpdateRecord) error {
	unlock, err := lockUpdateStamp()
	if err != nil {
		// The outcome of an update is worth more than the lock: writing it
		// anyway is one atomic replace, which is where this started.
		return writeUpdateRecord(rec)
	}
	defer unlock()
	return writeUpdateRecord(rec)
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
