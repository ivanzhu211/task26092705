package reqv

import "errors"

var (
	ErrUnknownReq        = errors.New("reqv: unknown requirement")
	ErrDuplicateReq      = errors.New("reqv: duplicate requirement")
	ErrDuplicateEdge     = errors.New("reqv: duplicate edge")
	ErrEdgeNotFound      = errors.New("reqv: edge not found")
	ErrCycle             = errors.New("reqv: edge would create a cycle")
	ErrDuplicateLink     = errors.New("reqv: duplicate requirement-testcase link")
	ErrLinkNotFound      = errors.New("reqv: requirement-testcase link not found")
	ErrDuplicateExec     = errors.New("reqv: duplicate execution id")
	ErrUnknownExec       = errors.New("reqv: unknown execution id")
	ErrInvalidResult     = errors.New("reqv: invalid execution result")
	ErrDuplicateAnomaly  = errors.New("reqv: duplicate anomaly id")
	ErrUnknownAnomaly    = errors.New("reqv: unknown anomaly id")
	ErrInvalidSeverity   = errors.New("reqv: invalid anomaly severity")
	ErrInvalidTransition = errors.New("reqv: invalid anomaly state transition")
)

type reqNode struct {
	parents  map[string]struct{}
	children map[string]struct{}
	tcs      map[string]struct{}

	blockedAnom int // active severity-1 anomalies directly linked to this requirement
	blockedTC   int // linked test cases carrying >=1 active severity-1 anomaly
	failTC      int // linked test cases whose current result is fail
	passTC      int // linked test cases whose current result is pass

	blockedChildren  int
	failedChildren   int
	verifiedChildren int
	gapChildren      int

	status Status
	gap    bool
}

type tcNode struct {
	reqs       map[string]struct{}
	execs      execStore
	cur        Result
	hasCur     bool
	activeSev1 int // active severity-1 anomalies linked to this test case
}

type anomaly struct {
	severity int
	state    AnomalyState
	reqs     map[string]struct{}
	tcs      map[string]struct{}
}

type execRef struct {
	tcID    string
	at, seq int64
}

// Engine maintains derived verification statuses for one project, in memory.
// Callers must serialize calls; it is not safe for concurrent use.
type Engine struct {
	reqs      map[string]*reqNode
	tcs       map[string]*tcNode
	anomalies map[string]*anomaly
	execs     map[string]execRef
	execSeq   int64
	summary   Summary
}

func New() *Engine {
	return &Engine{
		reqs:      map[string]*reqNode{},
		tcs:       map[string]*tcNode{},
		anomalies: map[string]*anomaly{},
		execs:     map[string]execRef{},
	}
}

func (e *Engine) getTC(id string) *tcNode {
	tc := e.tcs[id]
	if tc == nil {
		tc = &tcNode{reqs: map[string]struct{}{}}
		e.tcs[id] = tc
	}
	return tc
}

func computeStatus(r *reqNode) Status {
	if r.blockedAnom > 0 || r.blockedTC > 0 || r.blockedChildren > 0 {
		return StatusBlocked
	}
	if r.failTC > 0 || r.failedChildren > 0 {
		return StatusFailed
	}
	if len(r.tcs)+len(r.children) > 0 &&
		r.passTC == len(r.tcs) && r.verifiedChildren == len(r.children) {
		return StatusVerified
	}
	return StatusPending
}

func computeGap(r *reqNode) bool {
	return len(r.tcs) == 0 && r.gapChildren == len(r.children)
}

func (r *reqNode) addChildContrib(st Status, gap bool, delta int) {
	switch st {
	case StatusBlocked:
		r.blockedChildren += delta
	case StatusFailed:
		r.failedChildren += delta
	case StatusVerified:
		r.verifiedChildren += delta
	}
	if gap {
		r.gapChildren += delta
	}
}

// propagate recomputes the seeded requirements and walks upward through
// parents only while statuses or gap flags actually change. Each node is
// processed at most once per call, so shared (diamond) subgraphs cost O(1)
// per affected node rather than per path.
func (e *Engine) propagate(seeds ...string) {
	dirty := map[string]struct{}{}
	var queue []string
	push := func(id string) {
		if _, ok := dirty[id]; !ok {
			dirty[id] = struct{}{}
			queue = append(queue, id)
		}
	}
	for _, s := range seeds {
		push(s)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		r := e.reqs[id]
		ns, ng := computeStatus(r), computeGap(r)
		if ns == r.status && ng == r.gap {
			continue
		}
		old, oldGap := r.status, r.gap
		r.status, r.gap = ns, ng
		e.summary.addStatus(old, -1)
		e.summary.addStatus(ns, +1)
		if oldGap != ng {
			if ng {
				e.summary.Gaps++
			} else {
				e.summary.Gaps--
			}
		}
		for pid := range r.parents {
			p := e.reqs[pid]
			p.addChildContrib(old, oldGap, -1)
			p.addChildContrib(ns, ng, +1)
			push(pid)
		}
	}
}

func (s *Summary) addStatus(st Status, delta int) {
	switch st {
	case StatusBlocked:
		s.Blocked += delta
	case StatusFailed:
		s.Failed += delta
	case StatusVerified:
		s.Verified += delta
	case StatusPending:
		s.Pending += delta
	}
}

func (e *Engine) AddRequirement(id string) error {
	if _, ok := e.reqs[id]; ok {
		return ErrDuplicateReq
	}
	e.reqs[id] = &reqNode{
		parents:  map[string]struct{}{},
		children: map[string]struct{}{},
		tcs:      map[string]struct{}{},
		status:   StatusPending,
		gap:      true,
	}
	e.summary.Pending++
	e.summary.Gaps++
	return nil
}

func (e *Engine) AddEdge(parentID, childID string) error {
	p, ok := e.reqs[parentID]
	if !ok {
		return ErrUnknownReq
	}
	c, ok := e.reqs[childID]
	if !ok {
		return ErrUnknownReq
	}
	if _, ok := p.children[childID]; ok {
		return ErrDuplicateEdge
	}
	// Adding parent->child creates a cycle iff parent is reachable from child.
	if parentID == childID || e.reachable(childID, parentID) {
		return ErrCycle
	}
	p.children[childID] = struct{}{}
	c.parents[parentID] = struct{}{}
	p.addChildContrib(c.status, c.gap, +1)
	e.propagate(parentID)
	return nil
}

// reachable reports whether target is reachable from src following
// parent->child edges.
func (e *Engine) reachable(src, target string) bool {
	seen := map[string]struct{}{src: {}}
	stack := []string{src}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for next := range e.reqs[id].children {
			if next == target {
				return true
			}
			if _, ok := seen[next]; !ok {
				seen[next] = struct{}{}
				stack = append(stack, next)
			}
		}
	}
	return false
}

func (e *Engine) RemoveEdge(parentID, childID string) error {
	p, ok := e.reqs[parentID]
	if !ok {
		return ErrUnknownReq
	}
	c, ok := e.reqs[childID]
	if !ok {
		return ErrUnknownReq
	}
	if _, ok := p.children[childID]; !ok {
		return ErrEdgeNotFound
	}
	delete(p.children, childID)
	delete(c.parents, parentID)
	p.addChildContrib(c.status, c.gap, -1)
	e.propagate(parentID)
	return nil
}

func (e *Engine) LinkTestCase(reqID, tcID string) error {
	r, ok := e.reqs[reqID]
	if !ok {
		return ErrUnknownReq
	}
	if _, ok := r.tcs[tcID]; ok {
		return ErrDuplicateLink
	}
	tc := e.getTC(tcID)
	r.tcs[tcID] = struct{}{}
	tc.reqs[reqID] = struct{}{}
	if tc.hasCur {
		switch tc.cur {
		case ResultPass:
			r.passTC++
		case ResultFail:
			r.failTC++
		}
	}
	if tc.activeSev1 > 0 {
		r.blockedTC++
	}
	e.propagate(reqID)
	return nil
}

func (e *Engine) UnlinkTestCase(reqID, tcID string) error {
	r, ok := e.reqs[reqID]
	if !ok {
		return ErrUnknownReq
	}
	if _, ok := r.tcs[tcID]; !ok {
		return ErrLinkNotFound
	}
	tc := e.tcs[tcID]
	delete(r.tcs, tcID)
	delete(tc.reqs, reqID)
	if tc.hasCur {
		switch tc.cur {
		case ResultPass:
			r.passTC--
		case ResultFail:
			r.failTC--
		}
	}
	if tc.activeSev1 > 0 {
		r.blockedTC--
	}
	e.propagate(reqID)
	return nil
}

func (e *Engine) RecordExecution(execID, tcID string, at int64, result Result) error {
	switch result {
	case ResultPass, ResultFail, ResultIncomplete:
	default:
		return ErrInvalidResult
	}
	if _, ok := e.execs[execID]; ok {
		return ErrDuplicateExec
	}
	tc := e.getTC(tcID)
	e.execSeq++
	seq := e.execSeq
	tc.execs.insert(at, seq, result)
	e.execs[execID] = execRef{tcID: tcID, at: at, seq: seq}
	e.refreshTC(tc)
	return nil
}

func (e *Engine) DeleteExecution(execID string) error {
	ref, ok := e.execs[execID]
	if !ok {
		return ErrUnknownExec
	}
	delete(e.execs, execID)
	tc := e.tcs[ref.tcID]
	tc.execs.delete(ref.at, ref.seq)
	e.refreshTC(tc)
	return nil
}

// refreshTC recomputes a test case's current result and, if it changed,
// updates every linked requirement and propagates.
func (e *Engine) refreshTC(tc *tcNode) {
	cur, hasCur := tc.execs.max()
	if cur == tc.cur && hasCur == tc.hasCur {
		return
	}
	old, oldHas := tc.cur, tc.hasCur
	tc.cur, tc.hasCur = cur, hasCur
	seeds := make([]string, 0, len(tc.reqs))
	for reqID := range tc.reqs {
		r := e.reqs[reqID]
		if oldHas && old == ResultPass {
			r.passTC--
		}
		if oldHas && old == ResultFail {
			r.failTC--
		}
		if hasCur && cur == ResultPass {
			r.passTC++
		}
		if hasCur && cur == ResultFail {
			r.failTC++
		}
		seeds = append(seeds, reqID)
	}
	e.propagate(seeds...)
}

func (e *Engine) CreateAnomaly(id string, severity int, reqIDs, tcIDs []string) error {
	if _, ok := e.anomalies[id]; ok {
		return ErrDuplicateAnomaly
	}
	if severity < 1 || severity > 4 {
		return ErrInvalidSeverity
	}
	for _, reqID := range reqIDs {
		if _, ok := e.reqs[reqID]; !ok {
			return ErrUnknownReq
		}
	}
	a := &anomaly{
		severity: severity,
		state:    AnomalyOpen,
		reqs:     map[string]struct{}{},
		tcs:      map[string]struct{}{},
	}
	for _, reqID := range reqIDs {
		a.reqs[reqID] = struct{}{}
	}
	for _, tcID := range tcIDs {
		a.tcs[tcID] = struct{}{}
	}
	e.anomalies[id] = a
	if severity == 1 {
		e.applyActiveSev1(a, +1)
	}
	return nil
}

// applyActiveSev1 applies the counter effects of an anomaly becoming
// active (delta=+1) or inactive (delta=-1), then propagates.
func (e *Engine) applyActiveSev1(a *anomaly, delta int) {
	seen := map[string]struct{}{}
	var seeds []string
	addSeed := func(id string) {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			seeds = append(seeds, id)
		}
	}
	for reqID := range a.reqs {
		e.reqs[reqID].blockedAnom += delta
		addSeed(reqID)
	}
	for tcID := range a.tcs {
		tc := e.getTC(tcID)
		tc.activeSev1 += delta
		// A test case flips a requirement's blockedTC contribution only when
		// its active severity-1 count crosses zero.
		if (delta > 0 && tc.activeSev1 == 1) || (delta < 0 && tc.activeSev1 == 0) {
			for reqID := range tc.reqs {
				e.reqs[reqID].blockedTC += delta
				addSeed(reqID)
			}
		}
	}
	e.propagate(seeds...)
}

func anomalyActive(s AnomalyState) bool {
	return s == AnomalyOpen || s == AnomalyUnderInvestigation
}

func validTransition(from, to AnomalyState) bool {
	switch from {
	case AnomalyOpen:
		return to == AnomalyUnderInvestigation
	case AnomalyUnderInvestigation:
		return to == AnomalyResolved
	case AnomalyResolved:
		return to == AnomalyClosed || to == AnomalyOpen
	}
	return false
}

func (e *Engine) TransitionAnomaly(id string, to AnomalyState) error {
	a, ok := e.anomalies[id]
	if !ok {
		return ErrUnknownAnomaly
	}
	if !validTransition(a.state, to) {
		return ErrInvalidTransition
	}
	wasActive := anomalyActive(a.state)
	a.state = to
	if a.severity == 1 && wasActive != anomalyActive(to) {
		if wasActive {
			e.applyActiveSev1(a, -1)
		} else {
			e.applyActiveSev1(a, +1)
		}
	}
	return nil
}

// Requirement returns the derived status and coverage-gap flag of a requirement.
func (e *Engine) Requirement(id string) (Status, bool, error) {
	r, ok := e.reqs[id]
	if !ok {
		return StatusPending, false, ErrUnknownReq
	}
	return r.status, r.gap, nil
}

func (e *Engine) Summary() Summary { return e.summary }
