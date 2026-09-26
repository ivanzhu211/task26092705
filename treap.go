package reqstate

import "math/rand"

// execKey orders executions: larger at wins; ties broken by larger seq
// (later recorded wins).
type execKey struct {
	at  int64
	seq uint64
}

func (k execKey) less(o execKey) bool {
	if k.at != o.at {
		return k.at < o.at
	}
	return k.seq < o.seq
}

type treapNode struct {
	key    execKey
	execID string
	result Result
	prio   uint32
	left   *treapNode
	right  *treapNode
}

// execHeap is a treap over execution records of one test case, giving
// O(log n) insert, delete and O(log n) access to the current (max) record.
type execHeap struct {
	root *treapNode
	rnd  *rand.Rand
}

func newExecHeap(rnd *rand.Rand) *execHeap {
	return &execHeap{rnd: rnd}
}

func rotateRight(n *treapNode) *treapNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *treapNode) *treapNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func (h *execHeap) insert(key execKey, execID string, result Result) {
	n := &treapNode{key: key, execID: execID, result: result, prio: h.rnd.Uint32()}
	h.root = insertNode(h.root, n)
}

func insertNode(root, n *treapNode) *treapNode {
	if root == nil {
		return n
	}
	if n.key.less(root.key) {
		root.left = insertNode(root.left, n)
		if root.left.prio < root.prio {
			root = rotateRight(root)
		}
	} else {
		root.right = insertNode(root.right, n)
		if root.right.prio < root.prio {
			root = rotateLeft(root)
		}
	}
	return root
}

func (h *execHeap) remove(key execKey) {
	h.root = removeNode(h.root, key)
}

func removeNode(root *treapNode, key execKey) *treapNode {
	if root == nil {
		return nil
	}
	if key.less(root.key) {
		root.left = removeNode(root.left, key)
		return root
	}
	if root.key.less(key) {
		root.right = removeNode(root.right, key)
		return root
	}
	// Found: merge children.
	if root.left == nil {
		return root.right
	}
	if root.right == nil {
		return root.left
	}
	if root.left.prio < root.right.prio {
		root = rotateRight(root)
		root.right = removeNode(root.right, key)
	} else {
		root = rotateLeft(root)
		root.left = removeNode(root.left, key)
	}
	return root
}

// top returns the current (maximum-key) record, or ok=false when empty.
func (h *execHeap) top() (result Result, ok bool) {
	n := h.root
	if n == nil {
		return 0, false
	}
	for n.right != nil {
		n = n.right
	}
	return n.result, true
}
