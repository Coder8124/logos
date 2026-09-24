package transcript

import "testing"

// The sweep skips a transcript without reading it when its path rules the
// project out, so a wrong "no" here is a lost session nobody hears about. It
// may say yes too often; the parsed cwd decides after it.
func TestAPathRulesOutOnlyTranscriptsThatCannotBeTheProjects(t *testing.T) {
	for _, c := range []struct {
		harness, path, project string
		want                   bool
	}{
		{"claude-code", "/h/.claude/projects/-Users-a-IdeaProjects-brain/s.jsonl", "brain", true},
		{"claude-code", "/h/.claude/projects/-Users-a-code-eco-game/s.jsonl", "eco-game", true},
		{"claude-code", "/h/.claude/projects/-Users-a-code-My-App/s.jsonl", "my-app", true},
		{"claude-code", "/h/.claude/projects/-Users-a-code-my-app-v2/s.jsonl", "my_app.v2", true},
		{"claude-code", "/h/.claude/projects/-Users-a-code-kestrel/s.jsonl", "brain", false},
		{"claude-code", "/h/.claude/projects/-Users-a-code-shop-cart/s.jsonl", "shop", true},
		{"claude-code", "/h/.claude/projects/-Users-a-code-shopping/s.jsonl", "shop", false},
		{"claude-code", "/h/.claude/projects/-Users-a-code-debrain/s.jsonl", "brain", false},
		{"cursor", "/h/cursor/state.vscdb#abc", "brain", true},
	} {
		if got := MayBelongTo(c.harness, c.path, c.project); got != c.want {
			t.Errorf("MayBelongTo(%s, %s, %s) = %v, want %v", c.harness, c.path, c.project, got, c.want)
		}
	}
}
