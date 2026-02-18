package heartbeat

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blueclaw/blueclaw/internal/agent"
	"github.com/blueclaw/blueclaw/internal/outbox"
	"github.com/blueclaw/blueclaw/internal/provider"
)

type Service struct {
	interval          time.Duration
	minimumInterval   time.Duration
	maximumInterval   time.Duration
	nextIntervalNanos atomic.Int64
	blueclawDirectory string
	agentRunner       agent.Runner
	outbox            *outbox.Outbox
	cleanupFn         func() error
	stopChannel       chan struct{}
	resetChannel      chan struct{}
}

func NewService(interval time.Duration, minimumInterval time.Duration, maximumInterval time.Duration, blueclawDirectory string, agentRunner agent.Runner, messageOutbox *outbox.Outbox, cleanupFn func() error) *Service {
	return &Service{
		interval:          interval,
		minimumInterval:   minimumInterval,
		maximumInterval:   maximumInterval,
		blueclawDirectory: blueclawDirectory,
		agentRunner:       agentRunner,
		outbox:            messageOutbox,
		cleanupFn:         cleanupFn,
		stopChannel:       make(chan struct{}),
		resetChannel:      make(chan struct{}, 1),
	}
}

func (service *Service) SetAgentRunner(runner agent.Runner) {
	service.agentRunner = runner
}

func (service *Service) Start() {
	go service.run()
}

func (service *Service) Stop() {
	close(service.stopChannel)
}

func (service *Service) SetInterval(duration time.Duration) {
	clamped := duration
	if clamped < service.minimumInterval {
		clamped = service.minimumInterval
	}
	if clamped > service.maximumInterval {
		clamped = service.maximumInterval
	}
	service.nextIntervalNanos.Store(int64(clamped))
}

func (service *Service) ResetToDefault() {
	service.nextIntervalNanos.Store(0)
	select {
	case service.resetChannel <- struct{}{}:
	default:
	}
}

func (service *Service) getInterval() time.Duration {
	next := service.nextIntervalNanos.Swap(0)
	if next != 0 {
		return time.Duration(next)
	}
	return service.interval
}

func (service *Service) run() {
	activeInterval := service.interval
	ticker := time.NewTicker(activeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-service.stopChannel:
			return
		case <-service.resetChannel:
			ticker.Reset(service.interval)
			activeInterval = service.interval
		case <-ticker.C:
			select {
			case <-service.resetChannel:
				ticker.Reset(service.interval)
				activeInterval = service.interval
				continue
			default:
			}
			service.executeHeartbeat(activeInterval)
			nextInterval := service.getInterval()
			if nextInterval != activeInterval {
				ticker.Reset(nextInterval)
				activeInterval = nextInterval
			}
		}
	}
}

const heartbeatOK = "HEARTBEAT_OK"
const heartbeatSuppressInstruction = "\n\nIf nothing needs attention right now, your final response must be exactly: HEARTBEAT_OK (you may still call set_heartbeat_interval before that final message)"

func isHeartbeatSuppressed(response provider.Response) bool {
	if content := strings.TrimSpace(response.Message.Content); content == "" || content == heartbeatOK {
		return true
	}
	for _, item := range response.IntermediateContent {
		if strings.TrimSpace(item) == heartbeatOK {
			return true
		}
	}
	return false
}

func (service *Service) runCleanup() {
	if service.cleanupFn == nil {
		return
	}
	if err := service.cleanupFn(); err != nil {
		log.Printf("heartbeat: memory cleanup failed: %v", err)
	}
}

func (service *Service) executeHeartbeat(activeInterval time.Duration) {
	service.runCleanup()
	heartbeatPath := filepath.Join(service.blueclawDirectory, "HEARTBEAT.md")
	promptContent, err := os.ReadFile(heartbeatPath)
	if err != nil {
		log.Printf("heartbeat: failed to read HEARTBEAT.md: %v", err)
		return
	}
	prompt := strings.TrimSpace(string(promptContent))
	if prompt == "" {
		return
	}
	sessionID := fmt.Sprintf("heartbeat_%d", time.Now().UnixNano())
	session := agent.NewSession(sessionID)
	intervalInstruction := fmt.Sprintf(
		"\n\nYou may call set_heartbeat_interval to schedule your next check-in. Valid range: %s to %s. Current: %s.",
		service.minimumInterval, service.maximumInterval, activeInterval,
	)
	session.AddMessage(provider.Message{Role: "user", Content: prompt + heartbeatSuppressInstruction + intervalInstruction})
	promptContext := agent.PromptContext{
		BlueclawDirectory: service.blueclawDirectory,
		ToolRegistry:      nil,
	}
	request, err := agent.BuildRequest(promptContext, session)
	if err != nil {
		log.Printf("heartbeat: failed to build request: %v", err)
		return
	}
	heartbeatContext, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	response, err := service.agentRunner.RunAgent(heartbeatContext, request, sessionID)
	if err != nil {
		log.Printf("heartbeat: agentic loop failed: %v", err)
		return
	}
	if isHeartbeatSuppressed(response) {
		return
	}
	if err := service.outbox.Write("heartbeat", strings.TrimSpace(response.Message.Content)); err != nil {
		log.Printf("heartbeat: failed to write outbox: %v", err)
	}
}
