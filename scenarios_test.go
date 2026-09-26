package reqstate

import "testing"

// must is a small helper: apply op to engine; on success mirror to ref.
func check(t *testing.T, eng *Engine, ref *reference) {
	t.Helper()
	checkAll(t, eng, ref)
}

// Diamond sharing: D derived from B and C, both derived from A.
func TestDiamondSharing(t *testing.T) {
	eng, ref := New(), newReference()
	for _, id := range []string{"A", "B", "C", "D"} {
		if err := eng.AddRequirement(id); err != nil {
			t.Fatal(err)
		}
		ref.reqs[id] = true
	}
	for _, e := range [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}} {
		if err := eng.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
		ref.edges[e] = true
	}
	check(t, eng, ref)

	// Fail D's test case: B, C, A must all become Failed through the diamond.
	if err := eng.LinkTestCase("D", "T"); err != nil {
		t.Fatal(err)
	}
	ref.links[[2]string{"D", "T"}] = true
	if err := eng.RecordExecution("e1", "T", 1, Fail); err != nil {
		t.Fatal(err)
	}
	ref.seq++
	ref.execs["e1"] = refExec{tc: "T", at: 1, seq: ref.seq, result: Fail}
	check(t, eng, ref)
	for _, id := range []string{"A", "B", "C", "D"} {
		if s, _, _ := eng.Requirement(id); s != Failed {
			t.Fatalf("%s should be Failed, got %v", id, s)
		}
	}

	// Delete the failing record: everything recovers.
	if err := eng.DeleteExecution("e1"); err != nil {
		t.Fatal(err)
	}
	delete(ref.execs, "e1")
	check(t, eng, ref)
}

// One severity-1 anomaly hits the same requirement via direct link and via
// a linked test case; removing only the test-case path keeps it Blocked.
func TestDualPathAnomaly(t *testing.T) {
	eng, ref := New(), newReference()
	if err := eng.AddRequirement("R"); err != nil {
		t.Fatal(err)
	}
	ref.reqs["R"] = true
	if err := eng.LinkTestCase("R", "T"); err != nil {
		t.Fatal(err)
	}
	ref.links[[2]string{"R", "T"}] = true
	if err := eng.CreateAnomaly("AN", 1, []string{"R"}, []string{"T"}); err != nil {
		t.Fatal(err)
	}
	ref.anomalies["AN"] = refAnomaly{severity: 1, state: Open, reqs: []string{"R"}, tcs: []string{"T"}}
	check(t, eng, ref)
	if s, _, _ := eng.Requirement("R"); s != Blocked {
		t.Fatalf("R should be Blocked, got %v", s)
	}

	// Remove the test-case path only: direct link still blocks.
	if err := eng.UnlinkTestCase("R", "T"); err != nil {
		t.Fatal(err)
	}
	delete(ref.links, [2]string{"R", "T"})
	check(t, eng, ref)
	if s, _, _ := eng.Requirement("R"); s != Blocked {
		t.Fatalf("R should still be Blocked, got %v", s)
	}

	// Resolve then reopen: Open -> UI -> Resolved -> Open.
	for _, to := range []AnomalyState{UnderInvestigation, Resolved, Open} {
		if err := eng.TransitionAnomaly("AN", to); err != nil {
			t.Fatal(err)
		}
		an := ref.anomalies["AN"]
		an.state = to
		ref.anomalies["AN"] = an
		check(t, eng, ref)
	}
	if s, _, _ := eng.Requirement("R"); s != Blocked {
		t.Fatalf("reopened anomaly should block R, got %v", s)
	}
}

// Deleting the newest execution record must expose the previous one.
func TestDeleteNewestExecution(t *testing.T) {
	eng, ref := New(), newReference()
	if err := eng.AddRequirement("R"); err != nil {
		t.Fatal(err)
	}
	ref.reqs["R"] = true
	if err := eng.LinkTestCase("R", "T"); err != nil {
		t.Fatal(err)
	}
	ref.links[[2]string{"R", "T"}] = true
	recs := []struct {
		id  string
		at  int64
		res Result
	}{{"e1", 10, Fail}, {"e2", 20, Pass}, {"e3", 20, Incomplete}}
	for _, rc := range recs {
		if err := eng.RecordExecution(rc.id, "T", rc.at, rc.res); err != nil {
			t.Fatal(err)
		}
		ref.seq++
		ref.execs[rc.id] = refExec{tc: "T", at: rc.at, seq: ref.seq, result: rc.res}
	}
	check(t, eng, ref)
	// e3 is newest (tie at 20, recorded later) and Incomplete -> Pending.
	if s, _, _ := eng.Requirement("R"); s != Pending {
		t.Fatalf("want Pending, got %v", s)
	}
	if err := eng.DeleteExecution("e3"); err != nil {
		t.Fatal(err)
	}
	delete(ref.execs, "e3")
	check(t, eng, ref)
	if s, _, _ := eng.Requirement("R"); s != Verified {
		t.Fatalf("want Verified after deleting newest, got %v", s)
	}
	if err := eng.DeleteExecution("e2"); err != nil {
		t.Fatal(err)
	}
	delete(ref.execs, "e2")
	check(t, eng, ref)
	if s, _, _ := eng.Requirement("R"); s != Failed {
		t.Fatalf("want Failed, got %v", s)
	}
}

// Executions recorded and anomalies created before the test case is linked
// must take effect at link time.
func TestLateLinking(t *testing.T) {
	eng, ref := New(), newReference()
	// Record executions before R exists / before any link.
	if err := eng.RecordExecution("e1", "T1", 5, Pass); err != nil {
		t.Fatal(err)
	}
	ref.seq++
	ref.execs["e1"] = refExec{tc: "T1", at: 5, seq: ref.seq, result: Pass}
	// Create anomaly referencing a test case before any link.
	if err := eng.CreateAnomaly("AN", 1, nil, []string{"T2"}); err != nil {
		t.Fatal(err)
	}
	ref.anomalies["AN"] = refAnomaly{severity: 1, state: Open, tcs: []string{"T2"}}

	if err := eng.AddRequirement("R"); err != nil {
		t.Fatal(err)
	}
	ref.reqs["R"] = true
	if err := eng.LinkTestCase("R", "T1"); err != nil {
		t.Fatal(err)
	}
	ref.links[[2]string{"R", "T1"}] = true
	check(t, eng, ref)
	if s, _, _ := eng.Requirement("R"); s != Verified {
		t.Fatalf("want Verified, got %v", s)
	}
	if err := eng.LinkTestCase("R", "T2"); err != nil {
		t.Fatal(err)
	}
	ref.links[[2]string{"R", "T2"}] = true
	check(t, eng, ref)
	if s, _, _ := eng.Requirement("R"); s != Blocked {
		t.Fatalf("want Blocked, got %v", s)
	}
}

// Illegal operations must fail and leave no observable change.
func TestIllegalOpsAtomic(t *testing.T) {
	eng, ref := New(), newReference()
	mk := func() {
		if err := eng.AddRequirement("R1"); err != nil {
			t.Fatal(err)
		}
		ref.reqs["R1"] = true
		if err := eng.AddRequirement("R2"); err != nil {
			t.Fatal(err)
		}
		ref.reqs["R2"] = true
		if err := eng.AddEdge("R1", "R2"); err != nil {
			t.Fatal(err)
		}
		ref.edges[[2]string{"R1", "R2"}] = true
		if err := eng.LinkTestCase("R2", "T"); err != nil {
			t.Fatal(err)
		}
		ref.links[[2]string{"R2", "T"}] = true
		if err := eng.RecordExecution("e1", "T", 1, Pass); err != nil {
			t.Fatal(err)
		}
		ref.seq++
		ref.execs["e1"] = refExec{tc: "T", at: 1, seq: ref.seq, result: Pass}
		if err := eng.CreateAnomaly("AN", 2, []string{"R1"}, nil); err != nil {
			t.Fatal(err)
		}
		ref.anomalies["AN"] = refAnomaly{severity: 2, state: Open, reqs: []string{"R1"}}
	}
	mk()
	bad := []error{
		eng.AddRequirement("R1"),                           // duplicate
		eng.AddRequirement("R1"),                           // duplicate again
		eng.AddEdge("R1", "ghost"),                         // unknown
		eng.AddEdge("R1", "R2"),                            // duplicate edge
		eng.AddEdge("R2", "R1"),                            // cycle
		eng.AddEdge("R1", "R1"),                            // self cycle
		eng.RemoveEdge("R2", "R1"),                         // no such edge
		eng.RemoveEdge("ghost", "R1"),                      // unknown req
		eng.LinkTestCase("R2", "T"),                        // duplicate link
		eng.LinkTestCase("ghost", "T"),                     // unknown req
		eng.UnlinkTestCase("R1", "T"),                      // no such link
		eng.RecordExecution("e1", "T", 2, Fail),            // duplicate exec id
		eng.DeleteExecution("ghost"),                       // unknown exec
		eng.CreateAnomaly("AN", 1, nil, nil),               // duplicate anomaly
		eng.CreateAnomaly("A2", 0, nil, nil),               // bad severity
		eng.CreateAnomaly("A3", 5, nil, nil),               // bad severity
		eng.CreateAnomaly("A4", 1, []string{"ghost"}, nil), // unknown req
		eng.TransitionAnomaly("ghost", Open),               // unknown anomaly
		eng.TransitionAnomaly("AN", Resolved),              // Open -> Resolved illegal
		eng.TransitionAnomaly("AN", Closed),                // Open -> Closed illegal
	}
	for i, err := range bad {
		if err == nil {
			t.Fatalf("op %d should have failed", i)
		}
	}
	check(t, eng, ref)
}
