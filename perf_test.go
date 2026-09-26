package reqv

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// buildDiamond creates a DAG with `layers` layers of 2 nodes each, adjacent
// layers fully connected (each node linked to both nodes of the next layer).
func buildDiamond(t *testing.T, layers int) *Engine {
	t.Helper()
	e := New()
	id := func(l, n int) string { return fmt.Sprintf("L%d_N%d", l, n) }
	for l := 0; l < layers; l++ {
		for n := 0; n < 2; n++ {
			if err := e.AddRequirement(id(l, n)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for l := 0; l < layers-1; l++ {
		for a := 0; a < 2; a++ {
			for b := 0; b < 2; b++ {
				if err := e.AddEdge(id(l, a), id(l+1, b)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return e
}

func TestDiamond40(t *testing.T) {
	const layers = 40
	const budget = 50 * time.Millisecond

	run := func(name string, f func() error) {
		start := time.Now()
		err := f()
		el := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if el > budget {
			t.Fatalf("%s took %v, budget %v", name, el, budget)
		}
		t.Logf("%s: %v", name, el)
	}

	// Write at the bottom of a fresh diamond: link a failing test case.
	e1 := buildDiamond(t, layers)
	run("record exec", func() error {
		return e1.RecordExecution("x1", "tc-bottom", 1, ResultFail)
	})
	run("link bottom req", func() error {
		return e1.LinkTestCase(fmt.Sprintf("L%d_N0", layers-1), "tc-bottom")
	})
	if st, _, _ := e1.Requirement("L0_N0"); st != StatusFailed {
		t.Fatalf("top should be Failed, got %v", st)
	}
	run("delete exec", func() error {
		return e1.DeleteExecution("x1")
	})
	if st, _, _ := e1.Requirement("L0_N0"); st != StatusPending {
		t.Fatalf("top should be Pending after delete, got %v", st)
	}

	// Cycle-creating edge request on a fresh diamond: bottom -> top.
	e2 := buildDiamond(t, layers)
	start := time.Now()
	err := e2.AddEdge(fmt.Sprintf("L%d_N0", layers-1), "L0_N0")
	el := time.Since(start)
	if err != ErrCycle {
		t.Fatalf("want ErrCycle, got %v", err)
	}
	if el > budget {
		t.Fatalf("cycle rejection took %v, budget %v", el, budget)
	}
	t.Logf("cycle rejection: %v", el)
	if st, _, _ := e2.Requirement("L0_N0"); st != StatusPending {
		t.Fatalf("state must be untouched after rejected edge, got %v", st)
	}
}

// TestScale mirrors the required workload: 100k requirements, 300k
// associations, then 10k random writes which must finish within seconds.
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped in -short mode")
	}
	const (
		nReqs   = 100_000
		nEdges  = 150_000
		nLinks  = 150_000
		nTCs    = 30_000
		nWrites = 10_000
	)
	rng := rand.New(rand.NewSource(7))
	e := New()
	for i := 0; i < nReqs; i++ {
		if err := e.AddRequirement(fmt.Sprintf("r%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// Forward local edges (parent index < child index, window 64), added in
	// increasing parent order so cycle checks stay O(1) during setup.
	type edge struct{ p, c int }
	edges := make([]edge, 0, nEdges)
	seenEdge := map[edge]bool{}
	for len(edges) < nEdges {
		p := rng.Intn(nReqs - 1)
		c := p + 1 + rng.Intn(64)
		if c >= nReqs || seenEdge[edge{p, c}] {
			continue
		}
		seenEdge[edge{p, c}] = true
		edges = append(edges, edge{p, c})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].p < edges[j].p })
	for _, ed := range edges {
		if err := e.AddEdge(fmt.Sprintf("r%d", ed.p), fmt.Sprintf("r%d", ed.c)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < nLinks; i++ {
		r := rng.Intn(nReqs)
		tc := rng.Intn(nTCs)
		if err := e.LinkTestCase(fmt.Sprintf("r%d", r), fmt.Sprintf("t%d", tc)); err == ErrDuplicateLink {
			i--
			continue
		} else if err != nil {
			t.Fatal(err)
		}
	}

	// 10k random writes: exec record/delete, link/unlink, edge add/remove,
	// anomaly create/transition. Edge writes use high-index children so
	// cycle checks stay small; the 40-layer test covers the deep-DAG case.
	start := time.Now()
	execSeq := 0
	var liveExecs []string
	var liveEdges []edge
	anSeq := 0
	for i := 0; i < nWrites; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // record execution
			id := fmt.Sprintf("w%d", execSeq)
			execSeq++
			liveExecs = append(liveExecs, id)
			err := e.RecordExecution(id, fmt.Sprintf("t%d", rng.Intn(nTCs)),
				int64(rng.Intn(1000)), Result(rng.Intn(3)))
			if err != nil {
				t.Fatal(err)
			}
		case 4: // delete execution
			if len(liveExecs) == 0 {
				continue
			}
			k := rng.Intn(len(liveExecs))
			id := liveExecs[k]
			liveExecs = append(liveExecs[:k], liveExecs[k+1:]...)
			if err := e.DeleteExecution(id); err != nil {
				t.Fatal(err)
			}
		case 5, 6: // link
			_ = e.LinkTestCase(fmt.Sprintf("r%d", rng.Intn(nReqs)),
				fmt.Sprintf("t%d", rng.Intn(nTCs)))
		case 7: // unlink
			_ = e.UnlinkTestCase(fmt.Sprintf("r%d", rng.Intn(nReqs)),
				fmt.Sprintf("t%d", rng.Intn(nTCs)))
		case 8: // add/remove edge near the top of the index space
			p := nReqs - 1 - rng.Intn(500)
			c := nReqs - 1 - rng.Intn(500)
			if p == c {
				continue
			}
			if p > c {
				p, c = c, p
			}
			if err := e.AddEdge(fmt.Sprintf("r%d", p), fmt.Sprintf("r%d", c)); err == nil {
				liveEdges = append(liveEdges, edge{p, c})
			}
			if len(liveEdges) > 0 && rng.Intn(2) == 0 {
				k := rng.Intn(len(liveEdges))
				ed := liveEdges[k]
				liveEdges = append(liveEdges[:k], liveEdges[k+1:]...)
				if err := e.RemoveEdge(fmt.Sprintf("r%d", ed.p), fmt.Sprintf("r%d", ed.c)); err != nil {
					t.Fatal(err)
				}
			}
		case 9: // anomaly create + transitions
			id := fmt.Sprintf("an%d", anSeq)
			anSeq++
			sev := 1 + rng.Intn(4)
			reqs := []string{fmt.Sprintf("r%d", rng.Intn(nReqs))}
			tcs := []string{fmt.Sprintf("t%d", rng.Intn(nTCs))}
			if err := e.CreateAnomaly(id, sev, reqs, tcs); err != nil {
				t.Fatal(err)
			}
			_ = e.TransitionAnomaly(id, AnomalyUnderInvestigation)
			_ = e.TransitionAnomaly(id, AnomalyResolved)
			if rng.Intn(2) == 0 {
				_ = e.TransitionAnomaly(id, AnomalyOpen)
			}
		}
	}
	el := time.Since(start)
	t.Logf("10k random writes: %v", el)
	if el > 5*time.Second {
		t.Fatalf("10k writes took %v, want < 5s", el)
	}
}
