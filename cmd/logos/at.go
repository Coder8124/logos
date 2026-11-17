package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// atUsage is the --at value forms, quoted in every error so a rejected value
// says what would have been taken.
const atUsage = "a date (2026-11-03), a time (2026-11-03T14:05, RFC 3339) or unix seconds"

// parseAt reads --at, the moment a note, memory or checkpoint records rather
// than the moment it was typed. Importers and scripts that replay a history
// had no way to say "this was decided on Tuesday": everything landed as
// "today", so staleness, ordering and "recorded before the file changed" all
// read wrong. A time in the future is refused — it would sort after every
// real write for as long as the vault exists, and supersede them all.
func parseAt(v string, now time.Time) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("--at needs a value: %s", atUsage)
	}
	var t time.Time
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		t = time.Unix(n, 0)
	} else {
		var parsed bool
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
			if p, err := time.ParseInLocation(layout, v, time.Local); err == nil {
				t, parsed = p, true
				break
			}
		}
		if !parsed {
			return 0, fmt.Errorf("--at %q is not a time: use %s", v, atUsage)
		}
	}
	if t.After(now.Add(time.Minute)) {
		return 0, fmt.Errorf("--at %s is in the future; a record can only be dated to when it happened", t.Format(time.RFC3339))
	}
	return t.Unix(), nil
}

// datedSuffix is the receipt's mention of a backdated record. A record filed
// under another date is silently out of place in every listing otherwise.
func datedSuffix(ts int64) string {
	if ts == 0 {
		return ""
	}
	return " Dated " + time.Unix(ts, 0).Format("2006-01-02 15:04") + "."
}
