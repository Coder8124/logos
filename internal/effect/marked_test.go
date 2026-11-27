package effect

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/transcript"
)

// TestTheListsAreScoredAgainstTurnsAPersonMarked scores the two detectors here against turns a person
// marked, on the transcripts of the machine it runs on. It is how a change to
// either list is judged: the lists are openings and phrasings, anyone can add
// a line that reads well, and the only way to know whether a line helped is to
// count what it found and what it mistook.
//
// LOGOS_MARKED names a tab-separated file, a line to each turn a person wrote:
//
//	harness  session id  turn  rework  correction  set
//
// turn is the turn's place in the session as its reader returns it; rework and
// correction are y, n, or ? for a turn nobody could call, which is counted and
// scored as neither; set is any name, and each set is scored on its own — keep
// one that no list was ever tuned against and trust that one. The file holds
// no conversation text, and neither does what this prints.
//
// LOGOS_MARKED_SHOW names the sets whose mistakes are listed, by turn; the
// default is none, so a held-out set is not read by accident.
func TestTheListsAreScoredAgainstTurnsAPersonMarked(t *testing.T) {
	path := os.Getenv("LOGOS_MARKED")
	if path == "" {
		t.Skip("LOGOS_MARKED is not set: no marked turns to score against")
	}
	type mark struct{ rework, correction, set string }
	marks := map[string]mark{}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 6 {
			t.Fatalf("%s line %d: want 6 tab-separated fields, got %d", path, n, len(p))
		}
		marks[p[0]+"\t"+p[1]+"\t"+p[2]] = mark{p[3], p[4], p[5]}
	}
	show := map[string]bool{}
	for _, s := range strings.Split(os.Getenv("LOGOS_MARKED_SHOW"), ",") {
		show[strings.TrimSpace(s)] = true
	}

	found := map[string]map[string]string{"rework": {}, "correction": {}}
	read := map[string]bool{}
	for _, h := range transcript.Harnesses() {
		paths, err := transcript.Sessions(h)
		if err != nil {
			continue
		}
		for _, p := range paths {
			s, err := transcript.ReadFile(h, p)
			if err != nil {
				continue
			}
			read[h+"\t"+s.ID] = true
			for _, r := range Replies(s) {
				if r.Rework != "" {
					found["rework"][h+"\t"+s.ID+"\t"+strconv.Itoa(r.Turn)] = r.Rework
				}
			}
			for _, c := range corrections(s, Hold(nil, nil)) {
				found["correction"][h+"\t"+s.ID+"\t"+strconv.Itoa(c.Turn)] = "cue"
			}
		}
	}

	type tally struct {
		tp, fp, fn, unsure, missing int
		how                         map[string][2]int // by how it was found: right, wrong
	}
	for _, kind := range []string{"rework", "correction"} {
		per := map[string]*tally{}
		var lines []string
		for key, m := range marks {
			ta := per[m.set]
			if ta == nil {
				ta = &tally{how: map[string][2]int{}}
				per[m.set] = ta
			}
			p := strings.Split(key, "\t")
			if !read[p[0]+"\t"+p[1]] {
				ta.missing++ // its transcript is not on this machine
				continue
			}
			want := m.rework
			if kind == "correction" {
				want = m.correction
			}
			how, got := found[kind][key]
			verdict := ""
			switch {
			case want == "?":
				ta.unsure++
			case want == "y" && got:
				ta.tp++
				ta.how[how] = [2]int{ta.how[how][0] + 1, ta.how[how][1]}
			case want == "y":
				ta.fn++
				verdict = "missed"
			case got:
				ta.fp++
				ta.how[how] = [2]int{ta.how[how][0], ta.how[how][1] + 1}
				verdict = "mistook"
			}
			if verdict != "" && show[m.set] {
				lines = append(lines, fmt.Sprintf("  %-7s %s %s %.8s turn %s %s", verdict, m.set, p[0], p[1], p[2], how))
			}
		}
		// A turn found that nobody marked is a turn the file does not know a
		// person wrote, which is the file's gap and is said rather than scored.
		unmarked := 0
		for key := range found[kind] {
			if _, ok := marks[key]; !ok {
				unmarked++
			}
		}
		sets := make([]string, 0, len(per))
		for s := range per {
			sets = append(sets, s)
		}
		sort.Strings(sets)
		for _, s := range sets {
			ta := per[s]
			t.Logf("%-10s %-8s precision %s  recall %s  F1 %s   (found %d of %d, mistook %d; %d unsure, %d not on this machine)",
				kind, s, share(ta.tp, ta.tp+ta.fp), share(ta.tp, ta.tp+ta.fn), f1(ta.tp, ta.fp, ta.fn),
				ta.tp, ta.tp+ta.fn, ta.fp, ta.unsure, ta.missing)
			hows := make([]string, 0, len(ta.how))
			for h := range ta.how {
				hows = append(hows, h)
			}
			sort.Strings(hows)
			for _, h := range hows {
				if len(hows) > 1 {
					t.Logf("%-10s %-8s   %-7s right %d, wrong %d", kind, s, h, ta.how[h][0], ta.how[h][1])
				}
			}
		}
		if unmarked > 0 {
			t.Logf("%-10s %d found in turns the file does not mark", kind, unmarked)
		}
		sort.Strings(lines)
		for _, l := range lines {
			t.Log(l)
		}
	}
}

func share(n, of int) string {
	if of == 0 {
		return "  -"
	}
	return fmt.Sprintf("%3.0f%%", 100*float64(n)/float64(of))
}

func f1(tp, fp, fn int) string {
	if 2*tp+fp+fn == 0 {
		return "  -"
	}
	return fmt.Sprintf("%.2f", 2*float64(tp)/float64(2*tp+fp+fn))
}
