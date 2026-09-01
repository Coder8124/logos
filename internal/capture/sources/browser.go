package sources

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Coder8124/brain/internal/event"
	_ "modernc.org/sqlite"
)

// Browser history is read from the browsers' own SQLite files rather than an
// extension: no install per browser, no permissions dance, and full history
// including sessions predating this tool.
//
// The live file is always copied before opening. Chrome holds an exclusive lock
// while running, so opening in place either fails or — with some journal
// modes — can disturb the browser's own state. The copy is cheap and the
// correctness is not optional.

// Chromium stores microseconds since 1601-01-01.
const chromeEpochOffsetS int64 = 11_644_473_600

// WebKit stores seconds since 2001-01-01.
const safariEpochOffsetS int64 = 978_307_200

type Flavor int

const (
	Chromium Flavor = iota
	Safari
)

type Browser struct {
	Name   string
	Flavor Flavor
	DB     string
}

// DetectBrowsers returns only browsers whose history file exists here.
func DetectBrowsers() []Browser {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []Browser{
		{"chrome", Chromium, filepath.Join(home, "Library/Application Support/Google/Chrome/Default/History")},
		{"arc", Chromium, filepath.Join(home, "Library/Application Support/Arc/User Data/Default/History")},
		{"brave", Chromium, filepath.Join(home, "Library/Application Support/BraveSoftware/Brave-Browser/Default/History")},
		{"edge", Chromium, filepath.Join(home, "Library/Application Support/Microsoft Edge/Default/History")},
		{"safari", Safari, filepath.Join(home, "Library/Safari/History.db")},
	}

	var found []Browser
	for _, b := range candidates {
		if _, err := os.Stat(b.DB); err == nil {
			found = append(found, b)
		}
	}
	return found
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// snapshot copies the live history somewhere safe to open. WAL sidecars must
// come along or recent visits are missing from the copy.
func snapshot(db, scratch string) (string, error) {
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(scratch, "history-snapshot.db")

	if err := copyFile(db, dst); err != nil {
		return "", fmt.Errorf("copying %s: %w", db, err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(db + ext); err == nil {
			_ = copyFile(db+ext, dst+ext)
		}
	}
	return dst, nil
}

// VisitsSince returns visits newer than since (unix seconds) plus the new
// high-water mark, which the caller persists so the next poll is incremental.
func (b Browser) VisitsSince(since int64, scratch string) ([]event.Event, int64, error) {
	snap, err := snapshot(b.DB, scratch)
	if err != nil {
		return nil, since, err
	}
	defer func() {
		os.Remove(snap)
		os.Remove(snap + "-wal")
		os.Remove(snap + "-shm")
	}()

	conn, err := sql.Open("sqlite", "file:"+snap+"?mode=ro")
	if err != nil {
		return nil, since, err
	}
	defer conn.Close()

	var query string
	var cursorNative int64

	// Convert the cursor into the browser's own time base rather than
	// converting every row into ours — keeps the comparison indexed.
	switch b.Flavor {
	case Chromium:
		query = `SELECT v.visit_time, u.url, u.title
		         FROM visits v JOIN urls u ON u.id = v.url
		         WHERE v.visit_time > ? ORDER BY v.visit_time`
		cursorNative = (since + chromeEpochOffsetS) * 1_000_000
	case Safari:
		query = `SELECT v.visit_time, i.url, v.title
		         FROM history_visits v JOIN history_items i ON i.id = v.history_item
		         WHERE v.visit_time > ? ORDER BY v.visit_time`
		cursorNative = since - safariEpochOffsetS
	}

	rows, err := conn.Query(query, cursorNative)
	if err != nil {
		return nil, since, err
	}
	defer rows.Close()

	var events []event.Event
	high := cursorNative

	for rows.Next() {
		var native int64
		var url string
		var title sql.NullString
		if err := rows.Scan(&native, &url, &title); err != nil {
			return nil, since, err
		}
		if native > high {
			high = native
		}
		events = append(events, event.Event{
			TS: b.toUnix(native), Kind: event.URL, App: b.Name, URL: url, Title: title.String,
		})
	}

	highUnix := b.toUnix(high)
	if highUnix < since {
		highUnix = since
	}
	return events, highUnix, rows.Err()
}

func (b Browser) toUnix(native int64) int64 {
	if b.Flavor == Chromium {
		return native/1_000_000 - chromeEpochOffsetS
	}
	return native + safariEpochOffsetS
}
