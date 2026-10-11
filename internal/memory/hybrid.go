package memory

import (
	"math"
	"sort"
	"strings"
)

// Hybrid retrieval for memory: BM25 lexical fused with vector similarity by
// reciprocal rank fusion, the same idea the vault index uses.
//
// Pure vector recall blurs on the exact tokens that identify a specific
// memory — a name, a place, an error code, the one word the question shares
// with the session that answers it. Lexical matching catches those; fusion
// keeps the conceptual reach of embeddings. On LongMemEval this is aimed
// squarely at the single-session-user case, where the answer is one buried
// concrete statement.

// Candidate is one thing being ranked — a stored memory or a benchmark session.
type Candidate struct {
	ID   string
	Text string
	Vec  []float32
}

// Fuse returns a reciprocal-rank-fusion score per candidate (aligned to cands),
// combining vector similarity and BM25 lexical rankings, normalised to 0..1 by
// the top score so callers can blend it with other signals like salience.
func Fuse(query string, qVec []float32, cands []Candidate) []float64 {
	if len(cands) == 0 {
		return nil
	}
	type sc struct {
		i   int
		val float64
	}

	// Only a candidate with a vector takes part in the vector arm. A missing
	// vector would score cosine 0 and still collect a rank reward, so a
	// vectorless memory — or every memory, when the query itself could not be
	// embedded — would be ranked by the arbitrary order of equal zeros. A
	// vector of another length came from a model the user has since switched
	// away from, and is missing in the same way.
	var vec []sc
	if len(qVec) > 0 {
		for i, c := range cands {
			if len(c.Vec) == len(qVec) {
				vec = append(vec, sc{i, cosine(qVec, c.Vec)})
			}
		}
	}
	sort.Slice(vec, func(a, b int) bool { return vec[a].val > vec[b].val })

	docs := make([]string, len(cands))
	for i, c := range cands {
		docs[i] = c.Text
	}
	bm := bm25(query, docs)
	lex := make([]sc, len(cands))
	for i := range cands {
		lex[i] = sc{i, bm[i]}
	}
	sort.Slice(lex, func(a, b int) bool { return lex[a].val > lex[b].val })

	const rrfK = 60.0
	fused := make([]float64, len(cands))
	for rank, s := range vec {
		fused[s.i] += 1.0 / (rrfK + float64(rank))
	}
	for rank, s := range lex {
		// Only reward lexical rank when the doc actually matched a query term,
		// so a zero-overlap document does not ride its arbitrary bm25 ordering.
		if s.val > 0 {
			fused[s.i] += 1.0 / (rrfK + float64(rank))
		}
	}

	var mx float64
	for _, f := range fused {
		if f > mx {
			mx = f
		}
	}
	if mx > 0 {
		for i := range fused {
			fused[i] /= mx
		}
	}
	return fused
}

// Relevance floor for recall. Fuse ranks, and a rank says nothing about
// whether anything matched: the top candidate always scores 1.0, so every query
// returned up to its limit and reinforced all of it (#44). Measured with
// nomic-embed-text on ten short memories and ten queries: unrelated pairs sit
// at a median cosine of 0.36 with a maximum of 0.48, a clear answer at
// 0.73-0.87, and the one vague query ("which package manager") put its answer
// at 0.48 — level with the noise. So no absolute threshold separates them
// alone. MinCosine drops what no query should reach (nonsense tops out at
// 0.39); CosineGap drops the band of noise behind a strong answer, and leaves a
// vague query's flat field whole rather than guess at it. The nomic
// search_query/search_document prefixes were measured too and widened the
// overlap, so they are not sent.
const (
	MinCosine = 0.45
	CosineGap = 0.15
)

// Measured on a scratch vault of 432 memories about one project (its own 400
// commit subjects plus 32 labelled facts) and 18 labelled queries: any shared
// word admitted a memory, so a project's common vocabulary — "written",
// "claude code", "release" — let most of the vault through, and the 0.15 gap
// admitted the rest of a flat field. Weighting the shared words, and holding a
// vector-only match closer, took precision from 27% to 33% with the same
// recall there, and from 31% to 39% on the 32 facts alone, where three answers
// that had got in on a coincidental word ("tag" in "-tags") were lost. Swept 0.3-0.9 and
// 0.05-0.15: lower LexicalShare or a wider gap gave the noise back, a higher
// share or narrower gap started dropping answers. Ranking was not the lever:
// score fusion, qwen3-embedding:0.6b, and a 4B chat model as judge were all
// measured and none ranked the answers higher.
const (
	// LexicalShare is how much of the best match's shared-word weight a memory
	// must carry before its words are evidence on their own.
	LexicalShare = 0.7
	// VectorOnlyGap is CosineGap for a memory sharing no word with the query.
	// 0.12, not 0.10, which measured 2 points more precise on the 32 facts but
	// sits exactly on a paraphrase 0.10 below the answer, the case
	// TestAMemoryFarBelowTheBestMatchIsLeftOut keeps.
	VectorOnlyGap = 0.12
)

// Evidence reports, per candidate, whether anything ties it to the query:
// enough of the query's distinctive words, a few of them backed by a vector
// close to the best match, or a vector alone that is closer still. Without
// evidence a candidate is not an answer, however it ranks among the others.
func Evidence(query string, qVec []float32, cands []Candidate) []bool {
	cos := make([]float64, len(cands))
	best := math.Inf(-1)
	for i, c := range cands {
		cos[i] = math.Inf(-1)
		// Another model's vector is no vector: cosine scores the mismatch 0,
		// which would read as a vector that disagrees with the match.
		if len(qVec) > 0 && len(c.Vec) == len(qVec) {
			cos[i] = cosine(qVec, c.Vec)
			best = math.Max(best, cos[i])
		}
	}
	lex := sharedWeight(query, cands)
	bestLex := 0.0
	for _, l := range lex {
		bestLex = math.Max(bestLex, l)
	}
	near := func(i int, gap float64) bool { return cos[i] >= MinCosine && cos[i] >= best-gap }
	out := make([]bool, len(cands))
	for i := range cands {
		switch {
		case lex[i] == 0:
			out[i] = near(i, VectorOnlyGap)
		case lex[i] >= LexicalShare*bestLex:
			out[i] = true
		case math.IsInf(cos[i], -1):
			// No vector to confirm or refute a weak match: without a runtime
			// the words are all the evidence there is, and dropping them cost
			// a keyword-only machine over a third of its answers (86% recall
			// to 53% on the same 32 facts).
			out[i] = true
		default:
			out[i] = near(i, CosineGap)
		}
	}
	return out
}

// sharedWeight is, per candidate, the summed weight of the query words it
// shares, each weighted by how few candidates share it. A word most of the
// project uses ties a memory to the query about as much as the project name
// does; a word only the answer uses is what makes it the answer.
func sharedWeight(query string, cands []Candidate) []float64 {
	var qterms []string
	seen := map[string]bool{}
	for _, t := range tokenize(query) {
		if !gateStop[t] && !seen[t] {
			seen[t] = true
			qterms = append(qterms, t)
		}
	}
	has := make([][]bool, len(cands))
	df := make([]int, len(qterms))
	for i, c := range cands {
		toks := tokenize(c.Text)
		has[i] = make([]bool, len(qterms))
		for j, q := range qterms {
			if sharesATerm([]string{q}, toks) {
				has[i][j] = true
				df[j]++
			}
		}
	}
	n := float64(len(cands))
	out := make([]float64, len(cands))
	for i := range cands {
		for j := range qterms {
			if has[i][j] {
				out[i] += math.Log(1 + n/float64(df[j]))
			}
		}
	}
	return out
}

// sharesATerm is looser than bm25's exact match on purpose. With no embedding
// model the lexical arm is the only evidence there is, and "how should I
// reply" must still find "the user prefers short replies". It is a gate, not a
// ranker, so the stemming stays out of bm25, where it would move LongMemEval.
func sharesATerm(q, doc []string) bool {
	for _, a := range q {
		if gateStop[a] {
			continue
		}
		for _, b := range doc {
			if sameStem(a, b) {
				return true
			}
		}
	}
	return false
}

// gateStop are words a question and a memory share without being about the
// same thing: "what database do we use" and "we ship releases with argo cd"
// (#224). Kept out of the gate only, not out of stop, because stop feeds bm25
// and moving bm25 moves LongMemEval.
var gateStop = map[string]bool{
	"we": true, "our": true, "us": true, "should": true, "can": true, "could": true,
	"would": true, "will": true, "where": true, "why": true, "which": true, "who": true,
	"there": true, "their": true, "they": true, "your": true, "about": true, "from": true,
}

// sameStem: equal, or one word an inflection of the other. Past a shared prefix
// of at least three letters, what is left of each word must be an inflection
// ending (own/owned, reply/replies, release/releasing, stop/stopped). A bare
// shared prefix is not enough: use/user, replies/repo, form/format share letters
// and nothing else, and each such match returned and reinforced an unrelated
// memory (#224).
func sameStem(a, b string) bool {
	if a == b {
		return true
	}
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	if n < 3 {
		return false
	}
	ta, tb := tail(a[:n], a[n:]), tail(b[:n], b[n:])
	if ta > tb {
		ta, tb = tb, ta
	}
	switch ta + "|" + tb {
	case "|d", "|ed", "|es", "|ing", "|s", "d|s", "ed|es", "ed|ing", "ed|s", "es|ing", "es|s", "ing|s":
		return true
	case "e|ed", "e|es", "e|ing", "e|s":
		// release/releasing: the stem's silent e is dropped before -ing and
		// -ed. Bare "e" against nothing is car/care, two words.
		return true
	case "ied|y", "ies|y", "ied|ies":
		return true
	}
	return false
}

// tail is the part of a word past the shared prefix, with a doubled final
// consonant folded away (stop/stopped) and "d" kept only after an e
// (release/released), so fin/find is not an inflection.
func tail(prefix, rest string) string {
	last := prefix[len(prefix)-1]
	if len(rest) > 2 && rest[0] == last && (rest[1:] == "ed" || rest[1:] == "ing") {
		return rest[1:]
	}
	if rest == "d" && last != 'e' {
		return "x"
	}
	return rest
}

// HybridRank fuses vector and BM25 rankings, returning candidate IDs best-first.
func HybridRank(query string, qVec []float32, cands []Candidate, k int) []string {
	fused := Fuse(query, qVec, cands)
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if fused[order[a]] != fused[order[b]] {
			return fused[order[a]] > fused[order[b]]
		}
		return order[a] < order[b]
	})
	out := make([]string, 0, k)
	for i := 0; i < len(order) && i < k; i++ {
		out = append(out, cands[order[i]].ID)
	}
	return out
}

// bm25 scores each document against the query using Okapi BM25 with the
// candidate set as the corpus (idf derived from it). Standard k1/b.
func bm25(query string, docs []string) []float64 {
	const k1, b = 1.5, 0.75
	qterms := tokenize(query)
	if len(qterms) == 0 {
		return make([]float64, len(docs))
	}

	tokenized := make([][]string, len(docs))
	var totalLen int
	df := map[string]int{}
	for i, d := range docs {
		toks := tokenize(d)
		tokenized[i] = toks
		totalLen += len(toks)
		seen := map[string]bool{}
		for _, t := range toks {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	n := float64(len(docs))
	avgdl := float64(totalLen) / math.Max(1, n)

	scores := make([]float64, len(docs))
	for i, toks := range tokenized {
		tf := map[string]int{}
		for _, t := range toks {
			tf[t]++
		}
		dl := float64(len(toks))
		var score float64
		for _, q := range qterms {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[q])+0.5)/(float64(df[q])+0.5))
			score += idf * (f * (k1 + 1)) / (f + k1*(1-b+b*dl/avgdl))
		}
		scores[i] = score
	}
	return scores
}

var stop = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true, "to": true,
	"in": true, "on": true, "for": true, "is": true, "are": true, "was": true, "were": true,
	"i": true, "you": true, "my": true, "me": true, "it": true, "what": true, "when": true,
	"how": true, "did": true, "do": true, "does": true, "have": true, "has": true, "with": true,
	"that": true, "this": true, "at": true, "be": true, "as": true, "by": true,
}

// tokenize lowercases and keeps alphanumeric words, dropping stopwords so the
// lexical signal is the content terms that actually distinguish documents.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 1 {
			w := cur.String()
			if !stop[w] {
				out = append(out, w)
			}
		}
		cur.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}
