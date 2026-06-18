package scheduler

import (
	"context"
	"sync"
	"time"

	"sec_monitor/internal/service"

	"github.com/robfig/cron/v3"
)

const secFilingSyncTaskName = "sec_filing_sync"

type Scheduler struct {
	cron    *cron.Cron
	tasks   *service.TaskConfigService
	filings *service.FilingService
	mu      sync.Mutex
	running bool
}

func New(tasks *service.TaskConfigService, filings *service.FilingService) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		tasks:   tasks,
		filings: filings,
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
		if task.TaskName != secFilingSyncTaskName || !task.Enabled {
			continue
		}
		if _, err := s.cron.AddFunc(task.CronExpr, func() {
			_ = s.RunOnce(context.Background())
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	if err := s.tasks.MarkRunStarted(ctx, secFilingSyncTaskName); err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return err
	}

	_, err := s.filings.RefreshWithTrigger(ctx, "scheduler")
	finishedAt := time.Now().UTC()
	finishErr := s.tasks.MarkRunFinished(ctx, secFilingSyncTaskName, finishedAt)

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	if err != nil {
		return err
	}
	return finishErr
}
