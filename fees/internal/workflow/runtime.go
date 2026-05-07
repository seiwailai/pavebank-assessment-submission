package workflow

import (
	"fmt"

	"go.temporal.io/sdk/activity"
	temporalclient "go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"
)

type RuntimeConfig struct {
	HostPort  string
	Namespace string
	TaskQueue string
}

type TemporalRuntime struct {
	client    temporalclient.Client
	worker    temporalworker.Worker
	taskQueue string
}

func NewTemporalRuntime(store ActivityStore, cfg RuntimeConfig) (*TemporalRuntime, error) {
	if store == nil {
		return nil, fmt.Errorf("temporal runtime activity store is required")
	}

	if cfg.TaskQueue == "" {
		cfg.TaskQueue = DefaultTaskQueue
	}
	if cfg.HostPort == "" {
		cfg.HostPort = temporalclient.DefaultHostPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = temporalclient.DefaultNamespace
	}

	client, err := temporalclient.NewLazyClient(temporalclient.Options{
		HostPort:  cfg.HostPort,
		Namespace: cfg.Namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("create temporal client: %w", err)
	}

	worker := temporalworker.New(client, cfg.TaskQueue, temporalworker.Options{})
	registerWorkflowRuntime(worker, &Activities{Store: store})

	if err := worker.Start(); err != nil {
		client.Close()
		return nil, fmt.Errorf("start temporal worker: %w", err)
	}

	return &TemporalRuntime{
		client:    client,
		worker:    worker,
		taskQueue: cfg.TaskQueue,
	}, nil
}

func (r *TemporalRuntime) Client() Client {
	if r == nil {
		return nil
	}
	return NewTemporalClient(r.client, r.taskQueue)
}

func (r *TemporalRuntime) Close() {
	if r == nil {
		return
	}
	if r.worker != nil {
		r.worker.Stop()
	}
	if r.client != nil {
		r.client.Close()
	}
}

func registerWorkflowRuntime(worker temporalworker.Worker, activities *Activities) {
	worker.RegisterWorkflow(BillWorkflow)
	worker.RegisterActivityWithOptions(activities.TransitionBillStatus, activity.RegisterOptions{Name: ActivityNameTransitionBillStatus})
	worker.RegisterActivityWithOptions(activities.PersistLineItems, activity.RegisterOptions{Name: ActivityNamePersistLineItems})
	worker.RegisterActivityWithOptions(activities.FinalizeBill, activity.RegisterOptions{Name: ActivityNameFinalizeBill})
	worker.RegisterActivityWithOptions(activities.MarkBillFailed, activity.RegisterOptions{Name: ActivityNameMarkBillFailed})
}
