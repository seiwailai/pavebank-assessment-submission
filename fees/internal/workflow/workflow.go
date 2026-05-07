package workflow

import (
	"errors"
	"fmt"
	"time"

	"encore.app/fees/internal/domain"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	ErrBillNotAcceptingLineItems = errors.New("bill is not accepting new line items")
	ErrBillClosingInProgress     = errors.New("bill closing in progress")
	ErrBillInvalidState          = errors.New("bill invalid state")
)

const (
	billNotAcceptingLineItemsErrorType = "BillNotAcceptingLineItemsError"
	billClosingInProgressErrorType     = "BillClosingInProgressError"
	billInvalidStateErrorType          = "BillInvalidStateError"
)

type billImpl struct {
	// bill related info
	id                          string
	currencyCode                domain.CurrencyCode
	billingPeriodStartAt        time.Time
	lineItemsSubmissionDeadline time.Time
	snapshotTotalAmountMinor    int64

	// workflow related info
	lineItemsPersisted     workflow.Channel // every time there's items being persist, we check if there's need to continueAsNew
	closeSignal            workflow.Channel
	status                 domain.BillStatus
	closeRequested         bool  // to indicate if there's close signal
	activeUpdateCounts     int64 // indicate if there's still active update / signals pending process
	continueAsNewSuggested bool  // indicate if suggested to continueAsNew
}

func newBillWf(ctx workflow.Context, input StartBillWorkflowInput) *billImpl {
	return &billImpl{
		id:                          input.BillID,
		currencyCode:                input.CurrencyCode,
		status:                      input.InitialStatus,
		billingPeriodStartAt:        input.BillingPeriodStartAt,
		lineItemsSubmissionDeadline: input.LineItemsSubmissionDeadline,
		snapshotTotalAmountMinor:    input.SnapshotTotalAmountMinor,
		lineItemsPersisted:          workflow.NewChannel(ctx),
		closeSignal:                 workflow.NewChannel(ctx),
	}
}

func (billWf *billImpl) setup(ctx workflow.Context, input StartBillWorkflowInput) error {
	if err := workflow.SetQueryHandler(ctx, QueryNameState, func() (State, error) {
		return State{
			BillID:                      billWf.id,
			CurrencyCode:                billWf.currencyCode,
			Status:                      billWf.status,
			BillingPeriodStartAt:        billWf.billingPeriodStartAt,
			LineItemsSubmissionDeadline: billWf.lineItemsSubmissionDeadline,
			SnapshotTotalAmountMinor:    billWf.snapshotTotalAmountMinor,
		}, nil
	}); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateNameAddLineItems,
		func(updateCtx workflow.Context, input AddLineItemsInput) (*AddLineItemsResult, error) {
			billWf.activeUpdateCounts++
			defer func() {
				billWf.activeUpdateCounts--
			}()

			var activityResult PersistLineItemsActivityResult
			err := workflow.ExecuteActivity(withPersistLineItemsActivityOptions(ctx), ActivityNamePersistLineItems, PersistLineItemsActivityInput{
				BillID:         billWf.id,
				IdempotencyKey: input.IdempotencyKey,
				CurrencyCode:   input.CurrencyCode,
				LineItems:      input.LineItems,
			}).Get(updateCtx, &activityResult)
			if err != nil {
				return nil, err
			}
			if len(activityResult.FailedLineItemReasonsMap) > 0 {
				return nil, temporal.NewNonRetryableApplicationError(
					"line items batch rejected",
					"LineItemsBatchFailureError",
					nil,
					LineItemsBatchFailureError{RecordReasons: activityResult.FailedLineItemReasonsMap},
				)
			}

			billWf.lineItemsPersisted.SendAsync(struct{}{})
			billWf.snapshotTotalAmountMinor += activityResult.AmountDeltaMinor
			return &AddLineItemsResult{
				BillID:                   billWf.id,
				BillStatus:               billWf.status,
				SnapshotTotalAmountMinor: billWf.snapshotTotalAmountMinor,
				SuccessLineItemIDsMap:    activityResult.SuccessLineItemIDsMap,
				FailedLineItemReasonsMap: activityResult.FailedLineItemReasonsMap,
			}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(input AddLineItemsInput) error {
				if input.BillID != billWf.id {
					return invalidBillIdentityErr(input.BillID, billWf.id)
				}
				if input.CurrencyCode != billWf.currencyCode {
					return invalidCurrencyErr(input.CurrencyCode, billWf.currencyCode)
				}
				if billWf.status != domain.BillStatusOpen || billWf.closeRequested ||
					billWf.continueAsNewSuggested {
					return temporal.NewNonRetryableApplicationError(
						ErrBillNotAcceptingLineItems.Error(),
						billNotAcceptingLineItemsErrorType,
						nil,
					)
				}
				return nil
			},
		},
	); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		ctx,
		UpdateNameCloseBill,
		func(updateCtx workflow.Context, input CloseBillInput) (*CloseBillResult, error) {
			billWf.activeUpdateCounts++
			defer func() {
				billWf.activeUpdateCounts--
			}()

			billWf.closeSignal.SendAsync(struct{}{})
			// return snapshot first
			return &CloseBillResult{
				BillID:                   billWf.id,
				BillStatus:               billWf.status,
				SnapshotTotalAmountMinor: billWf.snapshotTotalAmountMinor,
			}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(CloseBillInput) error {
				if billWf.status != domain.BillStatusOpen {
					return temporal.NewNonRetryableApplicationError(
						ErrBillInvalidState.Error(),
						billInvalidStateErrorType,
						nil,
					)
				}
				if billWf.closeRequested {
					return temporal.NewNonRetryableApplicationError(
						ErrBillClosingInProgress.Error(),
						billClosingInProgressErrorType,
						nil,
					)
				}
				return nil
			},
		},
	); err != nil {
		return err
	}

	return nil
}

func (billWf *billImpl) run(ctx workflow.Context) (res *BillWorkflowResult, err error) {
	if err := billWf.handleScheduledState(ctx); err != nil {
		return nil, err
	}

	if err := billWf.handleOpenState(ctx); err != nil {
		return nil, err
	}

	return billWf.handleClosingState(ctx)
}

func (billWf *billImpl) waitForDrainingUpdatesAndSignals(ctx workflow.Context) error {
	return workflow.Await(ctx, func() bool {
		return billWf.activeUpdateCounts == 0 && billWf.lineItemsPersisted.Len() == 0 && billWf.closeSignal.Len() == 0
	})
}

func (billWf *billImpl) handleWorkflowError(ctx workflow.Context, err error) (*BillWorkflowResult, error) {
	if err == nil {
		return nil, nil
	}
	if workflow.IsContinueAsNewError(err) {
		return nil, err
	}

	failureReason := err.Error()
	markErr := workflow.ExecuteActivity(withMarkBillFailedActivityOptions(ctx), ActivityNameMarkBillFailed, MarkBillFailedActivityInput{
		BillID:                   billWf.id,
		FailureReason:            failureReason,
		SnapshotTotalAmountMinor: billWf.snapshotTotalAmountMinor,
	}).Get(ctx, nil)
	if markErr != nil {
		return nil, fmt.Errorf("finalize bill: %w (additionally failed to mark bill failed: %v)", err, markErr)
	}

	billWf.status = domain.BillStatusFailed
	return &BillWorkflowResult{
		BillID:           billWf.id,
		BillStatus:       billWf.status,
		TotalAmountMinor: billWf.snapshotTotalAmountMinor,
		FailureReason:    failureReason,
	}, nil
}

func (billWf *billImpl) handleClosingState(ctx workflow.Context) (*BillWorkflowResult, error) {
	if billWf.status == domain.BillStatusOpen && !billWf.closeRequested {
		return nil, nil
	}
	if billWf.status != domain.BillStatusOpen && billWf.status != domain.BillStatusFinalizing {
		return nil, nil
	}

	if billWf.status == domain.BillStatusOpen {
		if err := workflow.ExecuteActivity(withStatusTransitionActivityOptions(ctx), ActivityNameTransitionBillStatus, TransitionBillStatusActivityInput{
			BillID: billWf.id,
			Status: domain.BillStatusFinalizing,
		}).Get(ctx, nil); err != nil {
			return nil, err
		}
		billWf.status = domain.BillStatusFinalizing
	}

	if err := billWf.waitForDrainingUpdatesAndSignals(ctx); err != nil {
		return nil, err
	}

	// Finalize total amount minor and then close bill
	var activityResult FinalizeBillActivityResult
	if err := workflow.ExecuteActivity(withFinalizeBillActivityOptions(ctx), ActivityNameFinalizeBill, FinalizeBillActivityInput{
		BillID: billWf.id,
	}).Get(ctx, &activityResult); err != nil {
		return nil, err
	}

	billWf.status = activityResult.BillStatus
	billWf.snapshotTotalAmountMinor = activityResult.TotalAmountMinor
	return &BillWorkflowResult{
		BillID:           billWf.id,
		BillStatus:       billWf.status,
		TotalAmountMinor: billWf.snapshotTotalAmountMinor,
		FailureReason:    activityResult.FailureReason,
	}, nil
}

func (billWf *billImpl) handleOpenState(ctx workflow.Context) error {
	// 1. Transition to open state
	if billWf.status == domain.BillStatusScheduled {
		if err := workflow.ExecuteActivity(withStatusTransitionActivityOptions(ctx), ActivityNameTransitionBillStatus, TransitionBillStatusActivityInput{
			BillID: billWf.id,
			Status: domain.BillStatusOpen,
		}).Get(ctx, nil); err != nil {
			return err
		}
		billWf.status = domain.BillStatusOpen
	}

	if billWf.status != domain.BillStatusOpen {
		return nil
	}

	// 2. Create the Timer Future
	deadlineTimeLeft := billWf.lineItemsSubmissionDeadline.Sub(workflow.Now(ctx)).Seconds()
	if deadlineTimeLeft <= 0 {
		// deadline reach, can proceed to trigger timer immediately and start closing and draining
		deadlineTimeLeft = 0
	}
	deadlineTimer := workflow.NewTimer(ctx, time.Second*time.Duration(deadlineTimeLeft))

	for {
		// 3. check exit signal before proceeding to other logic
		// If timer fired or Close Signal received, try to drain and exit
		if billWf.closeRequested && billWf.waitForDrainingUpdatesAndSignals(ctx) == nil {
			return nil // Workflow Ends
		}

		// 4. New selector everytime
		var continueAsNewErr error
		selector := workflow.NewSelector(ctx)

		// 5. handle deadline
		selector.AddFuture(deadlineTimer, func(f workflow.Future) {
			billWf.closeRequested = true
		})

		selector.AddReceive(billWf.closeSignal, func(c workflow.ReceiveChannel, _ bool) {
			var ignore struct{}
			c.Receive(ctx, &ignore)
			billWf.closeRequested = true
		})

		// 6. everytime there's line items being persisted, check if there's need to continueAsNew
		// to avoid history bloat
		selector.AddReceive(billWf.lineItemsPersisted, func(c workflow.ReceiveChannel, _ bool) {
			var ignored struct{}
			c.Receive(ctx, &ignored)
			billWf.continueAsNewSuggested = workflow.GetInfo(ctx).GetContinueAsNewSuggested()
			if billWf.continueAsNewSuggested && !billWf.closeRequested && billWf.waitForDrainingUpdatesAndSignals(ctx) == nil {
				continueAsNewErr = workflow.NewContinueAsNewError(ctx, BillWorkflow, billWf.exportState())
			}
		})

		// 7. block until any of the events happen
		selector.Select(ctx)
		if continueAsNewErr != nil {
			return continueAsNewErr
		}
	}
}

func (billWf *billImpl) handleScheduledState(ctx workflow.Context) error {
	if billWf.status != domain.BillStatusScheduled {
		return nil
	}

	if billWf.billingPeriodStartAt.After(workflow.Now(ctx)) {
		return workflow.Sleep(ctx, billWf.billingPeriodStartAt.Sub(workflow.Now(ctx)))
	}

	return nil
}

func (billWf *billImpl) exportState() StartBillWorkflowInput {
	return StartBillWorkflowInput{
		BillID:                      billWf.id,
		CurrencyCode:                billWf.currencyCode,
		InitialStatus:               billWf.status,
		BillingPeriodStartAt:        billWf.billingPeriodStartAt,
		LineItemsSubmissionDeadline: billWf.lineItemsSubmissionDeadline,
		SnapshotTotalAmountMinor:    billWf.snapshotTotalAmountMinor,
		CloseRequested:              billWf.closeRequested,
	}
}

func BillWorkflow(ctx workflow.Context, input StartBillWorkflowInput) (*BillWorkflowResult, error) {
	billWf := newBillWf(ctx, input)
	if err := billWf.setup(ctx, input); err != nil {
		return billWf.handleWorkflowError(ctx, err)
	}

	res, err := billWf.run(ctx)
	if err != nil {
		return billWf.handleWorkflowError(ctx, err)
	}

	return res, nil
}

func withStatusTransitionActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    15 * time.Second,
		ScheduleToCloseTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    200 * time.Millisecond,
			BackoffCoefficient: 2,
			MaximumInterval:    2 * time.Second,
			MaximumAttempts:    3,
		},
	})
}

func withPersistLineItemsActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    30 * time.Second,
		ScheduleToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    200 * time.Millisecond,
			BackoffCoefficient: 2,
			MaximumInterval:    2 * time.Second,
			MaximumAttempts:    3,
		},
	})
}

func withFinalizeBillActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    time.Minute,
		ScheduleToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    200 * time.Millisecond,
			BackoffCoefficient: 2,
			MaximumInterval:    2 * time.Second,
			MaximumAttempts:    3,
		},
	})
}

func withMarkBillFailedActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    15 * time.Second,
		ScheduleToCloseTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    200 * time.Millisecond,
			BackoffCoefficient: 2,
			MaximumInterval:    2 * time.Second,
			MaximumAttempts:    3,
		},
	})
}

func invalidBillIdentityErr(receivedBillID, expectedBillID string) error {
	return fmt.Errorf("bill_id %q does not match workflow bill %q", receivedBillID, expectedBillID)
}

func invalidCurrencyErr(received, expected domain.CurrencyCode) error {
	return fmt.Errorf("currency_code %q does not match workflow currency %q", received, expected)
}
