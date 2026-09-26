package reqstate

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// buildLayered builds a DAG with the given layers and width, fully
// connecting adjacent layers. Edges are added top-down so construction
// stays cheap; returns ids per layer.
func buildLayered(eng *Engine, layers, width int) [][]string {
	ids := make([][]string, layers)
	for l := 0; l < layers; l++ {
		for w := 0; w < width; w++ {
			id := fmt.Sprintf("L%dN%d", l, w)
			eng.AddRequirement(id)
			ids[l] = append(ids[l], id)
		}
	}
	for l := 0; l+1 < layers; l++ {
		for _, p := range ids[l] {
			for _, c := range ids[l+1] {
				if err := eng.AddEdge(p, c); err != nil {
					panic(err)
				}
			}
		}
	}
	return ids
}

// The 40-layer, 2-per-layer, fully-connected-between-layers DAG: a write
// at a bottom requirement and a cycle-creating edge attempt must both
// complete in milliseconds.
func TestPerfDeepDiamond(t *testing.T) {
	eng := New()
	ids := buildLayered(eng, 40, 2)
	bottom := ids[39][0]
	top := ids[0][0]

	if err := eng.LinkTestCase(bottom, "T"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := eng.RecordExecution("e1", "T", 1, Fail); err != nil {
		t.Fatal(err)
	}
	writeDur := time.Since(start)
	if s, _, _ := eng.Requirement(top); s != Failed {
		t.Fatalf("top should be Failed, got %v", s)
	}

	start = time.Now()
	err := eng.AddEdge(bottom, top) // would create a cycle
	cycleDur := time.Since(start)
	if err != ErrCycle {
		t.Fatalf("want ErrCycle, got %v", err)
	}

	const limit = 50 * time.Millisecond
	if writeDur > limit {
		t.Fatalf("bottom write took %v, want < %v", writeDur, limit)
	}
	if cycleDur > limit {
		t.Fatalf("cycle check took %v, want < %v", cycleDur, limit)
	}
	t.Logf("bottom write: %v, cycle check: %v", writeDur, cycleDur)
}

// Deleting an execution record must not depend linearly on the number of
// records of its test case.
func TestPerfDeleteExecution(t *testing.T) {
	eng := New()
	eng.AddRequirement("R")
	eng.LinkTestCase("R", "T")
	const n = 50000
	for i := 0; i < n; i++ {
		if err := eng.RecordExecution(fmt.Sprintf("e%d", i), "T", int64(i), Pass); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	for i := 0; i < 1000; i++ {
		if err := eng.DeleteExecution(fmt.Sprintf("e%d", i*3+1)); err != nil {
			t.Fatal(err)
		}
	}
	dur := time.Since(start)
	if dur > 500*time.Millisecond {
		t.Fatalf("1000 deletes among %d records took %v", n, dur)
	}
	t.Logf("1000 deletes among %d records: %v", n, dur)
}

// 100k requirements, ~300k associations, 10k random writes in seconds.
func TestPerfScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	rnd := rand.New(rand.NewSource(7))
	eng := New()

	const layers, width = 500, 200 // 100k requirements
	ids := make([][]string, layers)
	for l := 0; l < layers; l++ {
		ids[l] = make([]string, width)
		for w := 0; w < width; w++ {
			id := fmt.Sprintf("r%d", l*width+w)
			eng.AddRequirement(id)
			ids[l][w] = id
		}
	}
	// ~200k DAG edges: 2 random edges per node to the next layer.
	// Added top-down so cycle checks stay trivial during construction.
	for l := 0; l+1 < layers; l++ {
		for w := 0; w < width; w++ {
			for k := 0; k < 2; k++ {
				if err := eng.AddEdge(ids[l][w], ids[l+1][rnd.Intn(width)]); err != nil && err != ErrDuplicateEdge {
					t.Fatal(err)
				}
			}
		}
	}
	// ~100k test-case links.
	const tcCount = 20000
	for i := 0; i < 100000; i++ {
		req := ids[rnd.Intn(layers)][rnd.Intn(width)]
		if err := eng.LinkTestCase(req, fmt.Sprintf("tc%d", rnd.Intn(tcCount))); err != nil {
			continue // duplicate link, fine
		}
	}

	// 10k random writes over the built project.
	var execs []string
	start := time.Now()
	for i := 0; i < 10000; i++ {
		req := ids[rnd.Intn(layers)][rnd.Intn(width)]
		switch rnd.Intn(10) {
		case 0, 1, 2: // record execution
			id := fmt.Sprintf("e%d", i)
			if err := eng.RecordExecution(id, fmt.Sprintf("tc%d", rnd.Intn(tcCount)),
				int64(rnd.Intn(1000)), Result(rnd.Intn(3))); err == nil {
				execs = append(execs, id)
			}
		case 3: // delete execution
			if len(execs) > 0 {
				eng.DeleteExecution(execs[rnd.Intn(len(execs))])
			}
		case 4, 5: // link
			eng.LinkTestCase(req, fmt.Sprintf("tc%d", rnd.Intn(tcCount)))
		case 6: // unlink
			eng.UnlinkTestCase(req, fmt.Sprintf("tc%d", rnd.Intn(tcCount)))
		case 7: // anomaly + transitions
			an := fmt.Sprintf("a%d", i)
			if err := eng.CreateAnomaly(an, 1+rnd.Intn(4),
				[]string{req}, []string{fmt.Sprintf("tc%d", rnd.Intn(tcCount))}); err == nil {
				eng.TransitionAnomaly(an, UnderInvestigation)
				eng.TransitionAnomaly(an, Resolved)
				if rnd.Intn(2) == 0 {
					eng.TransitionAnomaly(an, Open)
				}
			}
		case 8: // edge add/remove near the bottom half
			l := layers/2 + rnd.Intn(layers/2-1)
			p, c := ids[l][rnd.Intn(width)], ids[l+1][rnd.Intn(width)]
			if err := eng.AddEdge(p, c); err != nil {
				eng.RemoveEdge(p, c)
			}
		default: // new requirement
			eng.AddRequirement(fmt.Sprintf("extra%d", i))
		}
	}
	dur := time.Since(start)
	t.Logf("10k random writes over 100k reqs / 300k associations: %v", dur)
	if dur > 10*time.Second {
		t.Fatalf("10k writes took %v, want seconds", dur)
	}
	if s := eng.Summary(); s.Blocked+s.Failed+s.Verified+s.Pending == 0 {
		t.Fatal("summary should be non-empty")
	}
}
