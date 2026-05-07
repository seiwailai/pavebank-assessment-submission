package workflow

import (
	"context"
	"errors"

	enumspb "go.temporal.io/api/enums/v1"
	temporalclient "go.temporal.io/sdk/client"
)

type TemporalWorkflowClient struct {
	client    temporalclient.Client
	taskQueue string
}

func NewTemporalClient(client temporalclient.Client, taskQueue string) *TemporalWorkflowClient {
	if taskQueue == "" {
		taskQueue = DefaultTaskQueue
	}
	return &TemporalWorkflowClient{
		client:    client,
		taskQueue: taskQueue,
	}
}

func (c *TemporalWorkflowClient) StartBillWorkflow(ctx context.Context, input StartBillWorkflowInput) error {
	if c == nil || c.client == nil {
		return errors.New("temporal workflow client is not configured")
	}

	_, err := c.client.ExecuteWorkflow(ctx, temporalclient.StartWorkflowOptions{
		ID:                    WorkflowID(input.BillID),
		TaskQueue:             c.taskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, BillWorkflow, input)
	return err
}

func (c *TemporalWorkflowClient) AddLineItems(ctx context.Context, billID string, input AddLineItemsInput) (*AddLineItemsResult, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("temporal workflow client is not configured")
	}

	handle, err := c.client.UpdateWorkflow(ctx, temporalclient.UpdateWorkflowOptions{
		WorkflowID:  WorkflowID(billID),
		UpdateName:  UpdateNameAddLineItems,
		Args:        []interface{}{input},
		WaitForStage: temporalclient.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return nil, err
	}

	var result AddLineItemsResult
	if err := handle.Get(ctx, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *TemporalWorkflowClient) CloseBill(ctx context.Context, billID string, input CloseBillInput) (*CloseBillResult, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("temporal workflow client is not configured")
	}

	handle, err := c.client.UpdateWorkflow(ctx, temporalclient.UpdateWorkflowOptions{
		WorkflowID:  WorkflowID(billID),
		UpdateName:  UpdateNameCloseBill,
		Args:        []interface{}{input},
		WaitForStage: temporalclient.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return nil, err
	}

	var result CloseBillResult
	if err := handle.Get(ctx, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *TemporalWorkflowClient) GetState(ctx context.Context, billID string) (*State, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("temporal workflow client is not configured")
	}

	resp, err := c.client.QueryWorkflow(ctx, WorkflowID(billID), "", QueryNameState)
	if err != nil {
		return nil, err
	}

	var state State
	if err := resp.Get(&state); err != nil {
		return nil, err
	}
	return &state, nil
}
