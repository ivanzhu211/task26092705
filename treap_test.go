package reqv

import (
	"math/rand"
	"testing"
)

func TestExecStore(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var s execStore
	type rec struct {
		at, seq int64
		r       Result
	}
	live := map[int64]rec{} // seq -> rec
	results := []Result{ResultPass, ResultFail, ResultIncomplete}
	var nextSeq int64
	for i := 0; i < 20000; i++ {
		if len(live) > 0 && rng.Intn(3) == 0 {
			var seqs []int64
			for seq := range live {
				seqs = append(seqs, seq)
			}
			seq := seqs[rng.Intn(len(seqs))]
			s.delete(live[seq].at, seq)
			delete(live, seq)
		} else {
			at := int64(rng.Intn(50)) // small range forces equal-at ties
			r := results[rng.Intn(3)]
			s.insert(at, nextSeq, r)
			live[nextSeq] = rec{at: at, seq: nextSeq, r: r}
			nextSeq++
		}
		// expected current result: record with max (at, seq)
		var want Result
		wantOK := false
		var wantAt, wantSeq int64
		for _, rc := range live {
			if !wantOK || rc.at > wantAt || (rc.at == wantAt && rc.seq > wantSeq) {
				wantOK, wantAt, wantSeq, want = true, rc.at, rc.seq, rc.r
			}
		}
		got, gotOK := s.max()
		if gotOK != wantOK || (wantOK && got != want) {
			t.Fatalf("step %d: max = (%v,%v), want (%v,%v)", i, got, gotOK, want, wantOK)
		}
	}
}
