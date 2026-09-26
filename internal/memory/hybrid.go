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
	// embedded — would be ranked by the arbitrary order of equal zeros.
	var vec []sc
	if len(qVec) > 0 {
		for i, c := range cands {
			if len(c.Vec) > 0 {
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

// Evidence reports, per candidate, whether anything ties it to the query: a
// query term it contains, or a vector close enough both absolutely and to the
// best vector match. Without evidence a candidate is not an answer, however it
// ranks among the others.
func Evidence(query string, qVec []float32, cands []Candidate) []bool {
	qterms := tokenize(query)
	cos := make([]float64, len(cands))
	best := math.Inf(-1)
	for i, c := range cands {
		cos[i] = math.Inf(-1)
		if len(qVec) > 0 && len(c.Vec) > 0 {
			cos[i] = cosine(qVec, c.Vec)
			best = math.Max(best, cos[i])
		}
	}
	out := make([]bool, len(cands))
	for i := range cands {
		out[i] = sharesATerm(qterms, tokenize(cands[i].Text)) || (cos[i] >= MinCosine && cos[i] >= best-CosineGap)
	}
	return out
}

// sharesATerm is looser than bm25's exact match on purpose. With no embedding
// model the lexical arm is the only evidence there is, and "how should I
// reply" must still find "the user prefers short replies". It is a gate, not a
// ranker, so the stemming stays out of bm25, where it would move LongMemEval.
func sharesATerm(q, doc []string) bool {
	for _, a := range q {
		for _, b := range doc {
			if sameStem(a, b) {
				return true
			}
		}
	}
	return false
}

// sameStem: equal, or one word an inflection of the other — a shared prefix of
// at least three letters that stops at most two short of the shorter word
// (own/owns/owned, reply/replies, release/releases), which "cat" and "cache"
// do not have.
func sameStem(a, b string) bool {
	if a == b {
		return true
	}
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n >= 3 && n >= min(len(a), len(b))-2
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
