package reqv

// execStore is an ordered set of execution records for one test case,
// keyed by (at, seq). Insert, delete and max are O(log n) expected;
// it is a treap whose priorities are deterministic hashes of the key.
type execStore struct {
	root *execNode
}

type execNode struct {
	at, seq int64
	result  Result
	prio    uint64
	left    *execNode
	right   *execNode
}

// execPrio derives a deterministic pseudo-random priority from the key,
// so the treap shape depends only on the set of keys, not on any RNG state.
func execPrio(at, seq int64) uint64 {
	h := uint64(at)*0x9E3779B97F4A7C15 ^ uint64(seq)*0xC2B2AE3D27D4EB4F
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	h ^= h >> 31
	return h
}

func lessKey(at1, seq1, at2, seq2 int64) bool {
	return at1 < at2 || (at1 == at2 && seq1 < seq2)
}

func rotateRight(n *execNode) *execNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *execNode) *execNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func (s *execStore) insert(at, seq int64, r Result) {
	s.root = insertNode(s.root, at, seq, r)
}

func insertNode(n *execNode, at, seq int64, r Result) *execNode {
	if n == nil {
		return &execNode{at: at, seq: seq, result: r, prio: execPrio(at, seq)}
	}
	if lessKey(at, seq, n.at, n.seq) {
		n.left = insertNode(n.left, at, seq, r)
		if n.left.prio > n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = insertNode(n.right, at, seq, r)
		if n.right.prio > n.prio {
			n = rotateLeft(n)
		}
	}
	return n
}

func (s *execStore) delete(at, seq int64) {
	s.root = deleteNode(s.root, at, seq)
}

func deleteNode(n *execNode, at, seq int64) *execNode {
	if n == nil {
		return nil
	}
	switch {
	case lessKey(at, seq, n.at, n.seq):
		n.left = deleteNode(n.left, at, seq)
	case lessKey(n.at, n.seq, at, seq):
		n.right = deleteNode(n.right, at, seq)
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		if n.left.prio > n.right.prio {
			n = rotateRight(n)
			n.right = deleteNode(n.right, at, seq)
		} else {
			n = rotateLeft(n)
			n.left = deleteNode(n.left, at, seq)
		}
	}
	return n
}

// max returns the result of the record with the largest (at, seq).
func (s *execStore) max() (Result, bool) {
	n := s.root
	if n == nil {
		return 0, false
	}
	for n.right != nil {
		n = n.right
	}
	return n.result, true
}
