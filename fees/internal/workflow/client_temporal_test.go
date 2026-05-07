package workflow

import (
	"context"
	"testing"
)

func TestNewTemporalClientDefaultsTaskQueue(t *testing.T) {
	t.Parallel()

	client := NewTemporalClient(nil, "")
	if client.taskQueue != DefaultTaskQueue {
		t.Fatalf("taskQueue = %q, want %q", client.taskQueue, DefaultTaskQueue)
	}
}

func TestTemporalWorkflowClientNilClientGuards(t *testing.T) {
	t.Parallel()

	var nilClient *TemporalWorkflowClient
	if err := nilClient.StartBillWorkflow(context.Background(), StartBillWorkflowInput{}); err == nil {
		t.Fatal("expected StartBillWorkflow to fail without client")
	}
	if _, err := nilClient.AddLineItems(context.Background(), "bill-1", AddLineItemsInput{}); err == nil {
		t.Fatal("expected AddLineItems to fail without client")
	}
	if _, err := nilClient.CloseBill(context.Background(), "bill-1", CloseBillInput{}); err == nil {
		t.Fatal("expected CloseBill to fail without client")
	}
	if _, err := nilClient.GetState(context.Background(), "bill-1"); err == nil {
		t.Fatal("expected GetState to fail without client")
	}

	client := NewTemporalClient(nil, "custom")
	if err := client.StartBillWorkflow(context.Background(), StartBillWorkflowInput{}); err == nil {
		t.Fatal("expected StartBillWorkflow to fail without configured client")
	}
}
