package discovery

import (
	"sort"
	"time"
)

const (
	CapitalEventConfirmedFinancing  = "confirmed_financing"
	CapitalEventRegisteredFinancing = "registered_financing"
	CapitalEventATMProgram          = "atm_program"
	CapitalEventReverseSplit        = "reverse_split"
	CapitalEventGoingConcern        = "going_concern"
	CapitalEventWarrants            = "warrants"
)

const (
	CapitalRiskSeverityHigh   = "high"
	CapitalRiskSeverityMedium = "medium"
	CapitalRiskSeverityLow    = "low"
)

type CapitalRiskAssessment struct {
	CIK, Kind, Accession    string
	EffectiveAt, AcceptedAt time.Time
	ActiveUntil             time.Time
	Active                  bool
	BlocksA                 bool
	BlocksB                 bool
	Severity                string
	ChangesShares           bool
	Reason                  string
}

func AssessCapitalRisks(events []CapitalEvent, asOf time.Time) []CapitalRiskAssessment {
	risks := make([]CapitalRiskAssessment, 0, len(events))
	for _, event := range events {
		kind := normalizeCapitalEventKind(event.Kind)
		policy := capitalRiskPolicy(kind)
		if policy.severity == "" {
			policy = capitalRiskPolicy("default")
		}
		activeUntil := time.Time{}
		if policy.activeDays > 0 {
			activeUntil = event.EffectiveAt.Add(time.Duration(policy.activeDays) * 24 * time.Hour)
		}
		active := !event.EffectiveAt.IsZero() && !event.EffectiveAt.After(asOf)
		if !activeUntil.IsZero() && asOf.After(activeUntil) {
			active = false
		}
		if !event.AcceptedAt.IsZero() && event.AcceptedAt.After(asOf) {
			active = false
		}
		risks = append(risks, CapitalRiskAssessment{
			CIK: event.CIK, Kind: kind, Accession: event.Accession, EffectiveAt: event.EffectiveAt, AcceptedAt: event.AcceptedAt,
			ActiveUntil: activeUntil, Active: active, BlocksA: policy.blocksA, BlocksB: policy.blocksB,
			Severity: policy.severity, ChangesShares: event.ChangesShares, Reason: event.Reason,
		})
	}
	sort.Slice(risks, func(i, j int) bool { return canonicalLess(risks[i], risks[j]) })
	return risks
}

func CapitalRiskToSnapshot(batchID string, securityID uint, risk CapitalRiskAssessment, now time.Time) CapitalRiskSnapshot {
	return CapitalRiskSnapshot{
		BatchID: batchID, SecurityID: securityID, Kind: risk.Kind, Accession: risk.Accession,
		EffectiveAt: risk.EffectiveAt, AcceptedAt: risk.AcceptedAt, ActiveUntil: risk.ActiveUntil,
		Active: risk.Active, BlocksA: risk.BlocksA, BlocksB: risk.BlocksB, Severity: risk.Severity,
		ChangesShares: risk.ChangesShares, Reason: risk.Reason, CreatedAt: now,
	}
}

type capitalRiskPolicyRule struct {
	activeDays       int
	blocksA, blocksB bool
	severity         string
}

func capitalRiskPolicy(kind string) capitalRiskPolicyRule {
	switch normalizeCapitalEventKind(kind) {
	case CapitalEventConfirmedFinancing:
		return capitalRiskPolicyRule{activeDays: 90, blocksA: true, severity: CapitalRiskSeverityHigh}
	case CapitalEventATMProgram:
		return capitalRiskPolicyRule{activeDays: 90, blocksA: true, severity: CapitalRiskSeverityHigh}
	case CapitalEventReverseSplit:
		return capitalRiskPolicyRule{activeDays: 365, blocksA: true, severity: CapitalRiskSeverityHigh}
	case CapitalEventGoingConcern:
		return capitalRiskPolicyRule{blocksA: true, blocksB: true, severity: CapitalRiskSeverityHigh}
	case CapitalEventWarrants:
		return capitalRiskPolicyRule{activeDays: 365, blocksA: true, severity: CapitalRiskSeverityHigh}
	case CapitalEventRegisteredFinancing, "potential_financing", "potential_registration_effective":
		return capitalRiskPolicyRule{activeDays: 90, severity: CapitalRiskSeverityMedium}
	default:
		return capitalRiskPolicyRule{activeDays: 90, severity: CapitalRiskSeverityLow}
	}
}
