package idempotency

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"encore.dev/beta/errs"
	enmiddleware "encore.dev/middleware"

	idempotencystore "encore.app/fees/internal/store/idempotency"
)

func TestDecodeReplayPayloadAndSuccessEnvelope(t *testing.T) {
	t.Parallel()

	type payload struct {
		BillID string `json:"bill_id"`
	}

	got, err := decodeReplayPayload(reflectTypeOf[*payload](), []byte(`{"kind":"success","payload":{"bill_id":"bill-1"}}`))
	if err != nil {
		t.Fatalf("decodeReplayPayload() error = %v", err)
	}
	typed, ok := got.(*payload)
	if !ok || typed.BillID != "bill-1" {
		t.Fatalf("decodeReplayPayload() = %#v", got)
	}

	raw := unwrapReplaySuccessBody([]byte(`{"kind":"success","payload":{"bill_id":"bill-2"}}`))
	if string(raw) != `{"bill_id":"bill-2"}` {
		t.Fatalf("unwrapReplaySuccessBody() = %s", raw)
	}
}

func TestDecodeReplayResponseAndErrorHelpers(t *testing.T) {
	t.Parallel()

	resp, err := decodeReplayResponse(nil, &idempotencystore.ReserveResult{
		Outcome: idempotencystore.ReserveReplay,
		Record: &idempotencystore.Record{
			Response: &idempotencystore.StoredResponse{
				HTTPStatus: 409,
				Body:       []byte(`{"kind":"error","error_code":"aborted","error_message":"boom","error_details":{"why":"duplicate"}}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("decodeReplayResponse() error = %v", err)
	}
	if errs.Code(resp.Err) != errs.Aborted || resp.HTTPStatus != 409 {
		t.Fatalf("decodeReplayResponse() = %#v", resp)
	}

	if _, _, err := decodeReplayError(&idempotencystore.StoredResponse{
		Body: []byte(`{"kind":"error","error_code":"mystery","error_message":"boom"}`),
	}); err == nil {
		t.Fatal("expected unknown error code failure")
	}

	if code, ok := parseErrCode("not_found"); !ok || code != errs.NotFound {
		t.Fatalf("parseErrCode() = %v, %v", code, ok)
	}
	if _, ok := parseErrCode("unknown"); ok {
		t.Fatal("unexpected parseErrCode success")
	}
	if !isReplayableErrCode(errs.Aborted) || isReplayableErrCode(errs.Internal) {
		t.Fatal("unexpected replayable error classification")
	}
}

func TestResponseForCompletionHelpers(t *testing.T) {
	t.Parallel()

	stored, shouldComplete, err := responseForCompletion(enmiddleware.Response{
		Payload: &struct {
			BillID string `json:"bill_id"`
			Status int    `encore:"httpstatus" json:"-"`
		}{BillID: "bill-1", Status: 201},
	})
	if err != nil || !shouldComplete || stored.HTTPStatus != 201 {
		t.Fatalf("responseForCompletion(success) = %#v, %v, %v", stored, shouldComplete, err)
	}

	stored, shouldComplete, err = responseForCompletion(enmiddleware.Response{
		Err: &errs.Error{Code: errs.Aborted, Message: "duplicate"},
	})
	if err != nil || !shouldComplete || stored.HTTPStatus != 409 {
		t.Fatalf("responseForCompletion(error) = %#v, %v, %v", stored, shouldComplete, err)
	}

	_, shouldComplete, err = responseForCompletion(enmiddleware.Response{
		Err: &errs.Error{Code: errs.Internal, Message: "retry me"},
	})
	if err != nil || shouldComplete {
		t.Fatalf("responseForCompletion(internal) = %v, %v", shouldComplete, err)
	}
}

func TestFingerprintAndResponseStatus(t *testing.T) {
	t.Parallel()

	fp, err := fingerprint(map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("fingerprint() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(fp), &body); err != nil || body["a"] != float64(1) {
		t.Fatalf("fingerprint() = %s", fp)
	}

	if status := responseStatus(enmiddleware.Response{HTTPStatus: 202}); status != 202 {
		t.Fatalf("responseStatus() = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: nil}); status != 200 {
		t.Fatalf("responseStatus(nil payload) = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: "not-struct"}); status != 200 {
		t.Fatalf("responseStatus(non-struct) = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: &struct {
		Status int `encore:"httpstatus" json:"-"`
	}{Status: 204}}); status != 204 {
		t.Fatalf("responseStatus(tagged struct) = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: &struct {
		Status int `json:"-"`
	}{Status: http.StatusAccepted}}); status != http.StatusAccepted {
		t.Fatalf("responseStatus(untagged struct status field) = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: &struct {
		HTTPStatus int `json:"-"`
	}{HTTPStatus: http.StatusCreated}}); status != http.StatusCreated {
		t.Fatalf("responseStatus(untagged struct http status field) = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: map[string]any{"status": 202.0}}); status != 202 {
		t.Fatalf("responseStatus(map status) = %d", status)
	}
	if status := responseStatus(enmiddleware.Response{Payload: map[string]any{"http_status": 206}}); status != 206 {
		t.Fatalf("responseStatus(map http_status) = %d", status)
	}
}

func reflectTypeOf[T any]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}
