package fees

import (
	"testing"

	workflowpkg "encore.app/fees/internal/workflow"
)

func TestTemporalRuntimeConfigDefaults(t *testing.T) {
	t.Setenv("FEES_TEMPORAL_HOSTPORT", "")
	t.Setenv("FEES_TEMPORAL_NAMESPACE", "")
	t.Setenv("FEES_TEMPORAL_TASK_QUEUE", "")

	cfg := temporalRuntimeConfig()
	if cfg.TaskQueue != workflowpkg.DefaultTaskQueue {
		t.Fatalf("TaskQueue = %q, want %q", cfg.TaskQueue, workflowpkg.DefaultTaskQueue)
	}
	if cfg.HostPort != "" {
		t.Fatalf("HostPort = %q, want empty", cfg.HostPort)
	}
	if cfg.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty", cfg.Namespace)
	}
}

func TestTemporalRuntimeConfigUsesEnvOverrides(t *testing.T) {
	t.Setenv("FEES_TEMPORAL_HOSTPORT", "127.0.0.1:8233")
	t.Setenv("FEES_TEMPORAL_NAMESPACE", "uat-default")
	t.Setenv("FEES_TEMPORAL_TASK_QUEUE", "fees-uat")

	cfg := temporalRuntimeConfig()
	if cfg.HostPort != "127.0.0.1:8233" {
		t.Fatalf("HostPort = %q, want %q", cfg.HostPort, "127.0.0.1:8233")
	}
	if cfg.Namespace != "uat-default" {
		t.Fatalf("Namespace = %q, want %q", cfg.Namespace, "uat-default")
	}
	if cfg.TaskQueue != "fees-uat" {
		t.Fatalf("TaskQueue = %q, want %q", cfg.TaskQueue, "fees-uat")
	}
}
