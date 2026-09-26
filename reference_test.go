package reqstate

// reference is a deliberately simple from-scratch model used to
// differential-test the incremental Engine. It keeps raw facts and
// recomputes everything on demand.
type reference struct {
	reqs      map[string]bool
	edges     map[[2]string]bool // parent -> derived
	links     map[[2]string]bool // req -> test case
	execs     map[string]refExec
	anomalies map[string]refAnomaly
	seq       uint64
}

type refExec struct {
	tc     string
	at     int64
	seq    uint64
	result Result
}

type refAnomaly struct {
	severity int
	state    AnomalyState
	reqs     []string
	tcs      []string
}

func newReference() *reference {
	return &reference{
		reqs:      map[string]bool{},
		edges:     map[[2]string]bool{},
		links:     map[[2]string]bool{},
		execs:     map[string]refExec{},
		anomalies: map[string]refAnomaly{},
	}
}

// tcCurrent returns the current result of a test case: max by (at, seq).
func (m *reference) tcCurrent() map[string]Result {
	best := map[string]refExec{}
	has := map[string]bool{}
	for _, ex := range m.execs {
		cur, ok := best[ex.tc]
		if !ok || ex.at > cur.at || (ex.at == cur.at && ex.seq > cur.seq) {
			best[ex.tc] = ex
			has[ex.tc] = true
		}
	}
	out := map[string]Result{}
	for tc, ex := range best {
		out[tc] = ex.result
	}
	return out
}

func (m *reference) childrenOf() map[string][]string {
	out := map[string][]string{}
	for e := range m.edges {
		out[e[0]] = append(out[e[0]], e[1])
	}
	return out
}

func (m *reference) linkedTCs() map[string][]string {
	out := map[string][]string{}
	for l := range m.links {
		out[l[0]] = append(out[l[0]], l[1])
	}
	return out
}

func (m *reference) recompute() (map[string]State, map[string]bool, Summary) {
	cur := m.tcCurrent()
	children := m.childrenOf()
	linked := m.linkedTCs()

	states := map[string]State{}
	gaps := map[string]bool{}
	visiting := map[string]bool{}

	var solve func(id string) (State, bool)
	solve = func(id string) (State, bool) {
		if s, ok := states[id]; ok {
			return s, gaps[id]
		}
		if visiting[id] {
			panic("cycle in reference model")
		}
		visiting[id] = true
		defer delete(visiting, id)

		kids := children[id]
		tcs := linked[id]

		blocked := false
		for _, an := range m.anomalies {
			if an.severity != 1 || (an.state != Open && an.state != UnderInvestigation) {
				continue
			}
			hit := false
			for _, r := range an.reqs {
				if r == id {
					hit = true
				}
			}
			for _, at := range an.tcs {
				for _, lt := range tcs {
					if at == lt {
						hit = true
					}
				}
			}
			if hit {
				blocked = true
				break
			}
		}
		failed := false
		for _, tc := range tcs {
			if r, ok := cur[tc]; ok && r == Fail {
				failed = true
			}
		}
		allKidsVerified := true
		allKidsGap := true
		for _, k := range kids {
			ks, kg := solve(k)
			switch ks {
			case Blocked:
				blocked = true
			case Failed:
				failed = true
			}
			if ks != Verified {
				allKidsVerified = false
			}
			if !kg {
				allKidsGap = false
			}
		}

		var s State
		switch {
		case blocked:
			s = Blocked
		case failed:
			s = Failed
		case len(tcs)+len(kids) > 0 && allKidsVerified && allPass(tcs, cur):
			s = Verified
		default:
			s = Pending
		}
		gap := len(tcs) == 0 && allKidsGap
		states[id] = s
		gaps[id] = gap
		return s, gap
	}

	var sum Summary
	for id := range m.reqs {
		s, g := solve(id)
		sum.add(s, 1)
		if g {
			sum.Gaps++
		}
	}
	return states, gaps, sum
}

func allPass(tcs []string, cur map[string]Result) bool {
	for _, tc := range tcs {
		r, ok := cur[tc]
		if !ok || r != Pass {
			return false
		}
	}
	return true
}
