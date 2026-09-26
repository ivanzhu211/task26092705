package reqstate

import (
	"math/rand"
	"testing"
)

func TestExecHeap(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	h := newExecHeap(rnd)
	if _, ok := h.top(); ok {
		t.Fatal("empty heap must have no top")
	}
	type rec struct {
		key    execKey
		result Result
	}
	var live []rec
	check := func() {
		want := -1
		for i, r := range live {
			if want < 0 || live[want].key.less(r.key) {
				want = i
			}
		}
		got, ok := h.top()
		if want < 0 {
			if ok {
				t.Fatal("expected empty top")
			}
			return
		}
		if !ok || got != live[want].result {
			t.Fatalf("top mismatch: got %v %v, want %v", got, ok, live[want].result)
		}
	}
	results := []Result{Pass, Fail, Incomplete}
	for i := 0; i < 2000; i++ {
		if len(live) > 0 && rnd.Intn(3) == 0 {
			j := rnd.Intn(len(live))
			h.remove(live[j].key)
			live = append(live[:j], live[j+1:]...)
		} else {
			k := execKey{at: int64(rnd.Intn(50)), seq: uint64(i)}
			r := results[rnd.Intn(3)]
			h.insert(k, "e", r)
			live = append(live, rec{k, r})
		}
		check()
	}
}
