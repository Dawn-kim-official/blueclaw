package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/blueclaw/blueclaw/internal/agent"
	"github.com/blueclaw/blueclaw/internal/outbox"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
	"github.com/go-co-op/gocron/v2"
)

type ScheduledJob struct {
	ID             string    `json:"id"`
	CronExpression string    `json:"cronExpression"`
	Prompt         string    `json:"prompt"`
	CreatedAt      time.Time `json:"createdAt"`
	LastRunAt      time.Time `json:"lastRunAt,omitempty"`
	NextRunAt      time.Time `json:"nextRunAt,omitempty"`
}

type Service struct {
	scheduler         gocron.Scheduler
	jobs              []ScheduledJob
	jobsMutex         sync.RWMutex
	jobsFilePath      string
	blueclawDirectory string
	agentRunner       agent.Runner
	outbox            *outbox.Outbox
	executionMutex    sync.Mutex
}

func NewService(blueclawDirectory string, agentRunner agent.Runner, messageOutbox *outbox.Outbox) (*Service, error) {
	cronScheduler, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("creating cron scheduler: %w", err)
	}
	service := &Service{
		scheduler:         cronScheduler,
		jobsFilePath:      filepath.Join(blueclawDirectory, "cron", "jobs.json"),
		blueclawDirectory: blueclawDirectory,
		agentRunner:       agentRunner,
		outbox:            messageOutbox,
	}
	if err := service.loadJobs(); err != nil {
		log.Printf("scheduler: failed to load jobs: %v", err)
	}
	return service, nil
}

func (service *Service) SetAgentRunner(agentRunner agent.Runner) {
	service.agentRunner = agentRunner
}

func (service *Service) Start() {
	for _, job := range service.jobs {
		service.registerCronJob(job)
	}
	service.scheduler.Start()
}

func (service *Service) Stop() error {
	return service.scheduler.Shutdown()
}

func (service *Service) AddJob(cronExpression string, prompt string) (tool.ScheduledJobInfo, error) {
	job := ScheduledJob{
		ID:             fmt.Sprintf("job_%d", time.Now().UnixNano()),
		CronExpression: cronExpression,
		Prompt:         prompt,
		CreatedAt:      time.Now(),
	}
	if err := service.registerCronJob(job); err != nil {
		return tool.ScheduledJobInfo{}, err
	}
	service.jobsMutex.Lock()
	service.jobs = append(service.jobs, job)
	service.jobsMutex.Unlock()
	if err := service.saveJobs(); err != nil {
		return tool.ScheduledJobInfo{ID: job.ID}, fmt.Errorf("saving jobs: %w", err)
	}
	return tool.ScheduledJobInfo{ID: job.ID, NextRunAt: job.NextRunAt}, nil
}

func (service *Service) ListJobs() []ScheduledJob {
	service.jobsMutex.RLock()
	defer service.jobsMutex.RUnlock()
	result := make([]ScheduledJob, len(service.jobs))
	copy(result, service.jobs)
	return result
}

func (service *Service) registerCronJob(job ScheduledJob) error {
	_, err := service.scheduler.NewJob(
		gocron.CronJob(job.CronExpression, false),
		gocron.NewTask(func() {
			service.executeJob(job)
		}),
	)
	if err != nil {
		return fmt.Errorf("registering cron job %s: %w", job.ID, err)
	}
	return nil
}

func (service *Service) executeJob(job ScheduledJob) {
	service.executionMutex.Lock()
	defer service.executionMutex.Unlock()
	sessionID := fmt.Sprintf("cron_%s_%d", job.ID, time.Now().UnixNano())
	session := agent.NewSession(sessionID)
	session.AddMessage(provider.Message{Role: "user", Content: job.Prompt})
	promptContext := agent.PromptContext{
		BlueclawDirectory: service.blueclawDirectory,
		ToolRegistry:      nil,
	}
	request, err := agent.BuildRequest(promptContext, session)
	if err != nil {
		log.Printf("scheduler: failed to build request for job %s: %v", job.ID, err)
		return
	}
	jobContext, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	response, err := service.agentRunner.RunAgent(jobContext, request, sessionID)
	if err != nil {
		log.Printf("scheduler: job %s failed: %v", job.ID, err)
		return
	}
	if response.Message.Content != "" {
		source := fmt.Sprintf("cron:%s", job.ID)
		if err := service.outbox.Write(source, response.Message.Content); err != nil {
			log.Printf("scheduler: failed to write outbox for job %s: %v", job.ID, err)
		}
	}
	service.updateJobTimestamp(job.ID)
}

func (service *Service) updateJobTimestamp(jobID string) {
	service.jobsMutex.Lock()
	defer service.jobsMutex.Unlock()
	for i := range service.jobs {
		if service.jobs[i].ID == jobID {
			service.jobs[i].LastRunAt = time.Now()
			break
		}
	}
	service.saveJobs()
}

func (service *Service) loadJobs() error {
	data, err := os.ReadFile(service.jobsFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			service.jobs = make([]ScheduledJob, 0)
			return nil
		}
		return fmt.Errorf("reading jobs file: %w", err)
	}
	return json.Unmarshal(data, &service.jobs)
}

func (service *Service) saveJobs() error {
	directory := filepath.Dir(service.jobsFilePath)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("creating cron directory: %w", err)
	}
	data, err := json.MarshalIndent(service.jobs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling jobs: %w", err)
	}
	return os.WriteFile(service.jobsFilePath, data, 0644)
}
