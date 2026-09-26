package reqstate

import (
	"math/rand"
	"testing"
)

// genStats tracks whether the random generator actually produces the
// situations the spec calls out.
type genStats struct {
	dualPathAnomaly   bool // sev-1 anomaly hits req directly and via a linked TC
	dualPathRemoved   bool // ...and later the TC path is removed while direct stays
	deletedNewest     bool // deleted the current (max at,seq) execution of a TC
	execBeforeLink    bool // execution recorded on a TC before it was ever linked
	anomalyBeforeLink bool // anomaly created on a TC before it was ever linked
	lateLink          bool // such a TC got linked afterwards
	reopened          bool // Resolved -> Open
	linkedTCs         map[string]bool
	touchedUnlinked   map[string]bool
}

func newGenStats() *genStats {
	return &genStats{linkedTCs: map[string]bool{}, touchedUnlinked: map[string]bool{}}
}

func (s *genStats) onRecord(tc string) {
	if !s.linkedTCs[tc] {
		s.execBeforeLink = true
		s.touchedUnlinked[tc] = true
	}
}

func (s *genStats) onCreateAnomaly(an refAnomaly) {
	for _, tc := range an.tcs {
		if !s.linkedTCs[tc] {
			s.anomalyBeforeLink = true
			s.touchedUnlinked[tc] = true
		}
	}
}

func (s *genStats) onLink(req, tc string, ref *reference) {
	if s.touchedUnlinked[tc] {
		s.lateLink = true
	}
	s.linkedTCs[tc] = true
}

// rescanDualPath records whether any active severity-1 anomaly currently
// hits a requirement both directly and via a linked test case.
func (s *genStats) rescanDualPath(ref *reference) {
	for _, an := range ref.anomalies {
		if an.severity != 1 || (an.state != Open && an.state != UnderInvestigation) {
			continue
		}
		for _, r := range an.reqs {
			for _, t := range an.tcs {
				if ref.links[[2]string{r, t}] {
					s.dualPathAnomaly = true
				}
			}
		}
	}
}

func (s *genStats) onUnlink(req, tc string, ref *reference) {
	for _, an := range ref.anomalies {
		if an.severity != 1 || (an.state != Open && an.state != UnderInvestigation) {
			continue
		}
		direct, viaTC := false, false
		for _, r := range an.reqs {
			if r == req {
				direct = true
			}
		}
		for _, t := range an.tcs {
			if t == tc {
				viaTC = true
			}
		}
		if direct && viaTC {
			s.dualPathRemoved = true
		}
	}
}

func (s *genStats) onDeleteExec(id string, ref *reference) {
	ex, ok := ref.execs[id]
	if !ok {
		return
	}
	newest := true
	for _, o := range ref.execs {
		if o.tc != ex.tc {
			continue
		}
		if o.at > ex.at || (o.at == ex.at && o.seq > ex.seq) {
			newest = false
		}
	}
	if newest {
		s.deletedNewest = true
	}
}

// hasDiamond reports whether the ref DAG contains a diamond: a node with
// two parents that share a common ancestor.
func hasDiamond(ref *reference) bool {
	parents := map[string][]string{}
	for e := range ref.edges {
		parents[e[1]] = append(parents[e[1]], e[0])
	}
	ancestors := func(start string) map[string]bool {
		out := map[string]bool{}
		stack := []string{start}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, p := range parents[n] {
				if !out[p] {
					out[p] = true
					stack = append(stack, p)
				}
			}
		}
		return out
	}
	for _, ps := range parents {
		if len(ps) < 2 {
			continue
		}
		shared := ancestors(ps[0])
		for _, other := range ps[1:] {
			for a := range ancestors(other) {
				if shared[a] {
					return true
				}
			}
		}
	}
	return false
}

// TestGeneratorCoverage proves the random generator (seed 1) actually
// produces every situation the spec requires.
func TestGeneratorCoverage(t *testing.T) {
	stats := newGenStats()
	g := &gen{rnd: rand.New(rand.NewSource(1)), eng: New(), ref: newReference()}
	g.stats = stats
	for i := 0; i < 20000; i++ {
		g.step()
	}
	if !hasDiamond(g.ref) {
		t.Error("generator never produced a shared-diamond shape")
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"dual-path anomaly (direct + via TC)", stats.dualPathAnomaly},
		{"dual-path anomaly, one path removed", stats.dualPathRemoved},
		{"deleted newest execution", stats.deletedNewest},
		{"execution recorded before link", stats.execBeforeLink},
		{"anomaly created before link", stats.anomalyBeforeLink},
		{"late link after exec/anomaly", stats.lateLink},
		{"reopen after Resolved", stats.reopened},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("generator never produced: %s", c.name)
		}
	}
}
