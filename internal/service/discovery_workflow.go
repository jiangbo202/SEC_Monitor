package service

import (
	"context"
	"errors"

	"sec_monitor/internal/discovery"

	"gorm.io/gorm"
)

const (
	DiscoveryWorkflowReady     = "ready"
	DiscoveryWorkflowNoCurrent = "no_current_batch"
)

type DiscoveryWorkflowService struct {
	discoveryDB *gorm.DB
}

type DiscoveryWorkflowResult struct {
	Status  string                     `json:"status"`
	BatchID string                     `json:"batch_id"`
	Summary discovery.CandidateSummary `json:"summary"`
	Health  discovery.CandidateHealth  `json:"health"`
}

func NewDiscoveryWorkflowService(discoveryDB *gorm.DB) *DiscoveryWorkflowService {
	return &DiscoveryWorkflowService{discoveryDB: discoveryDB}
}

func (s *DiscoveryWorkflowService) Refresh(ctx context.Context) (DiscoveryWorkflowResult, error) {
	if s == nil || s.discoveryDB == nil {
		return DiscoveryWorkflowResult{}, errors.New("discovery workflow service is not configured")
	}
	summary, err := discovery.BuildCandidateSummary(ctx, s.discoveryDB, 10)
	if err != nil {
		return DiscoveryWorkflowResult{}, err
	}
	health, err := discovery.BuildCandidateHealth(ctx, s.discoveryDB)
	if err != nil {
		return DiscoveryWorkflowResult{}, err
	}
	status := DiscoveryWorkflowReady
	if summary.BatchID == "" {
		status = DiscoveryWorkflowNoCurrent
	}
	return DiscoveryWorkflowResult{Status: status, BatchID: summary.BatchID, Summary: summary, Health: health}, nil
}
