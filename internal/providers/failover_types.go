package providers

import (
	"fmt"
	"strings"
)

// ModelCandidate represents a provider+model+key combination for failover.
type ModelCandidate struct {
	Provider  string
	Model     string
	ProfileID string // opaque identifier (never raw API key)
}

// FailoverAttempt records a single failover attempt for diagnostics.
type FailoverAttempt struct {
	Candidate      ModelCandidate
	Classification FailoverClassification
	Err            error
}

// FailoverSummaryError wraps all attempts when failover is exhausted.
type FailoverSummaryError struct {
	Attempts []FailoverAttempt
}

func (e *FailoverSummaryError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "all %d failover candidates exhausted:", len(e.Attempts))
	for i, a := range e.Attempts {
		fmt.Fprintf(&b, " [%d] %s/%s: %s (%v)", i+1, a.Candidate.Provider, a.Candidate.Model, a.Classification.Reason, a.Err)
	}
	return b.String()
}
