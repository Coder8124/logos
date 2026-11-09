package transcript

// ClaudeProjectsRoot and CodexSessionsRoot are where each host keeps its
// transcripts, as every reader here resolves them — moved config directories
// and the test overrides included — so a reader outside this package (the
// spend dashboard) cannot look somewhere the importers do not. Empty when the
// host's directory is not there.
func ClaudeProjectsRoot() string { return claudeCodeReader{}.root() }

func CodexSessionsRoot() string { return codexReader{}.root() }
