package graph

import (
	"math"
	"sort"
)

// Point is a node's position in a layout, in the units of the box it was laid
// out in.
type Point struct{ X, Y float64 }

// Layout places every node of g in a w×h box with a force-directed pass:
// edges pull their ends together, every pair of nodes pushes apart, and the
// focus is held at the centre throughout so the rest arranges itself around
// it — the eye starts where the ego graph does. The result is then stretched
// to fill the box, which can move the focus off dead centre by the same
// amount the picture was lopsided.
//
// It is deterministic — no random start, nodes seeded on rings by hop
// distance in slug order — because the same focus drawn twice in a row must
// look the same. A layout that reshuffles on every run reads as a different
// graph, and a user comparing two runs to see what a new link changed would be
// comparing noise.
func Layout(g Graph, w, h float64) map[string]Point {
	nodes := append([]Node(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Hops != nodes[j].Hops {
			return nodes[i].Hops < nodes[j].Hops
		}
		return nodes[i].Slug < nodes[j].Slug
	})
	pos := make(map[string]Point, len(nodes))
	if len(nodes) == 0 {
		return pos
	}
	cx, cy := w/2, h/2
	if len(nodes) == 1 {
		pos[nodes[0].Slug] = Point{cx, cy}
		return pos
	}

	// Seed: each hop ring its own circle, spread by index. Starting from rings
	// rather than a line keeps the pass from having to untangle a crossing
	// the seed itself put there.
	byRing := map[int][]string{}
	maxRing := 0
	for _, n := range nodes {
		byRing[n.Hops] = append(byRing[n.Hops], n.Slug)
		maxRing = max(maxRing, n.Hops)
	}
	for ring, slugs := range byRing {
		r := float64(ring) / float64(max(maxRing, 1)) * 0.45
		for i, s := range slugs {
			a := 2*math.Pi*float64(i)/float64(len(slugs)) + float64(ring)*0.7
			pos[s] = Point{cx + r*w*math.Cos(a), cy + r*h*math.Sin(a)}
		}
	}

	// Fruchterman–Reingold, in a unit square stretched to the box afterwards
	// by working in coordinates scaled per axis. k is the ideal edge length.
	k := math.Sqrt(w * h / float64(len(nodes)))
	temp := math.Min(w, h) / 8
	iters := 300
	for it := 0; it < iters; it++ {
		disp := make(map[string]Point, len(nodes))
		for i := range nodes {
			a := nodes[i].Slug
			for j := i + 1; j < len(nodes); j++ {
				b := nodes[j].Slug
				dx, dy := pos[a].X-pos[b].X, pos[a].Y-pos[b].Y
				d := math.Hypot(dx, dy)
				if d < 0.01 {
					// Coincident: push apart along a fixed direction chosen by
					// index, so determinism survives the tie.
					dx, dy, d = float64(i-j), 0.5, math.Hypot(float64(i-j), 0.5)
				}
				f := k * k / d
				disp[a] = Point{disp[a].X + dx/d*f, disp[a].Y + dy/d*f}
				disp[b] = Point{disp[b].X - dx/d*f, disp[b].Y - dy/d*f}
			}
		}
		for _, e := range g.Edges {
			pa, oka := pos[e.Src]
			pb, okb := pos[e.Dst]
			if !oka || !okb {
				continue
			}
			dx, dy := pa.X-pb.X, pa.Y-pb.Y
			d := math.Max(math.Hypot(dx, dy), 0.01)
			f := d * d / k
			if e.Provenance == Similarity {
				// A lens, not a fact: it may nudge the picture, not shape it.
				f *= 0.3
			}
			disp[e.Src] = Point{disp[e.Src].X - dx/d*f, disp[e.Src].Y - dy/d*f}
			disp[e.Dst] = Point{disp[e.Dst].X + dx/d*f, disp[e.Dst].Y + dy/d*f}
		}
		for _, n := range nodes {
			if n.Hops == 0 && n.Slug == g.Focus {
				continue
			}
			d := disp[n.Slug]
			l := math.Hypot(d.X, d.Y)
			if l == 0 {
				continue
			}
			step := math.Min(l, temp)
			p := pos[n.Slug]
			p.X = clamp(p.X+d.X/l*step, 0, w)
			p.Y = clamp(p.Y+d.Y/l*step, 0, h)
			pos[n.Slug] = p
		}
		temp *= 1 - 1/float64(iters)*3
		temp = math.Max(temp, math.Min(w, h)/200)
	}
	if _, ok := pos[g.Focus]; ok {
		pos[g.Focus] = Point{cx, cy}
	}
	return fit(pos, w, h)
}

// fit stretches the layout to fill the box. Repulsion leaves a cluster in the
// middle of a large empty square, and on a terminal every row left blank is
// one the drawing could have spent separating two labels.
func fit(pos map[string]Point, w, h float64) map[string]Point {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pos {
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
	}
	sx, sy := maxX-minX, maxY-minY
	for s, p := range pos {
		if sx > 0 {
			p.X = (p.X - minX) / sx * w
		} else {
			p.X = w / 2
		}
		if sy > 0 {
			p.Y = (p.Y - minY) / sy * h
		} else {
			p.Y = h / 2
		}
		pos[s] = p
	}
	return pos
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
