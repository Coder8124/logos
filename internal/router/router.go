package router

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pragun/brain/internal/provider"
)

// ErrNoRuntime means nothing answered on any known local port.
//
// It is a condition, not a failure. Generation genuinely needs a model, but
// retrieval degrades to lexical and the whole continuity surface — checkpoint,
// resume, note_progress, before_you_try — needs no model at all. Callers that
// only want those should carry on with a nil Router rather than refusing to
// start, so this is a sentinel they can test for instead of matching on the
// message.
var ErrNoRuntime = errors.New("no local model runtime found — start Ollama, LM Studio, Jan or Msty")

// Capability is what a probe actually established about a model, as opposed to
// what the runtime advertised.
type Capability struct {
	Model string
	// Loads is false for models that list but fail to load — a corrupt or
	// version-mismatched pull, which is common enough to be worth catching
	// rather than letting it surface as a pipeline error at 3am.
	Loads bool
	// StructuredOutput is false when the endpoint ignores or rejects
	// response_format. Extraction must not be routed here.
	StructuredOutput bool
	Err              string
}

type Router struct {
	cfg      *Config
	local    *provider.Provider
	models   map[string]bool
	caps     map[string]Capability
	vault    string
	verbose  bool
	resolved map[Tier]string // tier → model actually in use after fallback
}

func New(cfg *Config, vault string) (*Router, error) {
	if cfg == nil {
		cfg = Defaults()
	}
	found := provider.Discover()
	if len(found) == 0 {
		return nil, ErrNoRuntime
	}
	// Carry the thinking-budget setting onto the local provider, so a reasoning
	// model is bounded and returns an answer rather than thinking until it runs
	// out of tokens.
	found[0].Provider.Think = cfg.Think

	models := make(map[string]bool, len(found[0].Models))
	for _, m := range found[0].Models {
		models[m] = true
		// Runtimes list "qwen3.6:latest" but users write "qwen3.6".
		if base, _, ok := strings.Cut(m, ":"); ok {
			models[base] = true
		}
	}

	return &Router{
		cfg:      cfg,
		local:    found[0].Provider,
		models:   models,
		caps:     map[string]Capability{},
		vault:    vault,
		resolved: map[Tier]string{},
	}, nil
}

func (r *Router) Local() *provider.Provider { return r.local }
func (r *Router) SetVerbose(v bool)         { r.verbose = v }

// Probe verifies a model can actually run and whether it honours constrained
// decoding. Cheap enough to run at startup; cached for the process lifetime.
func (r *Router) Probe(model string) Capability {
	if c, ok := r.caps[model]; ok {
		return c
	}

	cap := Capability{Model: model}
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
		"required":             []string{"ok"},
		"additionalProperties": false,
	}

	out, err := r.local.Chat(model, "Reply with JSON only.", `Set ok to true.`, schema)
	switch {
	case err == nil:
		cap.Loads = true
		cap.StructuredOutput = strings.Contains(out, "ok")
	default:
		cap.Err = err.Error()
		// Distinguish "cannot load" from "does not support schemas": retry
		// without the schema, and if that works the model is fine for prose.
		if _, plainErr := r.local.Chat(model, "Reply with the word ok.", "ok", nil); plainErr == nil {
			cap.Loads = true
			cap.StructuredOutput = false
		}
	}

	r.caps[model] = cap
	return cap
}

// Model resolves a tier to a model that exists. A tier whose configured model is
// missing or broken degrades to the next tier down with a warning, rather than
// failing the whole pipeline — a rollup running on a smaller model is worth more
// than no rollup.
func (r *Router) Model(t Tier) (string, error) {
	if m, ok := r.resolved[t]; ok {
		return m, nil
	}

	for tier := t; ; tier-- {
		tc, ok := r.cfg.Tiers[tier.String()]
		if ok && tc.Model != "" && r.models[tc.Model] {
			if tier != t {
				fmt.Fprintf(os.Stderr, "· %s unavailable, falling back to %s (%s)\n", t, tier, tc.Model)
			}
			r.resolved[t] = tc.Model
			return tc.Model, nil
		}
		if tier == T0 {
			break
		}
	}
	return "", fmt.Errorf("no model available for %s or any lower tier", t)
}

// ModelFor is Model plus a structured-output requirement. Extraction callers use
// this so a model that cannot honour a schema is never handed a schema.
func (r *Router) ModelFor(t Tier, needSchema bool) (string, error) {
	m, err := r.Model(t)
	if err != nil {
		return "", err
	}
	if !needSchema {
		return m, nil
	}

	if cap := r.Probe(m); !cap.StructuredOutput {
		for tier := t; tier > T0; tier-- {
			tc, ok := r.cfg.Tiers[tier.String()]
			if !ok || !r.models[tc.Model] || tc.Model == m {
				continue
			}
			if r.Probe(tc.Model).StructuredOutput {
				fmt.Fprintf(os.Stderr, "· %s does not honour JSON schemas, using %s\n", m, tc.Model)
				return tc.Model, nil
			}
		}
		return "", fmt.Errorf("no model available that honours JSON schemas (tried %s)", m)
	}
	return m, nil
}

// Available reports every configured tier and what it resolved to, for `brain
// doctor`. The first-run greeting is built from this.
func (r *Router) Available() []string {
	var out []string
	for _, t := range []Tier{T0, T1, T2, T3} {
		tc := r.cfg.Tiers[t.String()]
		switch {
		case tc.Model == "":
			out = append(out, fmt.Sprintf("%s  (unconfigured)", t))
		case t == T3:
			state := "off — no egress"
			if tc.CloudOK {
				state = "enabled"
			}
			out = append(out, fmt.Sprintf("%s  %-24s cloud, %s", t, tc.Model, state))
		case r.models[tc.Model]:
			out = append(out, fmt.Sprintf("%s  %-24s local", t, tc.Model))
		default:
			out = append(out, fmt.Sprintf("%s  %-24s NOT INSTALLED", t, tc.Model))
		}
	}
	return out
}
