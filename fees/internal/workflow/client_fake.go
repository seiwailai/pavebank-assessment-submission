package workflow

import (
	"context"
	"sync"

	"encore.app/fees/internal/domain"
)

type FakeClient struct {
	mu sync.Mutex

	StartedBills  []StartBillWorkflowInput
	AddRequests   []FakeAddRequest
	CloseRequests []FakeCloseRequest

	StartErr    error
	AddErr      error
	CloseErr    error
	AddResult   *AddLineItemsResult
	CloseResult *CloseBillResult
	StateResult *State
	StateErr    error
}

type FakeAddRequest struct {
	BillID string
	Input  AddLineItemsInput
}

type FakeCloseRequest struct {
	BillID string
	Input  CloseBillInput
}

func NewFakeClient() *FakeClient {
	return &FakeClient{}
}

func (c *FakeClient) StartBillWorkflow(_ context.Context, input StartBillWorkflowInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.StartedBills = append(c.StartedBills, input)
	return c.StartErr
}

func (c *FakeClient) AddLineItems(_ context.Context, billID string, input AddLineItemsInput) (*AddLineItemsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.AddRequests = append(c.AddRequests, FakeAddRequest{
		BillID: billID,
		Input:  input,
	})
	if c.AddErr != nil {
		return nil, c.AddErr
	}
	if c.AddResult == nil {
		return &AddLineItemsResult{
			BillID:                   billID,
			BillStatus:               domain.BillStatusOpen,
			SnapshotTotalAmountMinor: 0,
			SuccessLineItemIDsMap:    map[string]string{},
			FailedLineItemReasonsMap: map[string]string{},
		}, nil
	}

	result := *c.AddResult
	return &result, nil
}

func (c *FakeClient) CloseBill(_ context.Context, billID string, input CloseBillInput) (*CloseBillResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.CloseRequests = append(c.CloseRequests, FakeCloseRequest{
		BillID: billID,
		Input:  input,
	})
	if c.CloseErr != nil {
		return nil, c.CloseErr
	}
	if c.CloseResult == nil {
		return &CloseBillResult{
			BillID:                   billID,
			BillStatus:               domain.BillStatusOpen,
			SnapshotTotalAmountMinor: 0,
		}, nil
	}

	result := *c.CloseResult
	return &result, nil
}

func (c *FakeClient) GetState(_ context.Context, billID string) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.StateErr != nil {
		return nil, c.StateErr
	}
	if c.StateResult == nil {
		return &State{
			BillID:                   billID,
			Status:                   domain.BillStatusOpen,
			SnapshotTotalAmountMinor: 0,
		}, nil
	}

	state := *c.StateResult
	if state.BillID == "" {
		state.BillID = billID
	}
	return &state, nil
}
