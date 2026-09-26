package reqv

// Status is the derived verification status of a requirement.
type Status int

const (
	StatusPending Status = iota
	StatusVerified
	StatusFailed
	StatusBlocked
)

// Result is the outcome of a single test-case execution.
type Result int

const (
	ResultPass Result = iota
	ResultFail
	ResultIncomplete
)

// AnomalyState is the lifecycle state of an anomaly.
type AnomalyState int

const (
	AnomalyOpen AnomalyState = iota
	AnomalyUnderInvestigation
	AnomalyResolved
	AnomalyClosed
)

// Summary holds aggregate counts over all requirements.
type Summary struct {
	Blocked  int
	Failed   int
	Verified int
	Pending  int
	Gaps     int
}
