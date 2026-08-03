package integration

import (
	"github.com/Dawn-kim-official/blueclaw/agentcontract/harnesstest"
	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
	"github.com/Dawn-kim-official/blueclaw/internal/identity"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func newIntegrationConnectorRuntime(identityService *identity.IdentityService) *connectors.ConnectorRuntime {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)

	connectorRuntime := connectors.NewConnectorRuntime(identityService, harness, taskRunService, nil)
	return connectorRuntime
}
