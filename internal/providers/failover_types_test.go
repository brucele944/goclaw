package providers

import (
	"errors"
	"strings"
	"testing"
)

// FailoverSummaryError is surfaced (via ModelFallbackProvider) to callers and
// logs, so its message must keep naming every attempted provider/model with the
// classified reason. RunWithFailover itself was deleted as dead code; the type
// and its formatting remain live.
func TestFailoverSummaryError_Message(t *testing.T) {
	err := &FailoverSummaryError{Attempts: []FailoverAttempt{
		{
			Candidate:      ModelCandidate{Provider: "anthropic", Model: "claude-sonnet-4-6", ProfileID: "anthropic/claude-sonnet-4-6"},
			Classification: FailoverClassification{Reason: FailoverRateLimit},
			Err:            errors.New("429"),
		},
		{
			Candidate:      ModelCandidate{Provider: "openai", Model: "gpt-5.5"},
			Classification: FailoverClassification{Reason: FailoverOverloaded},
			Err:            errors.New("503"),
		},
	}}

	msg := err.Error()
	for _, want := range []string{"all 2 failover candidates exhausted", "anthropic/claude-sonnet-4-6", "openai/gpt-5.5", "429", "503"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q is missing %q", msg, want)
		}
	}
}
