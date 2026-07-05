package scheduler

import (
	"context"
	"sync"
	"time"

	"sec_monitor/internal/service"

	"github.com/robfig/cron/v3"
)

const (
	candidateNotificationSyncTaskName = "candidate_notification_sync"
	ipoRadarSyncTaskName              = "ipo_radar_sync"
	smallCapDiscoverySyncTaskName     = "small_cap_discovery_sync"
	secFilingSyncTaskName             = "sec_filing_sync"
)

type Scheduler struct {
	cron                   *cron.Cron
	tasks                  *service.TaskConfigService
	configs                *service.ConfigService
	filings                *service.FilingService
	ipo                    *service.IPORadarService
	candidateNotifications *service.CandidateNotificationService
	discoverySync          *service.DiscoverySyncService
	mu                     sync.Mutex
	running                bool
	started                bool
}

func New(tasks *service.TaskConfigService, filings *service.FilingService, services ...any) *Scheduler {
	var ipoService *service.IPORadarService
	var candidateNotifications *service.CandidateNotificationService
	var discoverySync *service.DiscoverySyncService
	var configs *service.ConfigService
	for _, svc := range services {
		switch typed := svc.(type) {
		case *service.ConfigService:
			configs = typed
		case *service.IPORadarService:
			ipoService = typed
		case *service.CandidateNotificationService:
			candidateNotifications = typed
		case *service.DiscoverySyncService:
			discoverySync = typed
		}
	}
	return &Scheduler{
		cron:                   cron.New(),
		tasks:                  tasks,
		configs:                configs,
		filings:                filings,
		ipo:                    ipoService,
		candidateNotifications: candidateNotifications,
		discoverySync:          discoverySync,
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.Reload(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.cron.Start()
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) Stop() context.Context {
	s.mu.Lock()
	cronInstance := s.cron
	s.started = false
	s.mu.Unlock()
	return cronInstance.Stop()
}

func (s *Scheduler) Reload(ctx context.Context) error {
	location, err := s.schedulerLocation(ctx)
	if err != nil {
		return err
	}
	nextCron := cron.New(cron.WithLocation(location))
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
		if _, err := nextCron.AddFunc(task.CronExpr, func() {
			_ = s.RunTask(context.Background(), taskName)
		}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	previousCron := s.cron
	wasStarted := s.started
	s.cron = nextCron
	if wasStarted {
		s.cron.Start()
	}
	s.mu.Unlock()
	if wasStarted && previousCron != nil {
		<-previousCron.Stop().Done()
	}
	return nil
}

func (s *Scheduler) schedulerLocation(ctx context.Context) (*time.Location, error) {
	if s == nil || s.configs == nil {
		return time.UTC, nil
	}
	location, _, err := s.configs.SchedulerTimezone(ctx)
	if err != nil {
		return nil, err
	}
	return location, nil
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
	case candidateNotificationSyncTaskName:
		return s.candidateNotifications != nil
	case smallCapDiscoverySyncTaskName:
		return s.discoverySync != nil
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
		_, err := s.ipo.RefreshWithTrigger(ctx, "ipo_scheduler")
		return err
	case candidateNotificationSyncTaskName:
		_, err := s.candidateNotifications.Send(ctx, service.CandidateNotificationSendInput{Confirm: true})
		return err
	case smallCapDiscoverySyncTaskName:
		_, err := s.discoverySync.Run(ctx)
		return err
	default:
		return nil
	}
}
