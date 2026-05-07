package fees

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	encore "encore.dev"
	"encore.dev/beta/errs"
	enmiddleware "encore.dev/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	idempotencystore "encore.app/fees/internal/store/idempotency"
	"encore.app/fees/internal/testutil/mocks"
)

func TestIdempotencyMiddlewareReplaysStoredResponse(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveReplay,
		Record: &idempotencystore.Record{
			Response: &idempotencystore.StoredResponse{
				HTTPStatus: 201,
				Body:       json.RawMessage(`{"bill_id":"bill-123"}`),
			},
		},
	}, nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills",
		Headers: http.Header{"Idempotency-Key": []string{"idem-create-001"}},
		Payload: &CreateBillRequest{
			IdempotencyKey:              "idem-create-001",
			AccountID:                   "acct_123",
			ExternalReferenceID:         "bill-001",
			CurrencyCode:                "USD",
			BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
			LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&CreateBillRequest{}),
			ResponseType: reflect.TypeOf(&CreateBillResponse{}),
		},
	})

	handlerCalled := false
	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		handlerCalled = true
		return enmiddleware.Response{
			Payload: &CreateBillResponse{BillID: "new-bill-should-not-run"},
		}
	})

	require.NoError(t, resp.Err)
	assert.False(t, handlerCalled)

	payload, ok := resp.Payload.(*CreateBillResponse)
	require.True(t, ok)
	assert.Equal(t, "bill-123", payload.BillID)
	assert.Equal(t, 201, resp.HTTPStatus)
}

func TestIdempotencyMiddlewareCompletesReplayableErrorResponse(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveAcquired,
	}, nil).Once()
	store.On("Complete", mock.Anything, mock.MatchedBy(func(params idempotencystore.CompleteParams) bool {
		if params.Scope != idempotencystore.Scope("POST:/v1/bills/bill-123/line-items") || params.Key != "idem-add-001" {
			return false
		}
		var body map[string]any
		if err := json.Unmarshal(params.Response.Body, &body); err != nil {
			return false
		}
		return params.Response.HTTPStatus == 409 && body["kind"] == "error"
	})).Return(nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills/bill-123/line-items",
		Headers: http.Header{"Idempotency-Key": []string{"idem-add-001"}},
		Payload: &AddLineItemsRequest{
			IdempotencyKey: "idem-add-001",
			CurrencyCode:   "USD",
			LineItems: []AddLineItemRequest{{
				ExternalReferenceID: "line-001",
				OccurredAt:          time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
				AmountMinor:         125,
			}},
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&AddLineItemsRequest{}),
			ResponseType: reflect.TypeOf(&AddLineItemsResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		return enmiddleware.Response{
			Err: lineItemsBatchFailureError(map[string]string{"line-001": "duplicate"}),
		}
	})

	require.Error(t, resp.Err)
	assert.Equal(t, errs.Aborted, errs.Code(resp.Err))
}

func TestIdempotencyMiddlewareReplaysStoredErrorResponse(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveReplay,
		Record: &idempotencystore.Record{
			Response: &idempotencystore.StoredResponse{
				HTTPStatus: 409,
				Body: json.RawMessage(`{
					"kind":"error",
					"error_code":"aborted",
					"error_message":"line items batch rejected",
					"error_details":{"record_reasons":{"line-001":"duplicate"}}
				}`),
			},
		},
	}, nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills/bill-123/line-items",
		Headers: http.Header{"Idempotency-Key": []string{"idem-add-001"}},
		Payload: &AddLineItemsRequest{
			IdempotencyKey: "idem-add-001",
			CurrencyCode:   "USD",
			LineItems: []AddLineItemRequest{{
				ExternalReferenceID: "line-001",
				OccurredAt:          time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
				AmountMinor:         125,
			}},
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&AddLineItemsRequest{}),
			ResponseType: reflect.TypeOf(&AddLineItemsResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		t.Fatal("handler should not be called on replay")
		return enmiddleware.Response{}
	})

	require.Error(t, resp.Err)
	assert.Equal(t, 409, resp.HTTPStatus)
	assert.Equal(t, errs.Aborted, errs.Code(resp.Err))
}

func TestIdempotencyMiddlewareRejectsMissingKey(t *testing.T) {
	t.Parallel()

	svc := &Service{idempotencyStore: mocks.NewIdempotencyStore(t)}
	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills",
		Headers: http.Header{},
		Payload: &CreateBillRequest{},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&CreateBillRequest{}),
			ResponseType: reflect.TypeOf(&CreateBillResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		t.Fatal("handler should not be called without idempotency key")
		return enmiddleware.Response{}
	})

	require.Error(t, resp.Err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(resp.Err))
}

func TestIdempotencyMiddlewareFallsBackToPayloadHeaderField(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.MatchedBy(func(params idempotencystore.ReserveParams) bool {
		return params.Scope == idempotencystore.Scope("POST:/v1/bills/bill-123/line-items") &&
			params.Key == "idem-add-001"
	})).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveAcquired,
	}, nil).Once()
	store.On("Complete", mock.Anything, mock.MatchedBy(func(params idempotencystore.CompleteParams) bool {
		return params.Scope == idempotencystore.Scope("POST:/v1/bills/bill-123/line-items") &&
			params.Key == "idem-add-001" &&
			params.Response.HTTPStatus == 200
	})).Return(nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills/bill-123/line-items",
		Headers: http.Header{},
		Payload: &AddLineItemsRequest{
			IdempotencyKey: "idem-add-001",
			CurrencyCode:   "USD",
			LineItems: []AddLineItemRequest{{
				ExternalReferenceID: "line-001",
				OccurredAt:          time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
				AmountMinor:         125,
			}},
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&AddLineItemsRequest{}),
			ResponseType: reflect.TypeOf(&AddLineItemsResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		return enmiddleware.Response{
			Payload: &AddLineItemsResponse{
				SuccessLineItemIDsMap:    map[string]string{"line-001": "line-item-001"},
				FailedLineItemReasonsMap: map[string]string{},
				Status:                   200,
			},
		}
	})

	require.NoError(t, resp.Err)
	payload, ok := resp.Payload.(*AddLineItemsResponse)
	require.True(t, ok)
	assert.Equal(t, "line-item-001", payload.SuccessLineItemIDsMap["line-001"])
}

func TestIdempotencyMiddlewareReturnsConflictOutcomes(t *testing.T) {
	t.Parallel()

	for _, outcome := range []idempotencystore.ReserveOutcome{
		idempotencystore.ReserveConflict,
		idempotencystore.ReserveInProgress,
	} {
		outcome := outcome
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()

			store := mocks.NewIdempotencyStore(t)
			store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
				Outcome: outcome,
			}, nil).Once()

			svc := &Service{
				idempotencyStore: store,
				now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
			}

			req := enmiddleware.NewRequest(context.Background(), &encore.Request{
				Method:  http.MethodPost,
				Path:    "/v1/bills",
				Headers: http.Header{"Idempotency-Key": []string{"idem-create-001"}},
				Payload: &CreateBillRequest{IdempotencyKey: "idem-create-001"},
				API: &encore.APIDesc{
					RequestType:  reflect.TypeOf(&CreateBillRequest{}),
					ResponseType: reflect.TypeOf(&CreateBillResponse{}),
				},
			})

			resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
				t.Fatal("handler should not be called for reserve conflicts")
				return enmiddleware.Response{}
			})

			require.Error(t, resp.Err)
			assert.Equal(t, errs.Aborted, errs.Code(resp.Err))
		})
	}
}

func TestIdempotencyMiddlewareDoesNotCompleteNonReplayableErrors(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveAcquired,
	}, nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills",
		Headers: http.Header{"Idempotency-Key": []string{"idem-create-001"}},
		Payload: &CreateBillRequest{IdempotencyKey: "idem-create-001"},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&CreateBillRequest{}),
			ResponseType: reflect.TypeOf(&CreateBillResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		return enmiddleware.Response{
			Err: &errs.Error{Code: errs.Internal, Message: "transient"},
		}
	})

	require.Error(t, resp.Err)
	assert.Equal(t, errs.Internal, errs.Code(resp.Err))
}

func TestIdempotencyMiddlewarePreservesReplayableErrorWhenCompleteFails(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveAcquired,
	}, nil).Once()
	store.On("Complete", mock.Anything, mock.AnythingOfType("idempotency.CompleteParams")).Return(assert.AnError).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills/bill-123/line-items",
		Headers: http.Header{"Idempotency-Key": []string{"idem-add-001"}},
		Payload: &AddLineItemsRequest{
			IdempotencyKey: "idem-add-001",
			CurrencyCode:   "USD",
			LineItems: []AddLineItemRequest{{
				ExternalReferenceID: "line-001",
				OccurredAt:          time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
				AmountMinor:         125,
			}},
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&AddLineItemsRequest{}),
			ResponseType: reflect.TypeOf(&AddLineItemsResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		return enmiddleware.Response{
			Err: conflictError("bill is not accepting new line items"),
		}
	})

	require.Error(t, resp.Err)
	assert.Equal(t, errs.Aborted, errs.Code(resp.Err))
	assert.Contains(t, resp.Err.Error(), "bill is not accepting new line items")
}

func TestIdempotencyMiddlewareSetsHTTPStatusFromPayloadWhenUnset(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveAcquired,
	}, nil).Once()
	store.On("Complete", mock.Anything, mock.MatchedBy(func(params idempotencystore.CompleteParams) bool {
		return params.Response.HTTPStatus == http.StatusAccepted
	})).Return(nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills/11111111-1111-4111-8111-111111111111/close",
		Headers: http.Header{"Idempotency-Key": []string{"idem-close-001"}},
		Payload: &CloseBillRequest{
			IdempotencyKey: "idem-close-001",
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&CloseBillRequest{}),
			ResponseType: reflect.TypeOf(&CloseBillResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		return enmiddleware.Response{
			Payload: &CloseBillResponse{
				BillID:           "11111111-1111-4111-8111-111111111111",
				BillStatus:       "OPEN",
				TotalAmountMinor: 0,
				Status:           http.StatusAccepted,
			},
		}
	})

	require.NoError(t, resp.Err)
	assert.Equal(t, http.StatusAccepted, resp.HTTPStatus)
}

func TestIdempotencyMiddlewareSetsHTTPStatusFromTransformedPayloadWhenUnset(t *testing.T) {
	t.Parallel()

	store := mocks.NewIdempotencyStore(t)
	store.On("Reserve", mock.Anything, mock.AnythingOfType("idempotency.ReserveParams")).Return(&idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveAcquired,
	}, nil).Once()
	store.On("Complete", mock.Anything, mock.MatchedBy(func(params idempotencystore.CompleteParams) bool {
		return params.Response.HTTPStatus == http.StatusAccepted
	})).Return(nil).Once()

	svc := &Service{
		idempotencyStore: store,
		now:              func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
	}

	req := enmiddleware.NewRequest(context.Background(), &encore.Request{
		Method:  http.MethodPost,
		Path:    "/v1/bills/11111111-1111-4111-8111-111111111111/close",
		Headers: http.Header{"Idempotency-Key": []string{"idem-close-002"}},
		Payload: &CloseBillRequest{
			IdempotencyKey: "idem-close-002",
		},
		API: &encore.APIDesc{
			RequestType:  reflect.TypeOf(&CloseBillRequest{}),
			ResponseType: reflect.TypeOf(&CloseBillResponse{}),
		},
	})

	resp := svc.Idempotency(req, func(req enmiddleware.Request) enmiddleware.Response {
		// Simulate a runtime-transformed payload that still carries the status value
		// but no longer exposes the original encore struct tag.
		return enmiddleware.Response{
			Payload: &struct {
				BillID           string `json:"bill_id"`
				BillStatus       string `json:"bill_status"`
				TotalAmountMinor int64  `json:"total_amount_minor"`
				Status           int    `json:"-"`
			}{
				BillID:           "11111111-1111-4111-8111-111111111111",
				BillStatus:       "OPEN",
				TotalAmountMinor: 0,
				Status:           http.StatusAccepted,
			},
		}
	})

	require.NoError(t, resp.Err)
	assert.Equal(t, http.StatusAccepted, resp.HTTPStatus)
}
