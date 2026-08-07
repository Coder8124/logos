// Package provider is one client for every model runtime.
//
// Ollama, LM Studio, Jan and Msty all speak OpenAI-compatible /v1, so the only
// thing that differs is the port and which models happen to be loaded. Cloud
// BYOK is the same surface with a base URL and a key.
package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Msg is one turn in a conversation.
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LocalEndpoint is a runtime we know how to find without configuration.
type LocalEndpoint struct {
	Name string
	URL  string
}

// Probed in this order; first match wins when nothing is configured.
var LocalEndpoints = []LocalEndpoint{
	{"Ollama", "http://localhost:11434/v1"},
	{"LM Studio", "http://localhost:1234/v1"},
	{"Jan", "http://localhost:1337/v1"},
	{"Msty", "http://localhost:10000/v1"},
}

type Provider struct {
	Name    string
	BaseURL string
	APIKey  string
	// Think controls how much a reasoning model reasons before answering:
	// "off", "low", "medium", "high" (empty = "low"). It matters because the
	// OpenAI-compatible /v1 endpoint gives a reasoning model no way to bound its
	// thinking, so it spends the whole token budget thinking and returns an empty
	// answer. When set (Ollama only), chat routes through the native /api/chat
	// endpoint, which honours it. "default" forces the plain /v1 path.
	Think string
	http  *http.Client
}

func New(name, baseURL, apiKey string) *Provider {
	return &Provider{
		Name:    name,
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		// Local generation on a 24B model genuinely takes a while.
		http: &http.Client{Timeout: 300 * time.Second},
	}
}

type Discovered struct {
	Provider *Provider
	Models   []string
}

// Discover probes the well-known ports and reports what is actually running.
// This is what lets the app say "found LM Studio running Qwen3" on first launch
// instead of presenting an empty endpoint configuration box.
func Discover() []Discovered {
	probe := &http.Client{Timeout: 400 * time.Millisecond}
	var out []Discovered

	for _, ep := range LocalEndpoints {
		res, err := probe.Get(ep.URL + "/models")
		if err != nil {
			continue
		}
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err = json.NewDecoder(res.Body).Decode(&list)
		res.Body.Close()
		if err != nil {
			continue
		}
		models := make([]string, 0, len(list.Data))
		for _, m := range list.Data {
			models = append(models, m.ID)
		}
		out = append(out, Discovered{New(ep.Name, ep.URL, ""), models})
	}
	return out
}

func (p *Provider) post(path string, body any, into any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", p.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	res, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable at %s: %w", p.Name, p.BaseURL, err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail bytes.Buffer
		detail.ReadFrom(res.Body)
		return fmt.Errorf("%s returned %s: %s", p.Name, res.Status, strings.TrimSpace(detail.String()))
	}
	return json.NewDecoder(res.Body).Decode(into)
}

func (p *Provider) Embed(model string, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	var res struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := p.post("/embeddings", map[string]any{"model": model, "input": inputs}, &res); err != nil {
		return nil, err
	}
	if len(res.Data) != len(inputs) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(inputs), len(res.Data))
	}

	out := make([][]float32, len(res.Data))
	for i, d := range res.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Chat runs a completion. A non-nil schema enables constrained decoding: small
// local models are unreliable at tool-calling but near-perfect when JSON is
// enforced at the sampler, which is why every extraction path goes through here
// rather than through tool definitions.
func (p *Provider) Chat(model, system, user string, schema map[string]any) (string, error) {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.2,
		"stream":      false,
	}
	if schema != nil {
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "out",
				"strict": true,
				"schema": schema,
			},
		}
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := p.post("/chat/completions", body, &res); err != nil {
		return "", err
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("no choices returned")
	}
	return res.Choices[0].Message.Content, nil
}

// ChatStream runs a multi-turn completion and streams tokens to onToken as they
// arrive. This is what makes the assistant feel alive: a 40-second answer that
// appears word by word reads as thinking, while the same answer delivered in
// one silent lump reads as broken.
//
// Returns the full assembled text so callers can persist the turn.
func (p *Provider) ChatStream(model string, messages []Msg, onToken func(string)) (string, error) {
	// Reasoning models on Ollama need the native endpoint to bound their thinking;
	// otherwise /v1 truncates on thinking and returns nothing. Fall back to /v1 if
	// the native path errors (e.g. a non-thinking model that rejects `think`) — but
	// only when it produced no text, so a usable partial answer is never re-run.
	if tv, ok := p.thinkValue(); ok {
		full, err := p.nativeChatStream(model, messages, tv, onToken)
		if err == nil || full != "" {
			return full, nil
		}
	}

	body := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": 0.4,
		"stream":      true,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", p.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	res, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s unreachable at %s: %w", p.Name, p.BaseURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail bytes.Buffer
		detail.ReadFrom(res.Body)
		return "", fmt.Errorf("%s returned %s: %s", p.Name, res.Status, strings.TrimSpace(detail.String()))
	}

	// OpenAI-compatible streaming is server-sent events: "data: {json}" lines
	// terminated by "data: [DONE]".
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var full strings.Builder

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		if tok := chunk.Choices[0].Delta.Content; tok != "" {
			full.WriteString(tok)
			onToken(tok)
		}
	}
	return full.String(), sc.Err()
}

// thinkValue resolves the Think setting to the value Ollama's native /api/chat
// expects, and whether the native path should be used at all. Only Ollama honours
// it; empty defaults to "low" so reasoning models return answers instead of
// nothing; "default" opts back out to the plain /v1 path.
func (p *Provider) thinkValue() (any, bool) {
	if p.Name != "Ollama" {
		return nil, false
	}
	level := p.Think
	if level == "" {
		level = "low"
	}
	switch level {
	case "off", "false", "none":
		return false, true
	case "low", "medium", "high":
		return level, true
	default: // "default" or anything unrecognised: use /v1 unchanged
		return nil, false
	}
}

// nativeChatStream streams a completion from Ollama's native /api/chat, which —
// unlike /v1 — lets a reasoning model's thinking be bounded by `think`. Only the
// answer (message.content) is streamed; the model's thinking is discarded.
func (p *Provider) nativeChatStream(model string, messages []Msg, think any, onToken func(string)) (string, error) {
	url := strings.TrimSuffix(p.BaseURL, "/v1") + "/api/chat"
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
		"think":    think,
		"options":  map[string]any{"temperature": 0.4},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	res, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s unreachable at %s: %w", p.Name, url, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail bytes.Buffer
		detail.ReadFrom(res.Body)
		return "", fmt.Errorf("%s /api/chat returned %s: %s", p.Name, res.Status, strings.TrimSpace(detail.String()))
	}

	// Native streaming is newline-delimited JSON, one object per line.
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var full strings.Builder

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if json.Unmarshal([]byte(line), &chunk) != nil {
			continue
		}
		if tok := chunk.Message.Content; tok != "" {
			full.WriteString(tok)
			onToken(tok)
		}
		if chunk.Done {
			break
		}
	}
	return full.String(), sc.Err()
}
