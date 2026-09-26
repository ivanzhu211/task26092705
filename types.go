package reqstate

// Result is the outcome of a single test-case execution.
type Result int

const (
	Pass Result = iota
	Fail
	Incomplete
)

// State is the derived verification state of a requirement.
type State int

const (
	Blocked State = iota
	Failed
	Verified
	Pending
)

// AnomalyState is the lifecycle state of an anomaly.
type AnomalyState int

const (
	Open AnomalyState = iota
	UnderInvestigation
	Resolved
	Closed
)

// Summary holds aggregate counts over all requirements.
type Summary struct {
	Blocked  int
	Failed   int
	Verified int
	Pending  int
	Gaps     int
}
