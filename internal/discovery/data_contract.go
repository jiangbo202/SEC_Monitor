package discovery

import "time"

// DataLayer names the four durable boundaries used by the research pipeline.
// Raw artifacts are provider-owned evidence, facts are normalized observations,
// features are reproducible calculations, and decisions are versioned research
// conclusions. A downstream layer may reference but must never mutate upstream
// evidence.
type DataLayer string

const (
	DataLayerRaw      DataLayer = "raw"
	DataLayerFact     DataLayer = "fact"
	DataLayerFeature  DataLayer = "feature"
	DataLayerDecision DataLayer = "decision"
)

const (
	DataQualityIncidentOpen     = "open"
	DataQualityIncidentResolved = "resolved"
)

// DataQualityMetadata is the common API contract for source-backed values.
// New endpoints should embed this contract instead of inventing per-page
// freshness fields.
type DataQualityMetadata struct {
	Layer         DataLayer `json:"layer"`
	Source        string    `json:"source,omitempty"`
	SourceVersion string    `json:"source_version,omitempty"`
	AsOf          string    `json:"as_of,omitempty"`
	QualityStatus string    `json:"quality_status"`
	FallbackUsed  bool      `json:"fallback_used"`
	CoveragePct   *float64  `json:"coverage_pct,omitempty"`
}

// DataQualityIncident quarantines one bad fact without aborting unrelated
// symbols in the same provider batch. Fingerprint makes repeated runs
// idempotent while OccurrenceCount preserves operational evidence.
type DataQualityIncident struct {
	ID              uint       `json:"id"`
	Fingerprint     string     `json:"fingerprint" gorm:"size:64;uniqueIndex"`
	Layer           DataLayer  `json:"layer" gorm:"size:16;index"`
	Domain          string     `json:"domain" gorm:"size:32;index"`
	EntityKey       string     `json:"entity_key" gorm:"size:160;index"`
	Reason          string     `json:"reason" gorm:"size:64;index"`
	Source          string     `json:"source" gorm:"size:64;index"`
	SourceVersion   string     `json:"source_version" gorm:"size:128"`
	Status          string     `json:"status" gorm:"size:16;index"`
	Retryable       bool       `json:"retryable" gorm:"index"`
	OccurrenceCount int        `json:"occurrence_count"`
	Detail          string     `json:"detail" gorm:"type:text"`
	FirstObservedAt time.Time  `json:"first_observed_at"`
	LastObservedAt  time.Time  `json:"last_observed_at" gorm:"index"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// researchQualityMetadata gives locally cached research views one consistent
// freshness contract. It does not make a provider observation a decision: the
// caller still supplies the correct raw/fact/feature/decision layer.
func researchQualityMetadata(layer DataLayer, source, sourceVersion string, asOf time.Time, staleAfter, expireAfter time.Duration) DataQualityMetadata {
	result := DataQualityMetadata{Layer: layer, Source: source, SourceVersion: sourceVersion, QualityStatus: QualityStatusMissing}
	if asOf.IsZero() {
		return result
	}
	result.AsOf = asOf.UTC().Format(time.RFC3339)
	result.QualityStatus = QualityStatusValid
	age := time.Since(asOf.UTC())
	if age < 0 {
		age = 0
	}
	if expireAfter > 0 && age > expireAfter {
		result.QualityStatus = "expired"
	} else if staleAfter > 0 && age > staleAfter {
		result.QualityStatus = "stale"
	}
	return result
}
