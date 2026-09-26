package reqstate

import (
	"fmt"
	"math/rand"
	"testing"
)

// checkAll compares every requirement's state and gap plus the summary
// between the incremental engine and the from-scratch reference.
func checkAll(t *testing.T, eng *Engine, ref *reference) {
	t.Helper()
	states, gaps, sum := ref.recompute()
	for id := range ref.reqs {
		s, g, err := eng.Requirement(id)
		if err != nil {
			t.Fatalf("Requirement(%s): %v", id, err)
		}
		if s != states[id] || g != gaps[id] {
			t.Fatalf("req %s: engine=(%v,%v) ref=(%v,%v)", id, s, g, states[id], gaps[id])
		}
	}
	if got := eng.Summary(); got != sum {
		t.Fatalf("summary: engine=%+v ref=%+v", got, sum)
	}
}

// gen is a random operation generator driving engine and reference in
// lockstep. Failed engine operations leave both untouched.
type gen struct {
	rnd     *rand.Rand
	eng     *Engine
	ref     *reference
	stats   *genStats
	reqs    []string
	tcs     []string
	execs   []string
	ans     []string
	edges   [][2]string
	links   [][2]string
	counter int
}

func (g *gen) fresh(prefix string) string {
	g.counter++
	return fmt.Sprintf("%s%d", prefix, g.counter)
}

func (g *gen) pickReq() string {
	if len(g.reqs) == 0 {
		return "nope"
	}
	return g.reqs[g.rnd.Intn(len(g.reqs))]
}

func (g *gen) pickTC() string {
	if len(g.tcs) == 0 || g.rnd.Intn(4) == 0 {
		return g.fresh("tc")
	}
	return g.tcs[g.rnd.Intn(len(g.tcs))]
}

func (g *gen) step() {
	// A small share of steps are targeted at the dual-path anomaly
	// situation so the generator reliably produces it.
	switch {
	case g.rnd.Intn(100) < 3:
		g.targetedDualPathCreate()
		return
	case g.rnd.Intn(100) < 3:
		g.targetedDualPathRemove()
		return
	}
	r := g.rnd.Intn(100)
	switch {
	case r < 12: // add requirement (sometimes duplicate)
		id := g.fresh("r")
		if len(g.reqs) > 0 && g.rnd.Intn(5) == 0 {
			id = g.pickReq()
		}
		if err := g.eng.AddRequirement(id); err == nil {
			g.ref.reqs[id] = true
			g.reqs = append(g.reqs, id)
		}
	case r < 24: // add edge: biased to layered diamonds, may cycle/dup
		a, b := g.pickReq(), g.pickReq()
		if len(g.reqs) >= 4 && g.rnd.Intn(2) == 0 {
			// pick from adjacent index bands to encourage diamonds
			i := g.rnd.Intn(len(g.reqs) - 1)
			a = g.reqs[i]
			b = g.reqs[i+1+g.rnd.Intn(len(g.reqs)-i-1)]
		}
		if err := g.eng.AddEdge(a, b); err == nil {
			g.ref.edges[[2]string{a, b}] = true
			g.edges = append(g.edges, [2]string{a, b})
		}
	case r < 30: // remove edge (often non-existent)
		var e [2]string
		idx := -1
		if len(g.edges) > 0 && g.rnd.Intn(2) == 0 {
			idx = g.rnd.Intn(len(g.edges))
			e = g.edges[idx]
		} else {
			e = [2]string{g.pickReq(), g.pickReq()}
		}
		if err := g.eng.RemoveEdge(e[0], e[1]); err == nil {
			delete(g.ref.edges, e)
			if idx >= 0 {
				g.edges[idx] = g.edges[len(g.edges)-1]
				g.edges = g.edges[:len(g.edges)-1]
			}
		}
	case r < 42: // link test case (sometimes duplicate / unknown req)
		req, tc := g.pickReq(), g.pickTC()
		if g.rnd.Intn(8) == 0 {
			req = "ghost"
		}
		if err := g.eng.LinkTestCase(req, tc); err == nil {
			g.ref.links[[2]string{req, tc}] = true
			g.links = append(g.links, [2]string{req, tc})
			g.trackTC(tc)
			if g.stats != nil {
				g.stats.onLink(req, tc, g.ref)
				g.stats.rescanDualPath(g.ref)
			}
		}
	case r < 48: // unlink test case (often non-existent)
		var l [2]string
		idx := -1
		if len(g.links) > 0 && g.rnd.Intn(2) == 0 {
			idx = g.rnd.Intn(len(g.links))
			l = g.links[idx]
		} else {
			l = [2]string{g.pickReq(), g.pickTC()}
		}
		if err := g.eng.UnlinkTestCase(l[0], l[1]); err == nil {
			delete(g.ref.links, l)
			if idx >= 0 {
				g.links[idx] = g.links[len(g.links)-1]
				g.links = g.links[:len(g.links)-1]
			}
			if g.stats != nil {
				g.stats.onUnlink(l[0], l[1], g.ref)
			}
		}
	case r < 66: // record execution (out of order, small at range, sometimes dup id)
		id := g.fresh("e")
		if len(g.execs) > 0 && g.rnd.Intn(6) == 0 {
			id = g.execs[g.rnd.Intn(len(g.execs))]
		}
		tc := g.pickTC()
		at := int64(g.rnd.Intn(12))
		res := []Result{Pass, Fail, Incomplete}[g.rnd.Intn(3)]
		if err := g.eng.RecordExecution(id, tc, at, res); err == nil {
			g.ref.seq++
			g.ref.execs[id] = refExec{tc: tc, at: at, seq: g.ref.seq, result: res}
			g.execs = append(g.execs, id)
			g.trackTC(tc)
			if g.stats != nil {
				g.stats.onRecord(tc)
			}
		}
	case r < 72: // delete execution (sometimes unknown)
		id := "ghost"
		if len(g.execs) > 0 {
			id = g.execs[g.rnd.Intn(len(g.execs))]
		}
		if err := g.eng.DeleteExecution(id); err == nil {
			if g.stats != nil {
				g.stats.onDeleteExec(id, g.ref)
			}
			delete(g.ref.execs, id)
		}
	case r < 84: // create anomaly (bias severity 1; sometimes dup/invalid/unknown req)
		id := g.fresh("a")
		if len(g.ans) > 0 && g.rnd.Intn(6) == 0 {
			id = g.ans[g.rnd.Intn(len(g.ans))]
		}
		sev := 1
		if g.rnd.Intn(2) == 0 {
			sev = 1 + g.rnd.Intn(4)
		}
		if g.rnd.Intn(10) == 0 {
			sev = g.rnd.Intn(7) // may be 0,5,6 -> invalid
		}
		var reqs []string
		for i := 0; i < g.rnd.Intn(3); i++ {
			reqs = append(reqs, g.pickReq())
		}
		if g.rnd.Intn(10) == 0 {
			reqs = append(reqs, "ghost")
		}
		var tcs []string
		for i := 0; i < g.rnd.Intn(3); i++ {
			tcs = append(tcs, g.pickTC())
		}
		if err := g.eng.CreateAnomaly(id, sev, reqs, tcs); err == nil {
			g.ref.anomalies[id] = refAnomaly{severity: sev, state: Open, reqs: reqs, tcs: tcs}
			g.ans = append(g.ans, id)
			for _, tc := range tcs {
				g.trackTC(tc)
			}
			if g.stats != nil {
				g.stats.onCreateAnomaly(g.ref.anomalies[id])
				g.stats.rescanDualPath(g.ref)
			}
		}
	default: // transition anomaly (often illegal)
		if len(g.ans) == 0 {
			return
		}
		id := g.ans[g.rnd.Intn(len(g.ans))]
		to := AnomalyState(g.rnd.Intn(4))
		from := g.ref.anomalies[id].state
		if err := g.eng.TransitionAnomaly(id, to); err == nil {
			an := g.ref.anomalies[id]
			an.state = to
			g.ref.anomalies[id] = an
			if g.stats != nil && from == Resolved && to == Open {
				g.stats.reopened = true
			}
		}
	}
}

// targetedDualPathCreate creates a severity-1 anomaly that hits one
// requirement both directly and through a linked test case.
func (g *gen) targetedDualPathCreate() {
	if len(g.links) == 0 {
		return
	}
	l := g.links[g.rnd.Intn(len(g.links))]
	id := g.fresh("a")
	if err := g.eng.CreateAnomaly(id, 1, []string{l[0]}, []string{l[1]}); err == nil {
		g.ref.anomalies[id] = refAnomaly{severity: 1, state: Open, reqs: []string{l[0]}, tcs: []string{l[1]}}
		g.ans = append(g.ans, id)
		if g.stats != nil {
			g.stats.onCreateAnomaly(g.ref.anomalies[id])
			g.stats.rescanDualPath(g.ref)
		}
	}
}

// targetedDualPathRemove finds an active severity-1 anomaly that currently
// hits a requirement via both paths and removes the test-case path.
func (g *gen) targetedDualPathRemove() {
	for _, id := range g.ans {
		an := g.ref.anomalies[id]
		if an.severity != 1 || (an.state != Open && an.state != UnderInvestigation) {
			continue
		}
		for _, r := range an.reqs {
			for _, tc := range an.tcs {
				key := [2]string{r, tc}
				if !g.ref.links[key] {
					continue
				}
				if err := g.eng.UnlinkTestCase(r, tc); err == nil {
					delete(g.ref.links, key)
					for i, l := range g.links {
						if l == key {
							g.links[i] = g.links[len(g.links)-1]
							g.links = g.links[:len(g.links)-1]
							break
						}
					}
					if g.stats != nil {
						g.stats.onUnlink(r, tc, g.ref)
					}
				}
				return
			}
		}
	}
}

func (g *gen) trackTC(tc string) {
	for _, x := range g.tcs {
		if x == tc {
			return
		}
	}
	g.tcs = append(g.tcs, tc)
}

func TestDifferentialRandom(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			g := &gen{rnd: rand.New(rand.NewSource(seed)), eng: New(), ref: newReference()}
			for i := 0; i < 4000; i++ {
				g.step()
				if i%50 == 0 {
					checkAll(t, g.eng, g.ref)
				}
			}
			checkAll(t, g.eng, g.ref)
		})
	}
}
