package handler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"sec_monitor/internal/discovery"
)

// This cache serves GET consumers only. Research allocation writes retain the
// uncached server gate. Any ORM/SQL mutation invalidates shared evidence after
// its commit; the TTL also bounds changes made outside this process.
type candidateSummaryCache struct {
	once          sync.Once
	mu            sync.Mutex
	revision      atomic.Uint64
	builtRevision uint64
	at            time.Time
	value         candidateSummary
}

type candidateSummary struct {
	Candidates    discovery.CandidateScorePage
	Health        discovery.CandidateHealth
	Overview      discovery.CandidateOverview
	Effectiveness discovery.CandidateEffectivenessReport
	timings       map[string]time.Duration
}

// InitializeReadCaches registers invalidation before scheduler/API concurrency.
func (h *AppHandler) InitializeReadCaches() {
	if h.DiscoveryDB == nil {
		return
	}
	cache := &h.candidateSummary
	cache.once.Do(func() {
		invalidate := func(tx *gorm.DB) {
			if tx.Error == nil && tx.RowsAffected > 0 {
				cache.revision.Add(1)
			}
		}
		seen := map[*gorm.Config]bool{}
		for _, db := range []*gorm.DB{h.DiscoveryDB, h.DB} {
			if db == nil || seen[db.Config] {
				continue
			}
			seen[db.Config] = true
			_ = db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("candidate_summary:invalidate", invalidate)
			_ = db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("candidate_summary:invalidate", invalidate)
			_ = db.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register("candidate_summary:invalidate", invalidate)
			_ = db.Callback().Raw().After("gorm:raw").Register("candidate_summary:invalidate", func(tx *gorm.DB) {
				if tx.Error == nil {
					cache.revision.Add(1)
				}
			})
		}
	})
}

func (h *AppHandler) readCandidateSummary(ctx context.Context) (candidateSummary, error) {
	if h.DiscoveryDB == nil {
		return candidateSummary{}, errors.New("database is required")
	}
	cache := &h.candidateSummary
	h.InitializeReadCaches()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	revision := cache.revision.Load()
	if !cache.at.IsZero() && cache.builtRevision == revision && time.Since(cache.at) < 15*time.Second {
		value := cache.value
		// Server-Timing reports work performed by this request, not the cost
		// of building a snapshot during an earlier request.
		value.timings = nil
		return value, nil
	}
	var result candidateSummary
	var err error
	result.timings = make(map[string]time.Duration)
	// These two read-only aggregates do not depend on each other. Overlap
	// their database reads and CPU work, and join/cancel on every return path.
	buildCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Add(1)
	type effectivenessResult struct {
		value    discovery.CandidateEffectivenessReport
		duration time.Duration
		err      error
	}
	effectivenessDone := make(chan effectivenessResult, 1)
	buildEffectiveness := func() {
		defer workers.Done()
		started := time.Now()
		value, buildErr := discovery.BuildCandidateEffectiveness(buildCtx, h.DiscoveryDB)
		effectivenessDone <- effectivenessResult{value: value, duration: time.Since(started), err: buildErr}
	}
	parallelReads := true
	if dialect, ok := h.DiscoveryDB.Dialector.(*sqlite.Dialector); ok {
		dsn := strings.ToLower(dialect.DSN)
		if (strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")) && !strings.Contains(dsn, "cache=shared") {
			// A private in-memory database belongs to one connection. Opening
			// another connection would expose an entirely different database.
			parallelReads = false
		}
	}
	if parallelReads {
		go buildEffectiveness()
	} else {
		buildEffectiveness()
	}
	defer func() { cancel(); workers.Wait() }()
	stageAt := time.Now()
	result.Candidates, err = discovery.ListAllCandidateScores(buildCtx, h.DiscoveryDB, discovery.CandidateScoreQuery{SkipPerformance: true, SkipValuationDetails: true})
	result.timings["candidates"] = time.Since(stageAt)
	if err != nil {
		return result, err
	}
	stageAt = time.Now()
	result.Health, err = discovery.BuildCandidateHealthWithReadiness(ctx, h.DiscoveryDB, result.Candidates.Items)
	result.timings["candidate_health"] = time.Since(stageAt)
	if err != nil {
		return result, err
	}
	stageAt = time.Now()
	result.Overview, err = discovery.BuildCandidateOverviewFromItems(ctx, h.DiscoveryDB, result.Candidates.Items)
	result.timings["candidate_overview"] = time.Since(stageAt)
	if err != nil {
		return result, err
	}
	effectiveness := <-effectivenessDone
	result.Effectiveness = effectiveness.value
	result.timings["effectiveness"] = effectiveness.duration
	if effectiveness.err != nil {
		return result, effectiveness.err
	}
	if cache.revision.Load() == revision {
		cache.value = result
		cache.builtRevision = revision
		cache.at = time.Now()
	}
	return result, nil
}

func (h *AppHandler) readCandidateOverview(ctx context.Context) (discovery.CandidateOverview, error) {
	value, err := h.readCandidateSummary(ctx)
	return value.Overview, err
}
func (h *AppHandler) readCandidateHealth(ctx context.Context) (discovery.CandidateHealth, error) {
	value, err := h.readCandidateSummary(ctx)
	return value.Health, err
}
func (h *AppHandler) readCandidateEffectiveness(ctx context.Context) (discovery.CandidateEffectivenessReport, error) {
	value, err := h.readCandidateSummary(ctx)
	return value.Effectiveness, err
}
func (h *AppHandler) readResearchActionGate(ctx context.Context) (discovery.ResearchActionGate, error) {
	value, err := h.readCandidateSummary(ctx)
	if err != nil {
		return discovery.ResearchActionGate{}, err
	}
	return discovery.ResearchActionGateFromEvidence(value.Health, value.Effectiveness, time.Now()), nil
}
