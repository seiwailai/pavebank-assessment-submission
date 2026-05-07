package workflow

import "context"

type Client interface {
	StartBillWorkflow(ctx context.Context, input StartBillWorkflowInput) error
	AddLineItems(ctx context.Context, billID string, input AddLineItemsInput) (*AddLineItemsResult, error)
	CloseBill(ctx context.Context, billID string, input CloseBillInput) (*CloseBillResult, error)
	GetState(ctx context.Context, billID string) (*State, error)
}
