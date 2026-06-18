package scheduler

import (
	"context"
	"sync"
	"time"

	"sec_monitor/internal/service"

	"github.com/robfig/cron/v3"
)

const (
	ipoRadarSyncTaskName  = "ipo_radar_sync"
	secFilingSyncTaskName = "sec_filing_sync"
)

type Scheduler struct {
	cron    *cron.Cron
	tasks   *service.TaskConfigService
	filings *service.FilingService
	ipo     *service.IPORadarService
	mu      sync.Mutex
	running bool
}

func New(tasks *service.TaskConfigService, filings *service.FilingService, ipo ...*service.IPORadarService) *Scheduler {
	var ipoService *service.IPORadarService
	if len(ipo) > 0 {
		ipoService = ipo[0]
	}
	return &Scheduler{
		cron:    cron.New(),
		tasks:   tasks,
		filings: filings,
		ipo:     ipoService,
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.Reload(ctx); err != nil {
		return err
	}
	s.cron.Start()
	return nil
}

func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

func (s *Scheduler) Reload(ctx context.Context) error {
	s.cron = cron.New()
	tasks, err := s.tasks.List(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		taskName := task.TaskName
		if !s.canRunTask(taskName) {
			continue
		}
		if _, err := s.cron.AddFunc(task.CronExpr, func() {
			_ = s.RunTask(context.Background(), taskName)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	return s.RunTask(ctx, secFilingSyncTaskName)
}

func (s *Scheduler) RunTask(ctx context.Context, taskName string) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	if err := s.tasks.MarkRunStarted(ctx, taskName); err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return err
	}

	err := s.runTask(ctx, taskName)
	finishedAt := time.Now().UTC()
	finishErr := s.tasks.MarkRunFinished(ctx, taskName, finishedAt)

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	if err != nil {
		return err
	}
	return finishErr
}

func (s *Scheduler) canRunTask(taskName string) bool {
	switch taskName {
	case secFilingSyncTaskName:
		return s.filings != nil
	case ipoRadarSyncTaskName:
		return s.ipo != nil
	default:
		return false
	}
}

func (s *Scheduler) runTask(ctx context.Context, taskName string) error {
	switch taskName {
	case secFilingSyncTaskName:
		_, err := s.filings.RefreshWithTrigger(ctx, "scheduler")
		return err
	case ipoRadarSyncTaskName:
		_, err := s.ipo.Refresh(ctx)
		return err
	default:
		return nil
	}
}
