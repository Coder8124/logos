package secret

import "sync"

// A line is checked against every rule's keywords at once, in one pass.
// Checking them one strings.Contains at a time was a pass over the line per
// keyword — about 170 passes — and made masking a 5 MB transcript three times
// slower than before the provider rules existed. An Aho-Corasick automaton
// over the keywords reads each byte once, and tells which rules could match.

// hitSet is a bitmap of rule indexes whose keyword appeared in a line.
type hitSet [4]uint64

func (h *hitSet) add(i int)      { h[i>>6] |= 1 << (i & 63) }
func (h *hitSet) has(i int) bool { return h[i>>6]&(1<<(i&63)) != 0 }

type acNode struct {
	next [256]int32 // full transition table: no failure-link walk while scanning
	out  hitSet     // rules whose keyword ends here, failure links folded in
}

type keywordScanner struct{ nodes []acNode }

// newKeywordScanner builds the automaton. keywords[i] lists rule i's literals,
// already lowercase; the scan lowercases ASCII as it reads, so the caller does
// not need a lowered copy of the line.
func newKeywordScanner(keywords [][]string) *keywordScanner {
	s := &keywordScanner{nodes: make([]acNode, 1)}
	for rule, ks := range keywords {
		for _, k := range ks {
			cur := int32(0)
			for i := 0; i < len(k); i++ {
				c := k[i]
				if s.nodes[cur].next[c] == 0 {
					s.nodes = append(s.nodes, acNode{})
					s.nodes[cur].next[c] = int32(len(s.nodes) - 1)
				}
				cur = s.nodes[cur].next[c]
			}
			s.nodes[cur].out.add(rule)
		}
	}
	// Breadth-first, so a node's failure target is complete before it is used.
	fail := make([]int32, len(s.nodes))
	queue := []int32{}
	for c := 0; c < 256; c++ {
		if n := s.nodes[0].next[c]; n != 0 {
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for c := 0; c < 256; c++ {
			n := s.nodes[cur].next[c]
			if n == 0 {
				s.nodes[cur].next[c] = s.nodes[fail[cur]].next[c]
				continue
			}
			fail[n] = s.nodes[fail[cur]].next[c]
			for w := range s.nodes[n].out {
				s.nodes[n].out[w] |= s.nodes[fail[n]].out[w]
			}
			queue = append(queue, n)
		}
	}
	return s
}

// scan reports which rules have a keyword in line, ignoring ASCII case.
func (s *keywordScanner) scan(line string) hitSet {
	var hits hitSet
	cur := int32(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		cur = s.nodes[cur].next[c]
		if o := &s.nodes[cur].out; o[0]|o[1]|o[2]|o[3] != 0 {
			for w := range hits {
				hits[w] |= o[w]
			}
		}
	}
	return hits
}

// Rule indexes past the token rules, for the keywords that gate the header
// checks in pii.go.
const (
	hitCookie = iota
	hitKeyHeader
	hitExtra
)

var scanner = sync.OnceValue(func() *keywordScanner {
	kw := make([][]string, 0, len(tokenRules)+hitExtra)
	for _, r := range tokenRules {
		kw = append(kw, r.keywords)
	}
	if len(kw) > 256-hitExtra {
		panic("secret: more rules than the hit set holds")
	}
	kw = append(kw, []string{"cookie"}, []string{"x-api", "x-auth", "x-access", "x-csrf", "x-xsrf", "x-amz-security", "x-goog-api", "proxy-authorization", "api-key", "apikey"})
	return newKeywordScanner(kw)
})
