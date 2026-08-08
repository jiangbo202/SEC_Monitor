package service

import (
	"context"
	"errors"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

// TradePlanSimulationService restricts simulations to enabled monitoring
// targets. It coordinates local data only and cannot submit brokerage orders.
type TradePlanSimulationService struct {
	db          *gorm.DB
	discoveryDB *gorm.DB
}

func NewTradePlanSimulationService(db, discoveryDB *gorm.DB) *TradePlanSimulationService {
	return &TradePlanSimulationService{db: db, discoveryDB: discoveryDB}
}

func (s *TradePlanSimulationService) Rebuild(ctx context.Context) (discovery.TradePlanSimulationRebuildResult, error) {
	if s == nil || s.db == nil || s.discoveryDB == nil {
		return discovery.TradePlanSimulationRebuildResult{}, errors.New("trade plan simulation service is not configured")
	}
	tickers, err := s.enabledTickers(ctx)
	if err != nil {
		return discovery.TradePlanSimulationRebuildResult{}, err
	}
	return discovery.RebuildTradePlanSimulations(ctx, s.discoveryDB, tickers)
}

func (s *TradePlanSimulationService) Report(ctx context.Context) (discovery.TradePlanSimulationReport, error) {
	if s == nil || s.db == nil || s.discoveryDB == nil {
		return discovery.TradePlanSimulationReport{}, errors.New("trade plan simulation service is not configured")
	}
	tickers, err := s.enabledTickers(ctx)
	if err != nil {
		return discovery.TradePlanSimulationReport{}, err
	}
	return discovery.ListTradePlanSimulations(ctx, s.discoveryDB, tickers)
}

func (s *TradePlanSimulationService) enabledTickers(ctx context.Context) ([]string, error) {
	var targets []model.WatchTarget
	if err := s.db.WithContext(ctx).Where("status = ?", "enabled").Order("ticker ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	tickers := make([]string, 0, len(targets))
	for _, target := range targets {
		tickers = append(tickers, target.Ticker)
	}
	return tickers, nil
}
