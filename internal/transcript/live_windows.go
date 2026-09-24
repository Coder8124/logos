//go:build windows

package transcript

// processAlive cannot be asked this cheaply on Windows, so no session is taken
// as open and the sweep falls back to how long a transcript has been quiet.
func processAlive(int) bool { return false }
