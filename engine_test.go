package reqv

import (
	"fmt"
	"math/rand"
	"testing"
)

type op struct {
	desc   string
	engine func(e *Engine) error
	model  func(m *refModel) error
}

// dual returns an op applying the same logical operation to both implementations.
func dual(desc string,
	ef func(e *Engine) error,
	mf func(m *refModel) error) op {
	return op{desc: desc, engine: ef, model: mf}
}

type coverage struct {
	diamond           bool
	dualPathAnomaly   bool
	deleteLatestExec  bool
	execBeforeLink    bool
	anomalyBeforeLink bool
	reopen            bool
}

func TestDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260927))
	ops, cov := genOps(rng, 4000)

	e := New()
	m := newRefModel()
	for i, o := range ops {
		errE := o.engine(e)
		errM := o.model(m)
		if (errE == nil) != (errM == nil) || (errE != nil && errE != errM) {
			t.Fatalf("step %d (%s): engine err=%v, model err=%v", i, o.desc, errE, errM)
		}
		status, gap := m.recompute()
		for id := range m.reqs {
			st, g, err := e.Requirement(id)
			if err != nil {
				t.Fatalf("step %d (%s): engine.Requirement(%s): %v", i, o.desc, id, err)
			}
			if st != status[id] || g != gap[id] {
				t.Fatalf("step %d (%s): req %s: engine=(%v,%v) model=(%v,%v)",
					i, o.desc, id, st, g, status[id], gap[id])
			}
		}
		if got, want := e.Summary(), m.summary(); got != want {
			t.Fatalf("step %d (%s): summary engine=%+v model=%+v", i, o.desc, got, want)
		}
	}
	if _, _, err := e.Requirement("no-such-req"); err != ErrUnknownReq {
		t.Fatalf("unknown req: got %v", err)
	}
	if !cov.diamond || !cov.dualPathAnomaly || !cov.deleteLatestExec ||
		!cov.execBeforeLink || !cov.anomalyBeforeLink || !cov.reopen {
		t.Fatalf("generator did not cover required scenarios: %+v", cov)
	}
}

func genOps(rng *rand.Rand, nRandom int) ([]op, *coverage) {
	cov := &coverage{}
	var ops []op

	reqSeq, tcSeq, execSeq, anSeq := 0, 0, 0, 0
	newReq := func() string { s := fmt.Sprintf("r%d", reqSeq); reqSeq++; return s }
	newTC := func() string { s := fmt.Sprintf("t%d", tcSeq); tcSeq++; return s }
	newExec := func() string { s := fmt.Sprintf("e%d", execSeq); execSeq++; return s }
	newAn := func() string { s := fmt.Sprintf("a%d", anSeq); anSeq++; return s }

	var reqIDs, tcIDs, execIDs, anIDs []string
	type edge struct{ p, c string }
	var edges []edge

	addReqOp := func(id string) {
		ops = append(ops, dual("addReq "+id,
			func(e *Engine) error { return e.AddRequirement(id) },
			func(m *refModel) error { return m.addReq(id) }))
	}
	addEdgeOp := func(p, c string) {
		ops = append(ops, dual(fmt.Sprintf("addEdge %s>%s", p, c),
			func(e *Engine) error { return e.AddEdge(p, c) },
			func(m *refModel) error { return m.addEdge(p, c) }))
	}
	removeEdgeOp := func(p, c string) {
		ops = append(ops, dual(fmt.Sprintf("removeEdge %s>%s", p, c),
			func(e *Engine) error { return e.RemoveEdge(p, c) },
			func(m *refModel) error { return m.removeEdge(p, c) }))
	}
	linkOp := func(r, t string) {
		ops = append(ops, dual(fmt.Sprintf("link %s~%s", r, t),
			func(e *Engine) error { return e.LinkTestCase(r, t) },
			func(m *refModel) error { return m.link(r, t) }))
	}
	unlinkOp := func(r, t string) {
		ops = append(ops, dual(fmt.Sprintf("unlink %s~%s", r, t),
			func(e *Engine) error { return e.UnlinkTestCase(r, t) },
			func(m *refModel) error { return m.unlink(r, t) }))
	}
	recordOp := func(id, tc string, at int64, res Result) {
		ops = append(ops, dual(fmt.Sprintf("record %s", id),
			func(e *Engine) error { return e.RecordExecution(id, tc, at, res) },
			func(m *refModel) error { return m.record(id, tc, at, res) }))
	}
	deleteExecOp := func(id string) {
		ops = append(ops, dual("deleteExec "+id,
			func(e *Engine) error { return e.DeleteExecution(id) },
			func(m *refModel) error { return m.deleteExec(id) }))
	}
	anomalyOp := func(id string, sev int, reqs, tcs []string) {
		ops = append(ops, dual("createAnomaly "+id,
			func(e *Engine) error { return e.CreateAnomaly(id, sev, reqs, tcs) },
			func(m *refModel) error { return m.createAnomaly(id, sev, reqs, tcs) }))
	}
	transitionOp := func(id string, to AnomalyState) {
		ops = append(ops, dual(fmt.Sprintf("transition %s->%d", id, to),
			func(e *Engine) error { return e.TransitionAnomaly(id, to) },
			func(m *refModel) error { return m.transition(id, to) }))
	}

	// --- Scripted scenarios guaranteeing the required situations. ---

	// Diamond sharing: d0 -> {d1, d2} -> d3.
	for _, id := range []string{"d0", "d1", "d2", "d3"} {
		addReqOp(id)
	}
	addEdgeOp("d0", "d1")
	addEdgeOp("d0", "d2")
	addEdgeOp("d1", "d3")
	addEdgeOp("d2", "d3")
	cov.diamond = true

	// One severity-1 anomaly hits the same requirement via a direct link and
	// via a test case; later only the test-case path is removed. The anomaly
	// is created before the test case is linked (anomaly-before-link).
	addReqOp("b0")
	anomalyOp("ab0", 1, []string{"b0"}, []string{"tb0"})
	anIDs = append(anIDs, "ab0")
	linkOp("b0", "tb0")
	unlinkOp("b0", "tb0") // withdraw only the test-case path
	cov.dualPathAnomaly = true
	cov.anomalyBeforeLink = true

	// Delete the test case's newest execution record (with an at-tie so the
	// later-recorded record wins and is then deleted).
	recordOp("ec0", "tc0", 10, ResultPass)
	recordOp("ec1", "tc0", 20, ResultFail)
	recordOp("ec2", "tc0", 20, ResultPass)
	deleteExecOp("ec2")
	execIDs = append(execIDs, "ec0", "ec1", "ec2")
	cov.deleteLatestExec = true

	// Record an execution before the test case is ever linked.
	recordOp("ex0", "tx0", 5, ResultFail)
	addReqOp("dx0")
	linkOp("dx0", "tx0")
	execIDs = append(execIDs, "ex0")
	cov.execBeforeLink = true

	// Reopen after Resolved.
	addReqOp("ar0")
	anomalyOp("ae0", 1, []string{"ar0"}, nil)
	anIDs = append(anIDs, "ae0")
	transitionOp("ae0", AnomalyUnderInvestigation)
	transitionOp("ae0", AnomalyResolved)
	transitionOp("ae0", AnomalyOpen)
	cov.reopen = true

	// --- Random stream, deliberately including illegal ops. ---

	pick := func(s []string) string { return s[rng.Intn(len(s))] }
	results := []Result{ResultPass, ResultFail, ResultIncomplete}

	for i := 0; i < nRandom; i++ {
		switch rng.Intn(14) {
		case 0: // add requirement (sometimes duplicate)
			id := newReq()
			if len(reqIDs) > 0 && rng.Intn(4) == 0 {
				id = pick(reqIDs)
			} else {
				reqIDs = append(reqIDs, id)
			}
			addReqOp(id)
		case 1, 2: // add edge (sometimes reversed -> cycle attempt, sometimes dup)
			if len(reqIDs) < 2 {
				id := newReq()
				reqIDs = append(reqIDs, id)
				addReqOp(id)
				continue
			}
			p, c := pick(reqIDs), pick(reqIDs)
			if len(edges) > 0 && rng.Intn(3) == 0 {
				e := edges[rng.Intn(len(edges))]
				p, c = e.c, e.p // reversed: likely a cycle
			}
			edges = append(edges, edge{p, c})
			addEdgeOp(p, c)
		case 3: // remove edge (hit or miss)
			if len(edges) == 0 {
				continue
			}
			e := edges[rng.Intn(len(edges))]
			removeEdgeOp(e.p, e.c)
		case 4, 5: // link (sometimes duplicate)
			if len(reqIDs) == 0 {
				continue
			}
			t := newTC()
			if len(tcIDs) > 0 && rng.Intn(2) == 0 {
				t = pick(tcIDs)
			} else {
				tcIDs = append(tcIDs, t)
			}
			linkOp(pick(reqIDs), t)
		case 6: // unlink (hit or miss)
			if len(reqIDs) == 0 || len(tcIDs) == 0 {
				continue
			}
			unlinkOp(pick(reqIDs), pick(tcIDs))
		case 7, 8, 9: // record execution (sometimes duplicate id, sometimes bad result)
			id := newExec()
			if len(execIDs) > 0 && rng.Intn(6) == 0 {
				id = pick(execIDs)
			} else {
				execIDs = append(execIDs, id)
			}
			t := newTC()
			if len(tcIDs) > 0 && rng.Intn(2) == 0 {
				t = pick(tcIDs)
			} else {
				tcIDs = append(tcIDs, t)
			}
			res := results[rng.Intn(3)]
			if rng.Intn(20) == 0 {
				res = Result(99) // invalid
			}
			recordOp(id, t, int64(rng.Intn(30)), res)
		case 10: // delete execution (hit or miss)
			if len(execIDs) == 0 {
				continue
			}
			deleteExecOp(pick(execIDs))
		case 11: // create anomaly (sometimes bad severity / unknown req / dup id)
			id := newAn()
			if len(anIDs) > 0 && rng.Intn(5) == 0 {
				id = pick(anIDs)
			} else {
				anIDs = append(anIDs, id)
			}
			sev := 1 + rng.Intn(4)
			if rng.Intn(15) == 0 {
				sev = rng.Intn(7) // may be 0, 5, 6
			}
			var reqs, tcs []string
			if len(reqIDs) > 0 {
				for k := 0; k < rng.Intn(3); k++ {
					reqs = append(reqs, pick(reqIDs))
				}
				if rng.Intn(15) == 0 {
					reqs = append(reqs, "unknown-req")
				}
			}
			for k := 0; k < rng.Intn(3); k++ {
				t := newTC()
				if len(tcIDs) > 0 && rng.Intn(2) == 0 {
					t = pick(tcIDs)
				} else {
					tcIDs = append(tcIDs, t)
				}
				tcs = append(tcs, t)
			}
			anomalyOp(id, sev, reqs, tcs)
		case 12, 13: // transition (random target: mostly illegal, sometimes legal)
			if len(anIDs) == 0 {
				continue
			}
			transitionOp(pick(anIDs), AnomalyState(rng.Intn(4)))
		}
	}
	return ops, cov
}
