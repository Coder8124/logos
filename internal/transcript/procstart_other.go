//go:build !darwin

package transcript

import "time"

// processStart is unknown here; see claudeCodeOpen for what that costs.
func processStart(int) (time.Time, bool) { return time.Time{}, false }
