package fees

import (
	"context"
	"os"
	"time"

	encore "encore.dev"
	"encore.dev/storage/sqldb"

	billingstore "encore.app/fees/internal/store/billing"
	idempotencystore "encore.app/fees/internal/store/idempotency"
	workflowpkg "encore.app/fees/internal/workflow"
)

//encore:service
type Service struct {
	billingStore     billingstore.Store
	idempotencyStore idempotencystore.Store
	wf               workflowpkg.Client
	runtime          *workflowpkg.TemporalRuntime
	now              func() time.Time
}

//nolint:unused
func initService() (*Service, error) {
	billingStore := billingstore.NewPostgresStore(db)
	idempotencyStore := idempotencystore.NewPostgresStore(db)
	now := func() time.Time { return time.Now().UTC() }

	if encore.Meta().Environment.Type == encore.EnvTest {
		return newServiceWithStores(billingStore, idempotencyStore, workflowpkg.NewFakeClient(), nil, now), nil
	}

	runtime, err := workflowpkg.NewTemporalRuntime(billingStore, temporalRuntimeConfig())
	if err != nil {
		panic(err)
	}

	return newServiceWithStores(billingStore, idempotencyStore, runtime.Client(), runtime, now), nil
}

func newServiceWithStores(
	billingStore billingstore.Store,
	idempotencyStore idempotencystore.Store,
	wf workflowpkg.Client,
	runtime *workflowpkg.TemporalRuntime,
	now func() time.Time,
) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		billingStore:     billingStore,
		idempotencyStore: idempotencyStore,
		wf:               wf,
		runtime:          runtime,
		now:              now,
	}
}

func (s *Service) Shutdown(force context.Context) {
	if s == nil || s.runtime == nil {
		return
	}
	s.runtime.Close()
}

//nolint:unused
var db = sqldb.NewDatabase("fees", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

func temporalRuntimeConfig() workflowpkg.RuntimeConfig {
	cfg := workflowpkg.RuntimeConfig{
		TaskQueue: workflowpkg.DefaultTaskQueue,
	}
	if hostPort := os.Getenv("FEES_TEMPORAL_HOSTPORT"); hostPort != "" {
		cfg.HostPort = hostPort
	}
	if namespace := os.Getenv("FEES_TEMPORAL_NAMESPACE"); namespace != "" {
		cfg.Namespace = namespace
	}
	if taskQueue := os.Getenv("FEES_TEMPORAL_TASK_QUEUE"); taskQueue != "" {
		cfg.TaskQueue = taskQueue
	}
	return cfg
}
