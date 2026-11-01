package mcpserver

import (
	"fmt"
	"os"

	"github.com/Coder8124/logos/internal/buildinfo"
)

// exeStamp is the file a server was started from, as it was then.
type exeStamp struct {
	path string
	info os.FileInfo
}

// started is this process's own executable at start. A host keeps the server
// it launched until the host restarts, so after `brew upgrade` or an npm update
// it went on serving the old version for weeks, and nothing said so: the user
// asked for per-conversation folders that had shipped two releases earlier.
var started = stampOf(executable())

// executable is the path the server was run by, not resolved: Homebrew's
// bin/logos is a link that an upgrade moves to the new version while the old
// file stays in the Cellar, so only the link shows the change.
func executable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

func stampOf(path string) exeStamp {
	if path == "" {
		return exeStamp{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return exeStamp{}
	}
	return exeStamp{path: path, info: info}
}

// staleNotice is a line for resume and checkpoint when the file this server
// was started from has since been replaced or removed, and "" otherwise —
// including when the server could not stat itself at start.
func staleNotice() string {
	if started.info == nil {
		return ""
	}
	now, err := os.Stat(started.path)
	if err == nil && os.SameFile(now, started.info) && now.Size() == started.info.Size() && now.ModTime().Equal(started.info.ModTime()) {
		return ""
	}
	return fmt.Sprintf("\n_This server is logos %s, and %s has been replaced or removed since it started, so an update is installed but not running. Restart this host to load it._\n",
		buildinfo.Version, started.path)
}
