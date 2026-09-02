package capture

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/Coder8124/brain/internal/capture/sources"
)

// PollOnce runs one pass over the pull-based sources. The frontmost sampler is
// push-based and lives in the daemon loop instead.
//
// backfill caps how far back a first run reaches. A second brain with no
// history is amnesiac on day one, but silently ingesting a year of browsing
// should be a choice rather than a default.
func PollOnce(db *sql.DB, scratch string, repos []string, policy *Policy, backfill int64) (int, error) {
	var collected []Event

	for _, b := range sources.DetectBrowsers() {
		key := "browser:" + b.Name
		since := Cursor(db, key)
		if since == 0 && backfill > 0 {
			since = Now() - backfill
		}

		events, high, err := b.VisitsSince(since, scratch)
		if err != nil {
			// A locked or TCC-protected history file is expected, not fatal;
			// the remaining sources must still run.
			fmt.Printf("· %s history unavailable: %v\n", b.Name, err)
			continue
		}
		collected = append(collected, events...)
		if err := SetCursor(db, key, high); err != nil {
			return 0, err
		}
	}

	// Calendar reaches into the future and is refreshed wholesale rather than
	// cursored: the source returns a window from 12h ago onward, and we replace
	// exactly that window so a rescheduled meeting never leaves a ghost.
	if cal, err := sources.CalendarEvents(14); err == nil {
		kept := cal[:0]
		for _, e := range cal {
			if !policy.ShouldDrop(e) {
				kept = append(kept, e)
			}
		}
		ReplaceCalendarWindow(db, Now()-12*3600, kept)
	}

	for _, repo := range repos {
		key := "git:" + repo
		since := Cursor(db, key)
		if since == 0 && backfill > 0 {
			since = Now() - backfill
		}

		events, err := sources.CommitsSince(repo, since)
		if err != nil {
			continue
		}
		var high int64
		for _, e := range events {
			if e.TS > high {
				high = e.TS
			}
		}
		if high > 0 {
			if err := SetCursor(db, key, high); err != nil {
				return 0, err
			}
		}
		collected = append(collected, events...)
	}

	kept := collected[:0]
	for _, e := range collected {
		if !policy.ShouldDrop(e) {
			kept = append(kept, e)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].TS < kept[j].TS })

	return InsertMany(db, kept)
}
