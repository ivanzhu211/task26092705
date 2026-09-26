package reqv

// reference_test.go holds an independent, obviously-simple model that
// recomputes every derived value from scratch. The differential test
// compares the incremental Engine against this model after every op.

type refReq struct {
	parents  map[string]bool
	children map[string]bool
	tcs      map[string]bool
}

type refTC struct {
	reqs map[string]bool
}

type refAnomaly struct {
	severity int
	state    AnomalyState
	reqs     map[string]bool
	tcs      map[string]bool
}

type refExec struct {
	tcID    string
	at, seq int64
	result  Result
}

type refModel struct {
	reqs      map[string]*refReq
	tcs       map[string]*refTC
	anomalies map[string]*refAnomaly
	execs     map[string]refExec
	seq       int64
}

func newRefModel() *refModel {
	return &refModel{
		reqs:      map[string]*refReq{},
		tcs:       map[string]*refTC{},
		anomalies: map[string]*refAnomaly{},
		execs:     map[string]refExec{},
	}
}

func (m *refModel) getTC(id string) *refTC {
	tc := m.tcs[id]
	if tc == nil {
		tc = &refTC{reqs: map[string]bool{}}
		m.tcs[id] = tc
	}
	return tc
}

func (m *refModel) addReq(id string) error {
	if _, ok := m.reqs[id]; ok {
		return ErrDuplicateReq
	}
	m.reqs[id] = &refReq{parents: map[string]bool{}, children: map[string]bool{}, tcs: map[string]bool{}}
	return nil
}

func (m *refModel) reaches(src, target string) bool {
	seen := map[string]bool{src: true}
	stack := []string{src}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for next := range m.reqs[id].children {
			if next == target {
				return true
			}
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

func (m *refModel) addEdge(parent, child string) error {
	p, ok := m.reqs[parent]
	if !ok {
		return ErrUnknownReq
	}
	c, ok := m.reqs[child]
	if !ok {
		return ErrUnknownReq
	}
	if p.children[child] {
		return ErrDuplicateEdge
	}
	if parent == child || m.reaches(child, parent) {
		return ErrCycle
	}
	p.children[child] = true
	c.parents[parent] = true
	return nil
}

func (m *refModel) removeEdge(parent, child string) error {
	p, ok := m.reqs[parent]
	if !ok {
		return ErrUnknownReq
	}
	c, ok := m.reqs[child]
	if !ok {
		return ErrUnknownReq
	}
	if !p.children[child] {
		return ErrEdgeNotFound
	}
	delete(p.children, child)
	delete(c.parents, parent)
	return nil
}

func (m *refModel) link(reqID, tcID string) error {
	r, ok := m.reqs[reqID]
	if !ok {
		return ErrUnknownReq
	}
	if r.tcs[tcID] {
		return ErrDuplicateLink
	}
	r.tcs[tcID] = true
	m.getTC(tcID).reqs[reqID] = true
	return nil
}

func (m *refModel) unlink(reqID, tcID string) error {
	r, ok := m.reqs[reqID]
	if !ok {
		return ErrUnknownReq
	}
	if !r.tcs[tcID] {
		return ErrLinkNotFound
	}
	delete(r.tcs, tcID)
	delete(m.tcs[tcID].reqs, reqID)
	return nil
}

func (m *refModel) record(execID, tcID string, at int64, result Result) error {
	switch result {
	case ResultPass, ResultFail, ResultIncomplete:
	default:
		return ErrInvalidResult
	}
	if _, ok := m.execs[execID]; ok {
		return ErrDuplicateExec
	}
	m.seq++
	m.execs[execID] = refExec{tcID: tcID, at: at, seq: m.seq, result: result}
	m.getTC(tcID)
	return nil
}

func (m *refModel) deleteExec(execID string) error {
	if _, ok := m.execs[execID]; !ok {
		return ErrUnknownExec
	}
	delete(m.execs, execID)
	return nil
}

func (m *refModel) createAnomaly(id string, severity int, reqIDs, tcIDs []string) error {
	if _, ok := m.anomalies[id]; ok {
		return ErrDuplicateAnomaly
	}
	if severity < 1 || severity > 4 {
		return ErrInvalidSeverity
	}
	for _, r := range reqIDs {
		if _, ok := m.reqs[r]; !ok {
			return ErrUnknownReq
		}
	}
	a := &refAnomaly{severity: severity, state: AnomalyOpen, reqs: map[string]bool{}, tcs: map[string]bool{}}
	for _, r := range reqIDs {
		a.reqs[r] = true
	}
	for _, t := range tcIDs {
		a.tcs[t] = true
		m.getTC(t)
	}
	m.anomalies[id] = a
	return nil
}

func (m *refModel) transition(id string, to AnomalyState) error {
	a, ok := m.anomalies[id]
	if !ok {
		return ErrUnknownAnomaly
	}
	if !validTransition(a.state, to) {
		return ErrInvalidTransition
	}
	a.state = to
	return nil
}

// tcCurrent recomputes a test case's current result from scratch.
func (m *refModel) tcCurrent(tcID string) (Result, bool) {
	var best refExec
	ok := false
	for _, ex := range m.execs {
		if ex.tcID != tcID {
			continue
		}
		if !ok || ex.at > best.at || (ex.at == best.at && ex.seq > best.seq) {
			best, ok = ex, true
		}
	}
	if !ok {
		return 0, false
	}
	return best.result, true
}

// recompute derives every requirement's status and gap from scratch.
func (m *refModel) recompute() (map[string]Status, map[string]bool) {
	// Current result of every test case, computed in one pass over execs.
	tcCur := map[string]Result{}
	tcOK := map[string]bool{}
	var bestAt, bestSeq = map[string]int64{}, map[string]int64{}
	for _, ex := range m.execs {
		if !tcOK[ex.tcID] || ex.at > bestAt[ex.tcID] ||
			(ex.at == bestAt[ex.tcID] && ex.seq > bestSeq[ex.tcID]) {
			tcOK[ex.tcID] = true
			bestAt[ex.tcID], bestSeq[ex.tcID] = ex.at, ex.seq
			tcCur[ex.tcID] = ex.result
		}
	}
	status := map[string]Status{}
	gap := map[string]bool{}
	visiting := map[string]bool{} // model is a DAG; guard anyway
	var solve func(id string) (Status, bool)
	solve = func(id string) (Status, bool) {
		if st, ok := status[id]; ok {
			return st, gap[id]
		}
		if visiting[id] {
			panic("cycle in reference model")
		}
		visiting[id] = true
		r := m.reqs[id]
		blocked, failed := false, false
		verified := len(r.tcs)+len(r.children) > 0
		g := len(r.tcs) == 0
		for _, a := range m.anomalies {
			if a.severity != 1 || !anomalyActive(a.state) {
				continue
			}
			if a.reqs[id] {
				blocked = true
			}
			for tcID := range r.tcs {
				if a.tcs[tcID] {
					blocked = true
				}
			}
		}
		for tcID := range r.tcs {
			cur, ok := tcCur[tcID], tcOK[tcID]
			if !ok || cur != ResultPass {
				verified = false
			}
			if ok && cur == ResultFail {
				failed = true
			}
		}
		for childID := range r.children {
			cst, cgap := solve(childID)
			if cst == StatusBlocked {
				blocked = true
			}
			if cst == StatusFailed {
				failed = true
			}
			if cst != StatusVerified {
				verified = false
			}
			if !cgap {
				g = false
			}
		}
		var st Status
		switch {
		case blocked:
			st = StatusBlocked
		case failed:
			st = StatusFailed
		case verified:
			st = StatusVerified
		default:
			st = StatusPending
		}
		status[id] = st
		gap[id] = g
		delete(visiting, id)
		return st, g
	}
	for id := range m.reqs {
		solve(id)
	}
	return status, gap
}

func (m *refModel) summary() Summary {
	status, gap := m.recompute()
	var s Summary
	for id := range m.reqs {
		s.addStatus(status[id], 1)
		if gap[id] {
			s.Gaps++
		}
	}
	return s
}
