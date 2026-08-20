package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
)

func providerAttemptsForResult(result ProviderResult) []ProviderAttempt {
	if len(result.Attempts) > 0 {
		return append([]ProviderAttempt(nil), result.Attempts...)
	}
	remaining := result.Expected - result.Records
	if remaining < 0 {
		remaining = 0
	}
	status := "success"
	if result.Records == 0 {
		status = "empty"
	} else if remaining > 0 {
		status = "partial"
	}
	return []ProviderAttempt{{
		Provider:      strings.ToLower(strings.TrimSpace(result.Provider)),
		Status:        status,
		SourceVersion: result.SourceVersion,
		Expected:      result.Expected,
		Records:       result.Records,
		Remaining:     remaining,
		CoveragePct:   result.CoveragePct,
	}}
}

func encodeProviderAttempts(attempts []ProviderAttempt) (string, error) {
	if len(attempts) == 0 {
		return "[]", nil
	}
	payload, err := json.Marshal(attempts)
	if err != nil {
		return "", fmt.Errorf("encode provider attempts: %w", err)
	}
	return string(payload), nil
}

func hydrateProviderRunAttempts(run *ProviderRun) error {
	if run == nil {
		return nil
	}
	run.Attempts = []ProviderAttempt{}
	if strings.TrimSpace(run.AttemptsJSON) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(run.AttemptsJSON), &run.Attempts); err != nil {
		return fmt.Errorf("decode provider attempts for run %d: %w", run.ID, err)
	}
	return nil
}
