package workflow

import (
	"context"
	"errors"
	"testing"

	"encore.app/fees/internal/domain"
)

func TestFakeClientRecordsAndReturnsDefaults(t *testing.T) {
	t.Parallel()

	client := NewFakeClient()

	if err := client.StartBillWorkflow(context.Background(), StartBillWorkflowInput{BillID: "bill-1"}); err != nil {
		t.Fatalf("StartBillWorkflow() error = %v", err)
	}
	if len(client.StartedBills) != 1 || client.StartedBills[0].BillID != "bill-1" {
		t.Fatalf("StartedBills = %#v", client.StartedBills)
	}

	addResult, err := client.AddLineItems(context.Background(), "bill-1", AddLineItemsInput{BillID: "bill-1"})
	if err != nil {
		t.Fatalf("AddLineItems() error = %v", err)
	}
	if addResult.BillStatus != domain.BillStatusOpen || addResult.BillID != "bill-1" {
		t.Fatalf("addResult = %#v", addResult)
	}
	if len(client.AddRequests) != 1 || client.AddRequests[0].BillID != "bill-1" {
		t.Fatalf("AddRequests = %#v", client.AddRequests)
	}

	closeResult, err := client.CloseBill(context.Background(), "bill-1", CloseBillInput{})
	if err != nil {
		t.Fatalf("CloseBill() error = %v", err)
	}
	if closeResult.BillStatus != domain.BillStatusOpen || closeResult.BillID != "bill-1" {
		t.Fatalf("closeResult = %#v", closeResult)
	}
	if len(client.CloseRequests) != 1 {
		t.Fatalf("CloseRequests = %#v", client.CloseRequests)
	}

	state, err := client.GetState(context.Background(), "bill-1")
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state.BillID != "bill-1" || state.Status != domain.BillStatusOpen {
		t.Fatalf("state = %#v", state)
	}
}

func TestFakeClientReturnsConfiguredErrorsAndResults(t *testing.T) {
	t.Parallel()

	client := NewFakeClient()
	client.StartErr = errors.New("start failed")
	client.AddErr = errors.New("add failed")
	client.CloseErr = errors.New("close failed")
	client.StateErr = errors.New("state failed")

	if err := client.StartBillWorkflow(context.Background(), StartBillWorkflowInput{}); err == nil {
		t.Fatal("expected configured start error")
	}
	if _, err := client.AddLineItems(context.Background(), "bill-1", AddLineItemsInput{}); err == nil {
		t.Fatal("expected configured add error")
	}
	if _, err := client.CloseBill(context.Background(), "bill-1", CloseBillInput{}); err == nil {
		t.Fatal("expected configured close error")
	}
	if _, err := client.GetState(context.Background(), "bill-1"); err == nil {
		t.Fatal("expected configured state error")
	}

	client.AddErr = nil
	client.CloseErr = nil
	client.StateErr = nil
	client.AddResult = &AddLineItemsResult{BillID: "bill-2", SnapshotTotalAmountMinor: 50}
	client.CloseResult = &CloseBillResult{BillID: "bill-2", BillStatus: domain.BillStatusClosed}
	client.StateResult = &State{Status: domain.BillStatusFinalizing}

	addResult, _ := client.AddLineItems(context.Background(), "bill-2", AddLineItemsInput{})
	if addResult.BillID != "bill-2" || addResult.SnapshotTotalAmountMinor != 50 {
		t.Fatalf("addResult = %#v", addResult)
	}
	closeResult, _ := client.CloseBill(context.Background(), "bill-2", CloseBillInput{})
	if closeResult.BillStatus != domain.BillStatusClosed {
		t.Fatalf("closeResult = %#v", closeResult)
	}
	state, _ := client.GetState(context.Background(), "bill-2")
	if state.BillID != "bill-2" {
		t.Fatalf("state = %#v", state)
	}
}
