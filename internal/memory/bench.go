package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/provider"
)

// Benchmarking our memory against LongMemEval (ICLR 2025), the standard
// long-term memory benchmark for chat assistants.
//
// We measure retrieval recall — the metric that isolates the memory backend:
// given a question, did we surface the session that actually holds the answer?
// Each instance's chat history is loaded into a fresh memory store (one entry
// per session, embedded), then the question is used to recall the top-k; a hit
// is when a recalled entry comes from one of the evidence sessions. Reported
// per LongMemEval category, so the knowledge-update and temporal cases are
// visible separately.

// lmeInstance mirrors the LongMemEval JSON shape.
type lmeInstance struct {
	QuestionID       string      `json:"question_id"`
	QuestionType     string      `json:"question_type"`
	Question         string      `json:"question"`
	Answer           any         `json:"answer"`
	HaystackIDs      []string    `json:"haystack_session_ids"`
	HaystackSessions [][]lmeTurn `json:"haystack_sessions"`
	AnswerSessionIDs []string    `json:"answer_session_ids"`
}

type lmeTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Ks are the recall@k thresholds reported in one pass.
var Ks = []int{1, 3, 5, 10}

// BenchResult is the outcome for one category (or overall): hits at each k.
type BenchResult struct {
	Category string
	N        int
	HitsAt   map[int]int // k -> number of questions whose evidence was in the top k
}

func newResult(cat string) *BenchResult {
	return &BenchResult{Category: cat, HitsAt: map[int]int{}}
}

// RecallAt returns recall@k for this result.
func (r BenchResult) RecallAt(k int) float64 {
	if r.N == 0 {
		return 0
	}
	return float64(r.HitsAt[k]) / float64(r.N)
}

// RunLongMemEval evaluates retrieval recall at several k thresholds (Ks) over up
// to `limit` instances of a LongMemEval file, in one embedding pass.
func RunLongMemEval(p *provider.Provider, embedModel, path string, limit int, hybrid bool, progress func(done, total int)) ([]BenchResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var instances []lmeInstance
	if err := json.Unmarshal(raw, &instances); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Abstention questions reference events that never happened — LongMemEval
	// excludes them from retrieval scoring, and so do we.
	filtered := instances[:0]
	for _, in := range instances {
		if !strings.HasSuffix(in.QuestionType, "_abs") && len(in.AnswerSessionIDs) > 0 {
			filtered = append(filtered, in)
		}
	}
	instances = filtered
	// Stride-sample across the whole file rather than taking a prefix — the
	// instances are grouped by category, so a prefix would test only one type.
	// Striding gives every category proportional representation.
	if limit > 0 && limit < len(instances) {
		step := len(instances) / limit
		if step < 1 {
			step = 1
		}
		sampled := make([]lmeInstance, 0, limit)
		for i := 0; i < len(instances) && len(sampled) < limit; i += step {
			sampled = append(sampled, instances[i])
		}
		instances = sampled
	}

	byCat := map[string]*BenchResult{}
	overall := newResult("OVERALL")

	maxK := 0
	for _, k := range Ks {
		if k > maxK {
			maxK = k
		}
	}

	for i, in := range instances {
		rank, err := evidenceRank(p, embedModel, in, hybrid, maxK)
		if err != nil {
			return nil, err
		}
		cat := byCat[in.QuestionType]
		if cat == nil {
			cat = newResult(in.QuestionType)
			byCat[in.QuestionType] = cat
		}
		cat.N++
		overall.N++
		// A hit at k means the best evidence rank is within the top k.
		for _, k := range Ks {
			if rank >= 0 && rank < k {
				cat.HitsAt[k]++
				overall.HitsAt[k]++
			}
		}
		if progress != nil {
			progress(i+1, len(instances))
		}
	}

	out := []BenchResult{*overall}
	var cats []string
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		out = append(out, *byCat[c])
	}
	return out, nil
}

// evidenceRank returns the best (lowest) rank at which any evidence session
// appears in the retrieval ordering, or -1 if none within the top `depth`.
func evidenceRank(p *provider.Provider, embedModel string, in lmeInstance, hybrid bool, depth int) (int, error) {
	ordered, _, err := retrieveTopK(p, embedModel, in, hybrid, depth)
	if err != nil {
		return -1, err
	}

	evidence := map[string]bool{}
	for _, e := range in.AnswerSessionIDs {
		evidence[normalizeSessionID(e)] = true
	}
	for rank, id := range ordered {
		if evidence[normalizeSessionID(id)] {
			return rank, nil
		}
	}
	return -1, nil
}

// retrieveTopK runs the same ranking evidenceRank scores against, returning
// the ordered session ids alongside their full text so a QA pass can build an
// answer prompt from exactly what retrieval surfaced — not from the evidence
// sessions LongMemEval labels as correct, which the memory backend never sees.
func retrieveTopK(p *provider.Provider, embedModel string, in lmeInstance, hybrid bool, depth int) ([]string, map[string]string, error) {
	texts := make([]string, 0, len(in.HaystackSessions))
	ids := make([]string, 0, len(in.HaystackSessions))
	for si, sess := range in.HaystackSessions {
		var b strings.Builder
		for _, turn := range sess {
			b.WriteString(turn.Role)
			b.WriteString(": ")
			b.WriteString(turn.Content)
			b.WriteString("\n")
		}
		id := ""
		if si < len(in.HaystackIDs) {
			id = in.HaystackIDs[si]
		}
		texts = append(texts, b.String())
		ids = append(ids, id)
	}

	sessVecs, err := p.Embed(embedModel, truncateAll(texts, 2000))
	if err != nil {
		return nil, nil, err
	}
	qVec, err := p.Embed(embedModel, []string{in.Question})
	if err != nil || len(qVec) == 0 {
		return nil, nil, err
	}

	byID := make(map[string]string, len(ids))
	for i, id := range ids {
		byID[id] = texts[i]
	}

	var ordered []string
	if hybrid {
		cands := make([]Candidate, len(sessVecs))
		for i := range sessVecs {
			cands[i] = Candidate{ID: ids[i], Text: texts[i], Vec: sessVecs[i]}
		}
		ordered = HybridRank(in.Question, qVec[0], cands, depth)
	} else {
		type sc struct {
			id  string
			sim float64
		}
		r := make([]sc, len(sessVecs))
		for i := range sessVecs {
			r[i] = sc{ids[i], cosine(qVec[0], sessVecs[i])}
		}
		sort.Slice(r, func(a, b int) bool { return r[a].sim > r[b].sim })
		for i := 0; i < depth && i < len(r); i++ {
			ordered = append(ordered, r[i].id)
		}
	}
	return ordered, byID, nil
}

// normalizeSessionID reduces the two id conventions LongMemEval uses (a plain
// haystack id vs. an "answer_<hash>_<n>" evidence id) to a comparable core.
func normalizeSessionID(id string) string {
	id = strings.TrimPrefix(id, "answer_")
	return id
}

// QAResult is end-to-end accuracy for one LongMemEval category: a generated
// answer, graded against the gold answer, after retrieval — as opposed to
// BenchResult, which only asks whether the right session was surfaced. A
// system can have perfect recall and a wrong answer; this is the number that
// is actually comparable to another memory system's published LongMemEval
// score, because every competitor reports QA accuracy, not recall@k.
type QAResult struct {
	Category string
	N        int
	Correct  int
}

// Accuracy returns the fraction of this category's questions answered
// correctly.
func (r QAResult) Accuracy() float64 {
	if r.N == 0 {
		return 0
	}
	return float64(r.Correct) / float64(r.N)
}

// RunLongMemEvalQA runs the full pipeline LongMemEval actually measures:
// retrieve, generate an answer from what was retrieved, grade it against the
// gold answer. genModel answers the question from retrieved context alone;
// judgeModel (often the same model) grades the generated answer against the
// gold one. Both run through the local router, per this project's no-cloud-call
// rule — there is no hosted grading step hiding in here.
func RunLongMemEvalQA(p *provider.Provider, embedModel, genModel, judgeModel, path string, limit int, hybrid bool, depth int, progress func(done, total int)) ([]QAResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var instances []lmeInstance
	if err := json.Unmarshal(raw, &instances); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Abstention questions are scored on their own: a correct answer to one is
	// refusing to answer, which judgeAnswer treats as a distinct outcome rather
	// than grading it against a gold string that was never meant to be found.
	instances = sampleInstances(instances, limit)

	byCat := map[string]*QAResult{}
	overall := &QAResult{Category: "OVERALL"}

	for i, in := range instances {
		ordered, byID, err := retrieveTopK(p, embedModel, in, hybrid, depth)
		if err != nil {
			return nil, err
		}
		var ctx strings.Builder
		for _, id := range ordered {
			ctx.WriteString(byID[id])
			ctx.WriteString("\n---\n")
		}

		answer, err := p.Chat(genModel,
			"Answer the question using only the context given. If the context does not contain the answer, say you don't know — never guess.",
			fmt.Sprintf("Context:\n%s\n\nQuestion: %s", ctx.String(), in.Question),
			nil,
		)
		if err != nil {
			return nil, err
		}

		correct, err := judgeAnswer(p, judgeModel, in, answer)
		if err != nil {
			return nil, err
		}

		cat := byCat[in.QuestionType]
		if cat == nil {
			cat = &QAResult{Category: in.QuestionType}
			byCat[in.QuestionType] = cat
		}
		cat.N++
		overall.N++
		if correct {
			cat.Correct++
			overall.Correct++
		}
		if progress != nil {
			progress(i+1, len(instances))
		}
	}

	out := []QAResult{*overall}
	var cats []string
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		out = append(out, *byCat[c])
	}
	return out, nil
}

// judgeSchema constrains the grader to a single boolean field — the same
// trick Chat's doc comment explains: small local models follow a forced JSON
// shape far more reliably than they follow "answer yes or no" in prose.
var judgeSchema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{"correct": map[string]any{"type": "boolean"}},
	"required":             []string{"correct"},
	"additionalProperties": false,
}

// judgeAnswer asks judgeModel whether the generated answer matches the gold
// answer closely enough to count, the same way LongMemEval's own reference
// grader works: semantic match, not exact string equality — "around 3pm" and
// "15:00" are both correct for a question with gold answer "3:00 PM".
//
// An abstention question's gold answer is empty or absent; there, "correct"
// means the model declined rather than fabricated one, so the grading prompt
// is different for that case.
func judgeAnswer(p *provider.Provider, judgeModel string, in lmeInstance, generated string) (bool, error) {
	gold := goldAnswerText(in.Answer)
	abstention := strings.HasSuffix(in.QuestionType, "_abs") || len(in.AnswerSessionIDs) == 0

	var user string
	if abstention {
		user = fmt.Sprintf("Question: %s\n\nThe correct behavior is to say the information is not available. Model's answer: %q\n\nDid the model correctly decline to answer, rather than inventing one?", in.Question, generated)
	} else {
		user = fmt.Sprintf("Question: %s\nGold answer: %s\nModel's answer: %q\n\nDoes the model's answer match the gold answer in substance? Minor wording or formatting differences do not count against it.", in.Question, gold, generated)
	}

	out, err := p.Chat(judgeModel,
		"You are grading an answer against a known-correct answer. Respond only with the JSON schema given.",
		user,
		judgeSchema,
	)
	if err != nil {
		return false, err
	}
	var parsed struct {
		Correct bool `json:"correct"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return false, fmt.Errorf("judge returned non-conforming JSON: %w", err)
	}
	return parsed.Correct, nil
}

// goldAnswerText renders LongMemEval's answer field as text regardless of
// whether a given instance's gold answer is a string or a list of strings.
func goldAnswerText(a any) string {
	switch v := a.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, p := range v {
			parts = append(parts, fmt.Sprintf("%v", p))
		}
		return strings.Join(parts, "; ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// sampleInstances applies RunLongMemEval's stride-sampling (proportional
// category coverage instead of a prefix) without excluding abstention
// questions, since RunLongMemEvalQA — unlike recall scoring — grades them too.
func sampleInstances(instances []lmeInstance, limit int) []lmeInstance {
	if limit <= 0 || limit >= len(instances) {
		return instances
	}
	step := len(instances) / limit
	if step < 1 {
		step = 1
	}
	sampled := make([]lmeInstance, 0, limit)
	for i := 0; i < len(instances) && len(sampled) < limit; i += step {
		sampled = append(sampled, instances[i])
	}
	return sampled
}

func truncateAll(s []string, n int) []string {
	out := make([]string, len(s))
	for i, v := range s {
		if len(v) > n {
			v = v[:n]
		}
		out[i] = v
	}
	return out
}
