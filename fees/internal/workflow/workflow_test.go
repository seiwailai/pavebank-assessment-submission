package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"encore.app/fees/internal/domain"
	billingstore "encore.app/fees/internal/store/billing"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

func newWorkflowTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerWorkflowActivities(env, &Activities{Store: &workflowTestStore{
		persistLineItems: func(_ context.Context, input PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			return &PersistLineItemsActivityResult{
				AmountDeltaMinor:         125,
				SuccessLineItemIDsMap:    map[string]string{"line-001": "line-item-001"},
				FailedLineItemReasonsMap: map[string]string{},
			}, nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return &FinalizeBillActivityResult{
				BillStatus:       domain.BillStatusClosed,
				TotalAmountMinor: 125,
			}, nil
		},
	}})
	return env
}

func TestBillImplExportStateCarriesForwardWorkflowState(t *testing.T) {
	t.Parallel()

	baseTime := time.Now().UTC()
	billWf := &billImpl{
		id:                          "bill-1",
		currencyCode:                domain.USD(),
		status:                      domain.BillStatusFinalizing,
		billingPeriodStartAt:        baseTime,
		lineItemsSubmissionDeadline: baseTime.Add(2 * time.Hour),
		snapshotTotalAmountMinor:    125,
	}
	exported := billWf.exportState()
	require.Equal(t, "bill-1", exported.BillID)
	require.Equal(t, domain.USD(), exported.CurrencyCode)
	require.Equal(t, domain.BillStatusFinalizing, exported.InitialStatus)
	require.True(t, exported.BillingPeriodStartAt.Equal(baseTime))
	require.True(t, exported.LineItemsSubmissionDeadline.Equal(baseTime.Add(2*time.Hour)))
	require.Equal(t, int64(125), exported.SnapshotTotalAmountMinor)
}

func TestBillWorkflow_RejectsAddWhenBillIdentityDoesNotMatchState(t *testing.T) {
	t.Parallel()

	env := newWorkflowTestEnv(t)
	baseTime := time.Now().UTC()

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameAddLineItems, "add-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "does not match workflow bill")
			},
			OnAccept: func() {
				t.Fatal("add update accepted unexpectedly")
			},
			OnComplete: func(interface{}, error) {},
		}, AddLineItemsInput{
			BillID:       "bill-2",
			CurrencyCode: domain.USD(),
			LineItems: []domain.AddLineItemInput{
				{
					ExternalReferenceID: "line-001",
					OccurredAt:          baseTime.Add(5 * time.Minute),
					AmountMinor:         125,
				},
			},
		})
	}, 0)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "bill-1",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(time.Hour),
		SnapshotTotalAmountMinor:    0,
	})

	require.NoError(t, env.GetWorkflowError())
}

func TestBillWorkflow_RejectsAddWhenCurrencyDoesNotMatchState(t *testing.T) {
	t.Parallel()

	env := newWorkflowTestEnv(t)
	baseTime := time.Now().UTC()

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameAddLineItems, "add-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "does not match workflow currency")
			},
			OnAccept: func() {
				t.Fatal("add update accepted unexpectedly")
			},
			OnComplete: func(interface{}, error) {},
		}, AddLineItemsInput{
			BillID:       "bill-1",
			CurrencyCode: domain.GEL(),
			LineItems: []domain.AddLineItemInput{
				{
					ExternalReferenceID: "line-001",
					OccurredAt:          baseTime.Add(5 * time.Minute),
					AmountMinor:         125,
				},
			},
		})
	}, 0)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "bill-1",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(time.Hour),
		SnapshotTotalAmountMinor:    0,
	})

	require.NoError(t, env.GetWorkflowError())
}

func TestBillWorkflow_RejectsCloseWhenBillIsNotOpen(t *testing.T) {
	t.Parallel()

	env := newWorkflowTestEnv(t)

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameCloseBill, "close-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				var appErr *temporal.ApplicationError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, "BillInvalidStateError", appErr.Type())
				require.Equal(t, ErrBillInvalidState.Error(), appErr.Message())
			},
			OnAccept: func() {
				t.Fatal("close update accepted unexpectedly")
			},
			OnComplete: func(interface{}, error) {},
		}, CloseBillInput{})
	}, 0)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                   "11111111-1111-1111-1111-111111111111",
		CurrencyCode:             domain.USD(),
		InitialStatus:            domain.BillStatusFinalizing,
		SnapshotTotalAmountMinor: 125,
	})

	require.NoError(t, env.GetWorkflowError())
}

func TestBillWorkflow_RejectsDuplicateCloseWhileCloseInProgress(t *testing.T) {
	t.Parallel()

	env := newWorkflowTestEnv(t)
	baseTime := time.Now().UTC()

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameCloseBill, "close-update-1", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("first close update rejected: %v", err)
			},
			OnAccept: func() {
				env.UpdateWorkflow(UpdateNameCloseBill, "close-update-2", &testsuite.TestUpdateCallback{
					OnReject: func(err error) {
						var appErr *temporal.ApplicationError
						require.ErrorAs(t, err, &appErr)
						require.Equal(t, "BillClosingInProgressError", appErr.Type())
						require.Equal(t, ErrBillClosingInProgress.Error(), appErr.Message())
					},
					OnAccept: func() {
						t.Fatal("second close update accepted unexpectedly")
					},
					OnComplete: func(interface{}, error) {},
				}, CloseBillInput{})
			},
			OnComplete: func(interface{}, error) {},
		}, CloseBillInput{})
	}, 2)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "11111111-1111-1111-1111-111111111111",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(time.Hour),
	})

	require.NoError(t, env.GetWorkflowError())
}

func TestWorkflowTransitionsToClosedFromFinalizingState(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	store := &workflowTestStore{
		updateBill: func(_ context.Context, params billingstore.UpdateBillParams) error {
			if params.Status == nil {
				t.Fatal("expected status update during transition")
			}
			return nil
		},
		persistLineItems: func(context.Context, PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			return &PersistLineItemsActivityResult{
				AmountDeltaMinor:         125,
				SuccessLineItemIDsMap:    map[string]string{"line-001": "line-item-001"},
				FailedLineItemReasonsMap: map[string]string{},
			}, nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return &FinalizeBillActivityResult{
				BillStatus:       domain.BillStatusClosed,
				TotalAmountMinor: 125,
			}, nil
		},
	}

	registerWorkflowActivities(env, &Activities{Store: store})

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                   "11111111-1111-1111-1111-111111111111",
		CurrencyCode:             domain.USD(),
		InitialStatus:            domain.BillStatusFinalizing,
		SnapshotTotalAmountMinor: 125,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result BillWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.BillStatus != domain.BillStatusClosed {
		t.Fatalf("workflow status = %q, want %q", result.BillStatus, domain.BillStatusClosed)
	}
	if result.TotalAmountMinor != 125 {
		t.Fatalf("workflow total = %d, want %d", result.TotalAmountMinor, 125)
	}
}

func TestWorkflowTransitionsScheduledToOpenAndClosesOnDeadline(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	baseTime := time.Now().UTC()
	var statuses []domain.BillStatus

	store := &workflowTestStore{
		updateBill: func(_ context.Context, params billingstore.UpdateBillParams) error {
			if params.Status != nil {
				statuses = append(statuses, *params.Status)
			}
			return nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return &FinalizeBillActivityResult{
				BillStatus:       domain.BillStatusClosed,
				TotalAmountMinor: 0,
			}, nil
		},
	}
	registerWorkflowActivities(env, &Activities{Store: store})

	env.SetStartTime(baseTime)
	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "55555555-5555-4555-8555-555555555555",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusScheduled,
		BillingPeriodStartAt:        baseTime.Add(time.Minute),
		LineItemsSubmissionDeadline: baseTime.Add(2 * time.Minute),
	})

	require.NoError(t, env.GetWorkflowError())
	var result BillWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, domain.BillStatusClosed, result.BillStatus)
	require.Equal(t, []domain.BillStatus{domain.BillStatusOpen, domain.BillStatusFinalizing}, statuses)
}

func TestWorkflowContinuesAsNewWithCarriedStateWhenSuggested(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	baseTime := time.Now().UTC()
	addCompleted := make(chan *AddLineItemsResult, 1)

	registerWorkflowActivities(env, &Activities{Store: &workflowTestStore{
		persistLineItems: func(_ context.Context, input PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			env.SetCurrentHistoryLength(1)
			env.SetCurrentHistorySize(1)
			env.SetContinueAsNewSuggested(true)
			return &PersistLineItemsActivityResult{
				AmountDeltaMinor:         125,
				SuccessLineItemIDsMap:    map[string]string{input.LineItems[0].ExternalReferenceID: "line-item-" + input.LineItems[0].ExternalReferenceID},
				FailedLineItemReasonsMap: map[string]string{},
			}, nil
		},
	}})
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameAddLineItems, "add-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("add update rejected: %v", err)
			},
			OnAccept: func() {},
			OnComplete: func(result interface{}, err error) {
				if err != nil {
					t.Fatalf("add update completed with error: %v", err)
				}
				addResult, ok := result.(*AddLineItemsResult)
				if !ok {
					t.Fatalf("add update result type = %T, want *AddLineItemsResult", result)
				}
				addCompleted <- addResult
			},
		}, AddLineItemsInput{
			BillID:         "bill-continue-as-new",
			IdempotencyKey: "idem-continue-as-new",
			CurrencyCode:   domain.USD(),
			LineItems: []domain.AddLineItemInput{{
				ExternalReferenceID: "line-001",
				OccurredAt:          baseTime.Add(5 * time.Minute),
				AmountMinor:         125,
			}},
		})
	}, 0)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "bill-continue-as-new",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(24 * time.Hour),
	})

	var addResult *AddLineItemsResult
	select {
	case addResult = <-addCompleted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for add update completion")
	}
	require.Equal(t, int64(125), addResult.SnapshotTotalAmountMinor)

	err := env.GetWorkflowError()
	require.Error(t, err)

	var continueAsNewErr *temporalworkflow.ContinueAsNewError
	require.ErrorAs(t, err, &continueAsNewErr)
	require.NotNil(t, continueAsNewErr.WorkflowType)
	require.Equal(t, "BillWorkflow", continueAsNewErr.WorkflowType.Name)

	var carriedInput StartBillWorkflowInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(continueAsNewErr.Input, &carriedInput))
	require.Equal(t, "bill-continue-as-new", carriedInput.BillID)
	require.Equal(t, domain.USD(), carriedInput.CurrencyCode)
	require.Equal(t, domain.BillStatusOpen, carriedInput.InitialStatus)
	require.True(t, carriedInput.BillingPeriodStartAt.Equal(baseTime))
	require.True(t, carriedInput.LineItemsSubmissionDeadline.Equal(baseTime.Add(24*time.Hour)))
	require.Equal(t, int64(125), carriedInput.SnapshotTotalAmountMinor)
	require.False(t, carriedInput.CloseRequested)
}

func TestWorkflowRejectsLateAddAfterFinalizing(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	baseTime := time.Now().UTC()

	releaseFinalize := make(chan struct{})
	lateAddRejected := make(chan error, 1)
	store := &workflowTestStore{
		updateBill: func(_ context.Context, params billingstore.UpdateBillParams) error {
			if params.Status == nil {
				t.Fatal("expected status update during transition")
			}
			return nil
		},
		persistLineItems: func(context.Context, PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			return &PersistLineItemsActivityResult{}, nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			select {
			case <-releaseFinalize:
				return &FinalizeBillActivityResult{
					BillStatus:       domain.BillStatusClosed,
					TotalAmountMinor: 0,
				}, nil
			case <-time.After(2 * time.Second):
				return nil, errors.New("late add was not rejected before finalization completed")
			}
		},
	}

	registerWorkflowActivities(env, &Activities{Store: store})
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameCloseBill, "close-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("close update rejected: %v", err)
			},
			OnAccept: func() {
				env.UpdateWorkflow(UpdateNameAddLineItems, "late-add", &testsuite.TestUpdateCallback{
					OnReject: func(err error) {
						lateAddRejected <- err
						close(releaseFinalize)
					},
					OnAccept: func() {
						lateAddRejected <- errors.New("late add was accepted unexpectedly")
						close(releaseFinalize)
					},
					OnComplete: func(interface{}, error) {},
				}, AddLineItemsInput{
					BillID:       "22222222-2222-2222-2222-222222222222",
					CurrencyCode: domain.USD(),
					LineItems: []domain.AddLineItemInput{
						{
							ExternalReferenceID: "line-late",
							OccurredAt:          baseTime.Add(5 * time.Minute),
							AmountMinor:         100,
						},
					},
				})
			},
			OnComplete: func(result interface{}, err error) {
				if err != nil {
					t.Fatalf("close update completed with error: %v", err)
				}

				closeResult, ok := result.(*CloseBillResult)
				if !ok {
					t.Fatalf("close update result type = %T, want *CloseBillResult", result)
				}
				if closeResult.BillStatus != domain.BillStatusOpen {
					t.Fatalf("close update status = %q, want %q", closeResult.BillStatus, domain.BillStatusOpen)
				}
			},
		}, CloseBillInput{})
	}, 2)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "22222222-2222-2222-2222-222222222222",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		LineItemsSubmissionDeadline: baseTime.Add(time.Hour),
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	rejectedErr := <-lateAddRejected
	if rejectedErr == nil {
		t.Fatal("expected late add rejection error")
	}
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, rejectedErr, &appErr)
	require.Equal(t, "BillNotAcceptingLineItemsError", appErr.Type())
	require.Equal(t, ErrBillNotAcceptingLineItems.Error(), appErr.Message())
}

func TestWorkflowQueryReturnsUpdatedSnapshotForOpenBill(t *testing.T) {
	t.Parallel()

	env := newWorkflowTestEnv(t)
	baseTime := time.Now().UTC()
	queried := make(chan State, 1)
	addCompleted := make(chan struct{}, 1)

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameAddLineItems, "add-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("add update rejected: %v", err)
			},
			OnAccept: func() {},
			OnComplete: func(result interface{}, err error) {
				if err != nil {
					t.Fatalf("add update completed with error: %v", err)
				}
				addCompleted <- struct{}{}
			},
		}, AddLineItemsInput{
			BillID:         "bill-query",
			IdempotencyKey: "idem-query",
			CurrencyCode:   domain.USD(),
			LineItems: []domain.AddLineItemInput{{
				ExternalReferenceID: "line-001",
				OccurredAt:          baseTime.Add(5 * time.Minute),
				AmountMinor:         125,
			}},
		})
	}, 0)
	env.RegisterDelayedCallback(func() {
		encoded, queryErr := env.QueryWorkflow(QueryNameState)
		if queryErr != nil {
			t.Fatalf("query workflow error: %v", queryErr)
		}
		var state State
		if err := encoded.Get(&state); err != nil {
			t.Fatalf("decode workflow query error: %v", err)
		}
		queried <- state
	}, time.Minute)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "bill-query",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(24 * time.Hour),
	})

	require.NoError(t, env.GetWorkflowError())
	<-addCompleted
	state := <-queried
	require.Equal(t, "bill-query", state.BillID)
	require.Equal(t, domain.BillStatusOpen, state.Status)
	require.Equal(t, int64(125), state.SnapshotTotalAmountMinor)
}

func TestWorkflowAddLineItemsBatchFailurePreservesSnapshot(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	baseTime := time.Now().UTC()
	queried := make(chan State, 1)
	batchFailureChecked := make(chan struct{}, 1)

	store := &workflowTestStore{
		persistLineItems: func(context.Context, PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			return &PersistLineItemsActivityResult{
				FailedLineItemReasonsMap: map[string]string{"line-001": "duplicate"},
			}, nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return &FinalizeBillActivityResult{
				BillStatus:       domain.BillStatusClosed,
				TotalAmountMinor: 0,
			}, nil
		},
	}
	registerWorkflowActivities(env, &Activities{Store: store})

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameAddLineItems, "add-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("add update rejected unexpectedly: %v", err)
			},
			OnAccept: func() {},
			OnComplete: func(_ interface{}, err error) {
				var appErr *temporal.ApplicationError
				if !errors.As(err, &appErr) {
					t.Fatalf("expected ApplicationError, got %v", err)
				}
				if !strings.Contains(strings.ToLower(appErr.Type()), "lineitemsbatchfailure") {
					t.Fatalf("unexpected application error type: %s", appErr.Type())
				}
				var details LineItemsBatchFailureError
				if err := appErr.Details(&details); err != nil {
					t.Fatalf("failed to decode batch failure details: %v", err)
				}
				require.Equal(t, map[string]string{"line-001": "duplicate"}, details.RecordReasons)
				batchFailureChecked <- struct{}{}
			},
		}, AddLineItemsInput{
			BillID:         "bill-batch-failure",
			IdempotencyKey: "idem-batch-failure",
			CurrencyCode:   domain.USD(),
			LineItems: []domain.AddLineItemInput{{
				ExternalReferenceID: "line-001",
				OccurredAt:          baseTime.Add(5 * time.Minute),
				AmountMinor:         125,
			}},
		})
	}, 0)
	env.RegisterDelayedCallback(func() {
		encoded, queryErr := env.QueryWorkflow(QueryNameState)
		if queryErr != nil {
			t.Fatalf("query workflow error: %v", queryErr)
		}
		var state State
		if err := encoded.Get(&state); err != nil {
			t.Fatalf("decode workflow query error: %v", err)
		}
		queried <- state
	}, time.Minute)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "bill-batch-failure",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(24 * time.Hour),
	})

	require.NoError(t, env.GetWorkflowError())
	<-batchFailureChecked
	state := <-queried
	require.Equal(t, domain.BillStatusOpen, state.Status)
	require.Equal(t, int64(0), state.SnapshotTotalAmountMinor)
}

func TestWorkflowAddLineItemsPropagatesPersistError(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	baseTime := time.Now().UTC()

	store := &workflowTestStore{
		persistLineItems: func(context.Context, PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			return nil, errors.New("persist line items failed")
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return &FinalizeBillActivityResult{
				BillStatus:       domain.BillStatusClosed,
				TotalAmountMinor: 0,
			}, nil
		},
	}
	registerWorkflowActivities(env, &Activities{Store: store})

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameAddLineItems, "add-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("add update rejected unexpectedly: %v", err)
			},
			OnAccept: func() {},
			OnComplete: func(_ interface{}, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "persist line items failed")
			},
		}, AddLineItemsInput{
			BillID:         "bill-persist-error",
			IdempotencyKey: "idem-persist-error",
			CurrencyCode:   domain.USD(),
			LineItems: []domain.AddLineItemInput{{
				ExternalReferenceID: "line-001",
				OccurredAt:          baseTime.Add(5 * time.Minute),
				AmountMinor:         125,
			}},
		})
	}, 0)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "bill-persist-error",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(time.Hour),
	})

	require.NoError(t, env.GetWorkflowError())
}

func TestWorkflowPersistsSnapshotWhenFinalizationFails(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	var failedUpdate billingstore.UpdateBillParams
	store := &workflowTestStore{
		updateBill: func(_ context.Context, params billingstore.UpdateBillParams) error {
			if params.Status != nil && *params.Status == domain.BillStatusFailed {
				failedUpdate = params
			}
			return nil
		},
		persistLineItems: func(context.Context, PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
			return &PersistLineItemsActivityResult{
				AmountDeltaMinor:         275,
				SuccessLineItemIDsMap:    map[string]string{"line-001": "line-item-001"},
				FailedLineItemReasonsMap: map[string]string{},
			}, nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return nil, errors.New("sum line items failed")
		},
	}

	registerWorkflowActivities(env, &Activities{Store: store})

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                   "33333333-3333-3333-3333-333333333333",
		CurrencyCode:             domain.USD(),
		InitialStatus:            domain.BillStatusFinalizing,
		SnapshotTotalAmountMinor: 275,
	})

	require.NoError(t, env.GetWorkflowError())

	var result BillWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, domain.BillStatusFailed, result.BillStatus)
	require.Equal(t, int64(275), result.TotalAmountMinor)
	require.NotNil(t, failedUpdate.Status)
	require.Equal(t, domain.BillStatusFailed, *failedUpdate.Status)
	require.NotNil(t, failedUpdate.SnapshotTotalAmountMinor)
	require.Equal(t, int64(275), *failedUpdate.SnapshotTotalAmountMinor)
}

func TestWorkflowReturnsErrorWhenMarkBillFailedAlsoFails(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	store := &workflowTestStore{
		updateBill: func(_ context.Context, params billingstore.UpdateBillParams) error {
			if params.Status != nil && *params.Status == domain.BillStatusFailed {
				return errors.New("mark failed write failed")
			}
			return nil
		},
		finalizeBill: func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
			return nil, errors.New("sum line items failed")
		},
	}

	registerWorkflowActivities(env, &Activities{Store: store})
	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                   "66666666-6666-4666-8666-666666666666",
		CurrencyCode:             domain.USD(),
		InitialStatus:            domain.BillStatusFinalizing,
		SnapshotTotalAmountMinor: 275,
	})

	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "sum line items failed")
	require.Contains(t, env.GetWorkflowError().Error(), "mark failed write failed")
}

func TestWorkflowMarksBillFailedWhenTransitionToFinalizingFailsAfterCloseAccepted(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	baseTime := time.Now().UTC()

	store := &workflowTestStore{
		updateBill: func(_ context.Context, params billingstore.UpdateBillParams) error {
			if params.Status != nil && *params.Status == domain.BillStatusFinalizing {
				return errors.New("write FINALIZING failed")
			}
			return nil
		},
	}

	registerWorkflowActivities(env, &Activities{Store: store})
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateNameCloseBill, "close-update", &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				t.Fatalf("close update rejected unexpectedly: %v", err)
			},
			OnAccept: func() {},
			OnComplete: func(result interface{}, err error) {
				if err != nil {
					t.Fatalf("close update completed with error: %v", err)
				}

				closeResult, ok := result.(*CloseBillResult)
				if !ok {
					t.Fatalf("close update result type = %T, want *CloseBillResult", result)
				}
				if closeResult.BillStatus != domain.BillStatusOpen {
					t.Fatalf("close update status = %q, want %q", closeResult.BillStatus, domain.BillStatusOpen)
				}
			},
		}, CloseBillInput{})
	}, 0)

	env.ExecuteWorkflow(BillWorkflow, StartBillWorkflowInput{
		BillID:                      "44444444-4444-4444-4444-444444444444",
		CurrencyCode:                domain.USD(),
		InitialStatus:               domain.BillStatusOpen,
		BillingPeriodStartAt:        baseTime,
		LineItemsSubmissionDeadline: baseTime.Add(time.Hour),
		SnapshotTotalAmountMinor:    0,
	})

	require.NoError(t, env.GetWorkflowError())

	var result BillWorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, domain.BillStatusFailed, result.BillStatus)
	require.Contains(t, result.FailureReason, "write FINALIZING failed")
}

func registerWorkflowActivities(env *testsuite.TestWorkflowEnvironment, activities *Activities) {
	env.RegisterActivityWithOptions(activities.TransitionBillStatus, activity.RegisterOptions{Name: ActivityNameTransitionBillStatus})
	env.RegisterActivityWithOptions(activities.PersistLineItems, activity.RegisterOptions{Name: ActivityNamePersistLineItems})
	env.RegisterActivityWithOptions(activities.FinalizeBill, activity.RegisterOptions{Name: ActivityNameFinalizeBill})
	env.RegisterActivityWithOptions(activities.MarkBillFailed, activity.RegisterOptions{Name: ActivityNameMarkBillFailed})
}

type workflowTestStore struct {
	updateBill       func(context.Context, billingstore.UpdateBillParams) error
	persistLineItems func(context.Context, PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error)
	finalizeBill     func(context.Context, FinalizeBillActivityInput) (*FinalizeBillActivityResult, error)
}

func (s *workflowTestStore) UpdateBill(ctx context.Context, params billingstore.UpdateBillParams) error {
	if s.updateBill == nil {
		return nil
	}
	return s.updateBill(ctx, params)
}

func (s *workflowTestStore) InsertLineItems(ctx context.Context, params billingstore.InsertLineItemsParams) (*billingstore.InsertLineItemsResult, error) {
	if s.persistLineItems == nil {
		return &billingstore.InsertLineItemsResult{}, nil
	}
	lineItems := make([]domain.AddLineItemInput, 0, len(params.LineItems))
	for _, item := range params.LineItems {
		lineItems = append(lineItems, domain.AddLineItemInput{
			ExternalReferenceID: item.ExternalReferenceID,
			OccurredAt:          item.OccurredAt,
			AmountMinor:         item.AmountMinor,
			Description:         item.Description,
		})
	}
	result, err := s.persistLineItems(ctx, PersistLineItemsActivityInput{
		BillID:         params.BillID,
		IdempotencyKey: params.IdempotencyKey,
		CurrencyCode:   params.CurrencyCode,
		LineItems:      lineItems,
	})
	if err != nil {
		return nil, err
	}
	var batchFailure *billingstore.BatchFailure
	if len(result.FailedLineItemReasonsMap) > 0 {
		batchFailure = &billingstore.BatchFailure{RecordReasons: result.FailedLineItemReasonsMap}
	}
	return &billingstore.InsertLineItemsResult{
		SuccessLineItemIDs: result.SuccessLineItemIDsMap,
		BatchFailure:       batchFailure,
		AmountDeltaMinor:   result.AmountDeltaMinor,
	}, nil
}

func (s *workflowTestStore) FinalizeBill(ctx context.Context, billID string) (*billingstore.FinalizeBillResult, error) {
	if s.finalizeBill == nil {
		return &billingstore.FinalizeBillResult{}, nil
	}
	result, err := s.finalizeBill(ctx, FinalizeBillActivityInput{BillID: billID})
	if err != nil {
		return nil, err
	}
	return &billingstore.FinalizeBillResult{
		BillStatus:       result.BillStatus,
		TotalAmountMinor: result.TotalAmountMinor,
		FailureReason:    result.FailureReason,
	}, nil
}
