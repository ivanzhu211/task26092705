package reqstate

import (
	"errors"
	"math/rand"
)

var (
	ErrUnknownRequirement   = errors.New("unknown requirement")
	ErrDuplicateRequirement = errors.New("duplicate requirement")
	ErrUnknownEdge          = errors.New("no such parent-derived edge")
	ErrDuplicateEdge        = errors.New("edge already exists")
	ErrCycle                = errors.New("edge would create a cycle")
	ErrDuplicateLink        = errors.New("requirement already linked to test case")
	ErrUnknownLink          = errors.New("requirement not linked to test case")
	ErrDuplicateExecution   = errors.New("duplicate execution id")
	ErrUnknownExecution     = errors.New("unknown execution id")
	ErrDuplicateAnomaly     = errors.New("duplicate anomaly id")
	ErrUnknownAnomaly       = errors.New("unknown anomaly id")
	ErrInvalidSeverity      = errors.New("severity must be in 1..4")
	ErrInvalidTransition    = errors.New("illegal anomaly state transition")
)

// requirement is the per-requirement bookkeeping. All derived facts are
// maintained incrementally; nothing is recomputed by traversal at query
// time. Parent/child adjacency uses pointer slices: degrees are small and
// iteration dominates the hot propagation loop.
type requirement struct {
	id       string
	parents  []*requirement
	children []*requirement
	tcs      map[string]struct{}

	// Aggregation over children, kept in sync on every child change.
	childState [4]int // children per State
	childGap   int    // children flagged as coverage gap

	// Local conditions.
	blockers map[string]int // active severity-1 anomaly id -> hit channels (direct + via linked TCs)
	failTCs  int            // linked test cases whose current result is Fail
	passTCs  int            // linked test cases whose current result is Pass

	state State
	gap   bool
	mark  uint32 // scratch mark for traversals
}

func newRequirement(id string) *requirement {
	return &requirement{
		id:       id,
		tcs:      map[string]struct{}{},
		blockers: map[string]int{},
		state:    Pending,
		gap:      true,
	}
}

// compute derives the state and gap flag purely from maintained counters.
func (r *requirement) compute() (State, bool) {
	var s State
	switch {
	case len(r.blockers) > 0 || r.childState[Blocked] > 0:
		s = Blocked
	case r.failTCs > 0 || r.childState[Failed] > 0:
		s = Failed
	case len(r.tcs)+len(r.children) > 0 &&
		r.passTCs == len(r.tcs) &&
		r.childState[Verified] == len(r.children):
		s = Verified
	default:
		s = Pending
	}
	gap := len(r.tcs) == 0 && r.childGap == len(r.children)
	return s, gap
}

func hasReq(list []*requirement, r *requirement) bool {
	for _, x := range list {
		if x == r {
			return true
		}
	}
	return false
}

// removeReq deletes r from list (order not preserved); ok=false if absent.
func removeReq(list []*requirement, r *requirement) ([]*requirement, bool) {
	for i, x := range list {
		if x == r {
			list[i] = list[len(list)-1]
			return list[:len(list)-1], true
		}
	}
	return list, false
}

// testCase exists as soon as its ID is referenced.
type testCase struct {
	id        string
	reqs      []*requirement // requirements linked to this test case
	anomalies map[string]struct{}
	heap      *execHeap
	cur       Result
	hasCur    bool
}

type execRef struct {
	tc  *testCase
	key execKey
}

type anomaly struct {
	id       string
	severity int
	state    AnomalyState
	reqs     []string // directly linked requirements, fixed at creation
	tcs      []string // linked test cases, fixed at creation
}

func (a *anomaly) active() bool {
	return a.state == Open || a.state == UnderInvestigation
}

// Engine maintains derived verification states for one project, in memory.
// It is not safe for concurrent use; callers must serialize calls.
type Engine struct {
	reqs      map[string]*requirement
	tcs       map[string]*testCase
	execs     map[string]execRef
	anomalies map[string]*anomaly
	rnd       *rand.Rand
	seq       uint64
	gen       uint32 // traversal generation counter
	summary   Summary
}

func New() *Engine {
	return &Engine{
		reqs:      map[string]*requirement{},
		tcs:       map[string]*testCase{},
		execs:     map[string]execRef{},
		anomalies: map[string]*anomaly{},
		rnd:       rand.New(rand.NewSource(0x5eed)),
	}
}

func (s *Summary) add(st State, delta int) {
	switch st {
	case Blocked:
		s.Blocked += delta
	case Failed:
		s.Failed += delta
	case Verified:
		s.Verified += delta
	case Pending:
		s.Pending += delta
	}
}

// refresh recomputes r from its counters and, on change, propagates the
// delta to ancestors. Iterative to stay safe on deep DAGs.
func (e *Engine) refresh(r *requirement) {
	stack := []*requirement{r}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ns, ng := cur.compute()
		os, og := cur.state, cur.gap
		if ns == os && ng == og {
			continue
		}
		e.summary.add(os, -1)
		e.summary.add(ns, +1)
		if og {
			e.summary.Gaps--
		}
		if ng {
			e.summary.Gaps++
		}
		cur.state, cur.gap = ns, ng
		for _, p := range cur.parents {
			p.childState[os]--
			p.childState[ns]++
			if og {
				p.childGap--
			}
			if ng {
				p.childGap++
			}
			stack = append(stack, p)
		}
	}
}

func (e *Engine) getTC(id string) *testCase {
	tc, ok := e.tcs[id]
	if !ok {
		tc = &testCase{
			id:        id,
			anomalies: map[string]struct{}{},
			heap:      newExecHeap(e.rnd),
		}
		e.tcs[id] = tc
	}
	return tc
}

func (e *Engine) AddRequirement(id string) error {
	if _, ok := e.reqs[id]; ok {
		return ErrDuplicateRequirement
	}
	r := newRequirement(id)
	e.reqs[id] = r
	e.summary.Pending++
	e.summary.Gaps++
	return nil
}

// reachable reports whether target is reachable from start following
// parent->derived edges (start itself counts). Uses a generation mark to
// avoid allocating a visited set per call.
func (e *Engine) reachable(start, target *requirement) bool {
	if start == target {
		return true
	}
	e.gen++
	gen := e.gen
	start.mark = gen
	stack := []*requirement{start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range n.children {
			if c == target {
				return true
			}
			if c.mark != gen {
				c.mark = gen
				stack = append(stack, c)
			}
		}
	}
	return false
}

func (e *Engine) AddEdge(parentID, derivedID string) error {
	p, ok := e.reqs[parentID]
	if !ok {
		return ErrUnknownRequirement
	}
	c, ok := e.reqs[derivedID]
	if !ok {
		return ErrUnknownRequirement
	}
	if hasReq(p.children, c) {
		return ErrDuplicateEdge
	}
	if e.reachable(c, p) {
		return ErrCycle
	}
	p.children = append(p.children, c)
	c.parents = append(c.parents, p)
	p.childState[c.state]++
	if c.gap {
		p.childGap++
	}
	e.refresh(p)
	return nil
}

func (e *Engine) RemoveEdge(parentID, derivedID string) error {
	p, ok := e.reqs[parentID]
	if !ok {
		return ErrUnknownRequirement
	}
	c, ok := e.reqs[derivedID]
	if !ok {
		return ErrUnknownRequirement
	}
	var found bool
	p.children, found = removeReq(p.children, c)
	if !found {
		return ErrUnknownEdge
	}
	c.parents, _ = removeReq(c.parents, p)
	p.childState[c.state]--
	if c.gap {
		p.childGap--
	}
	e.refresh(p)
	return nil
}

// tcDelta returns the (passTCs, failTCs) contribution of one linked test
// case given its current result.
func tcDelta(tc *testCase) (pass, fail int) {
	if !tc.hasCur {
		return 0, 0
	}
	switch tc.cur {
	case Pass:
		return 1, 0
	case Fail:
		return 0, 1
	}
	return 0, 0
}

func (e *Engine) LinkTestCase(reqID, testCaseID string) error {
	r, ok := e.reqs[reqID]
	if !ok {
		return ErrUnknownRequirement
	}
	if _, ok := r.tcs[testCaseID]; ok {
		return ErrDuplicateLink
	}
	tc := e.getTC(testCaseID)
	r.tcs[testCaseID] = struct{}{}
	tc.reqs = append(tc.reqs, r)
	p, f := tcDelta(tc)
	r.passTCs += p
	r.failTCs += f
	for anID := range tc.anomalies {
		an := e.anomalies[anID]
		if an.severity == 1 && an.active() {
			r.blockers[anID]++
		}
	}
	e.refresh(r)
	return nil
}

func (e *Engine) UnlinkTestCase(reqID, testCaseID string) error {
	r, ok := e.reqs[reqID]
	if !ok {
		return ErrUnknownRequirement
	}
	if _, ok := r.tcs[testCaseID]; !ok {
		return ErrUnknownLink
	}
	tc := e.tcs[testCaseID]
	delete(r.tcs, testCaseID)
	tc.reqs, _ = removeReq(tc.reqs, r)
	p, f := tcDelta(tc)
	r.passTCs -= p
	r.failTCs -= f
	for anID := range tc.anomalies {
		an := e.anomalies[anID]
		if an.severity == 1 && an.active() {
			r.blockers[anID]--
			if r.blockers[anID] == 0 {
				delete(r.blockers, anID)
			}
		}
	}
	e.refresh(r)
	return nil
}

// applyTCResult propagates a test case's current-result change to all
// linked requirements.
func (e *Engine) applyTCResult(tc *testCase, oldPass, oldFail int) {
	newPass, newFail := tcDelta(tc)
	if newPass == oldPass && newFail == oldFail {
		return
	}
	for _, r := range tc.reqs {
		r.passTCs += newPass - oldPass
		r.failTCs += newFail - oldFail
		e.refresh(r)
	}
}

func (e *Engine) RecordExecution(execID, testCaseID string, at int64, result Result) error {
	if _, ok := e.execs[execID]; ok {
		return ErrDuplicateExecution
	}
	tc := e.getTC(testCaseID)
	oldPass, oldFail := tcDelta(tc)
	e.seq++
	key := execKey{at: at, seq: e.seq}
	tc.heap.insert(key, execID, result)
	e.execs[execID] = execRef{tc: tc, key: key}
	tc.cur, tc.hasCur = tc.heap.top()
	e.applyTCResult(tc, oldPass, oldFail)
	return nil
}

func (e *Engine) DeleteExecution(execID string) error {
	ref, ok := e.execs[execID]
	if !ok {
		return ErrUnknownExecution
	}
	tc := ref.tc
	oldPass, oldFail := tcDelta(tc)
	tc.heap.remove(ref.key)
	delete(e.execs, execID)
	tc.cur, tc.hasCur = tc.heap.top()
	e.applyTCResult(tc, oldPass, oldFail)
	return nil
}

func uniq(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func (e *Engine) CreateAnomaly(id string, severity int, reqIDs, testCaseIDs []string) error {
	if _, ok := e.anomalies[id]; ok {
		return ErrDuplicateAnomaly
	}
	if severity < 1 || severity > 4 {
		return ErrInvalidSeverity
	}
	reqIDs = uniq(reqIDs)
	testCaseIDs = uniq(testCaseIDs)
	for _, rid := range reqIDs {
		if _, ok := e.reqs[rid]; !ok {
			return ErrUnknownRequirement
		}
	}
	an := &anomaly{id: id, severity: severity, state: Open, reqs: reqIDs, tcs: testCaseIDs}
	e.anomalies[id] = an
	if severity == 1 {
		e.activate(an)
	}
	return nil
}

// activate adds the blocking channels of a severity-1 anomaly; deactivate
// removes them. Callers must only invoke these on severity-1 anomalies whose
// active flag is about to flip.
func (e *Engine) activate(an *anomaly) {
	for _, rid := range an.reqs {
		r := e.reqs[rid]
		r.blockers[an.id]++
		e.refresh(r)
	}
	for _, tcID := range an.tcs {
		tc := e.getTC(tcID)
		tc.anomalies[an.id] = struct{}{}
		for _, r := range tc.reqs {
			r.blockers[an.id]++
			e.refresh(r)
		}
	}
}

func (e *Engine) deactivate(an *anomaly) {
	for _, rid := range an.reqs {
		r := e.reqs[rid]
		r.blockers[an.id]--
		if r.blockers[an.id] == 0 {
			delete(r.blockers, an.id)
		}
		e.refresh(r)
	}
	for _, tcID := range an.tcs {
		tc := e.tcs[tcID]
		delete(tc.anomalies, an.id)
		for _, r := range tc.reqs {
			r.blockers[an.id]--
			if r.blockers[an.id] == 0 {
				delete(r.blockers, an.id)
			}
			e.refresh(r)
		}
	}
}

var anomalyTransitions = map[AnomalyState][]AnomalyState{
	Open:               {UnderInvestigation},
	UnderInvestigation: {Resolved},
	Resolved:           {Closed, Open},
}

func (e *Engine) TransitionAnomaly(id string, to AnomalyState) error {
	an, ok := e.anomalies[id]
	if !ok {
		return ErrUnknownAnomaly
	}
	legal := false
	for _, t := range anomalyTransitions[an.state] {
		if t == to {
			legal = true
			break
		}
	}
	if !legal {
		return ErrInvalidTransition
	}
	wasActive := an.active()
	an.state = to
	nowActive := an.active()
	if an.severity == 1 && wasActive != nowActive {
		if nowActive {
			e.activate(an)
		} else {
			e.deactivate(an)
		}
	}
	return nil
}

// Requirement returns the derived state and coverage-gap flag of a requirement.
func (e *Engine) Requirement(id string) (State, bool, error) {
	r, ok := e.reqs[id]
	if !ok {
		return Pending, false, ErrUnknownRequirement
	}
	return r.state, r.gap, nil
}

func (e *Engine) Summary() Summary { return e.summary }
