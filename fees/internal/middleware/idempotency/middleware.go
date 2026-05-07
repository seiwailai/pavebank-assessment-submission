package idempotency

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"encore.dev/beta/errs"
	enmiddleware "encore.dev/middleware"

	idempotencystore "encore.app/fees/internal/store/idempotency"
)

const (
	leaseTTL  = 2 * time.Minute
	recordTTL = 24 * time.Hour
)

type Dependencies struct {
	Store idempotencystore.Store
	Now   func() time.Time
}

func Handle(req enmiddleware.Request, next enmiddleware.Next, deps Dependencies) enmiddleware.Response {
	if deps.Store == nil {
		return enmiddleware.Response{Err: internalError("internal error")}
	}
	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now()
	}

	data := req.Data()
	if data == nil {
		return enmiddleware.Response{Err: internalError("internal error")}
	}

	key := extractIdempotencyKey(data.Headers, data.Payload)
	if key == "" {
		return enmiddleware.Response{Err: invalidArgument("Idempotency-Key header is required")}
	}

	fingerprint, err := fingerprint(data.Payload)
	if err != nil {
		return enmiddleware.Response{Err: internalError("internal error")}
	}
	scope := idempotencystore.Scope(data.Method + ":" + data.Path)

	reservation, err := deps.Store.Reserve(req.Context(), idempotencystore.ReserveParams{
		Scope:            scope,
		Key:              key,
		Fingerprint:      fingerprint,
		InProgressExpiry: now.Add(leaseTTL),
		RecordExpiry:     now.Add(recordTTL),
		Now:              now,
	})
	if err != nil {
		return enmiddleware.Response{Err: internalError("internal error: reserve failed: " + err.Error())}
	}

	switch reservation.Outcome {
	case idempotencystore.ReserveReplay:
		replayResp, err := decodeReplayResponse(data.API.ResponseType, reservation)
		if err != nil {
			return enmiddleware.Response{Err: internalError("internal error")}
		}
		return replayResp
	case idempotencystore.ReserveConflict:
		return enmiddleware.Response{Err: conflictError("idempotency key was already used with a different request payload")}
	case idempotencystore.ReserveInProgress:
		return enmiddleware.Response{Err: conflictError("request with this idempotency key is already in progress")}
	}

	req = req.WithContext(withMetadata(req.Context(), Metadata{
		Scope:       string(scope),
		Key:         key,
		Fingerprint: fingerprint,
	}))

	resp := next(req)
	if resp.Err == nil && resp.HTTPStatus == 0 {
		resp.HTTPStatus = responseStatus(resp)
	}
	stored, shouldComplete, err := responseForCompletion(resp)
	if err != nil {
		// Preserve original business response when idempotency persistence encoding fails.
		// This avoids turning a valid handler result into a false 500.
		return resp
	}
	if shouldComplete {
		if err := deps.Store.Complete(req.Context(), idempotencystore.CompleteParams{
			Scope:       scope,
			Key:         key,
			Fingerprint: fingerprint,
			Response:    stored,
		}); err != nil {
			// Preserve the original business response even if idempotency persistence fails.
			// This avoids surfacing false 500s after the handler already produced a valid response.
			return resp
		}
	}
	return resp
}

func extractIdempotencyKey(headers http.Header, payload any) string {
	if key := strings.TrimSpace(headers.Get("Idempotency-Key")); key != "" {
		return key
	}
	return strings.TrimSpace(extractTaggedHeaderValue(payload, "Idempotency-Key"))
}

func extractTaggedHeaderValue(payload any, headerName string) string {
	value := reflect.ValueOf(payload)
	if !value.IsValid() {
		return ""
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return ""
	}

	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Tag.Get("header") != headerName {
			continue
		}
		fieldValue := value.Field(i)
		if fieldValue.Kind() == reflect.String {
			return fieldValue.String()
		}
	}
	return ""
}

func decodeReplayResponse(responseType reflect.Type, reservation *idempotencystore.ReserveResult) (enmiddleware.Response, error) {
	status := 200
	if reservation != nil && reservation.Record != nil && reservation.Record.Response != nil && reservation.Record.Response.HTTPStatus != 0 {
		status = reservation.Record.Response.HTTPStatus
	}
	if reservation == nil || reservation.Record == nil || reservation.Record.Response == nil {
		return enmiddleware.Response{HTTPStatus: status}, nil
	}

	if replayErr, ok, err := decodeReplayError(reservation.Record.Response); err != nil {
		return enmiddleware.Response{}, err
	} else if ok {
		return enmiddleware.Response{Err: replayErr, HTTPStatus: status}, nil
	}

	payload, err := decodeReplayPayload(responseType, reservation.Record.Response.Body)
	if err != nil {
		return enmiddleware.Response{}, err
	}
	return enmiddleware.Response{Payload: payload, HTTPStatus: status}, nil
}

func decodeReplayPayload(responseType reflect.Type, body []byte) (any, error) {
	if responseType == nil {
		return nil, nil
	}
	body = unwrapReplaySuccessBody(body)

	targetType := responseType
	if targetType.Kind() == reflect.Pointer {
		target := reflect.New(targetType.Elem())
		if err := json.Unmarshal(body, target.Interface()); err != nil {
			return nil, err
		}
		return target.Interface(), nil
	}

	target := reflect.New(targetType)
	if err := json.Unmarshal(body, target.Interface()); err != nil {
		return nil, err
	}
	return target.Elem().Interface(), nil
}

func decodeReplayError(stored *idempotencystore.StoredResponse) (error, bool, error) {
	if stored == nil {
		return nil, false, nil
	}

	var envelope replayErrorEnvelope
	if err := json.Unmarshal(stored.Body, &envelope); err != nil {
		return nil, false, nil
	}
	if envelope.Kind != replayErrorKind {
		return nil, false, nil
	}

	code, ok := parseErrCode(envelope.ErrorCode)
	if !ok {
		return nil, false, errors.New("unknown replay error code")
	}

	var details errs.ErrDetails
	if len(envelope.ErrorDetails) > 0 && string(envelope.ErrorDetails) != "null" {
		raw := replayErrorDetails{}
		if err := json.Unmarshal(envelope.ErrorDetails, &raw); err != nil {
			return nil, false, err
		}
		details = raw
	}

	return &errs.Error{
		Code:    code,
		Message: envelope.ErrorMessage,
		Details: details,
	}, true, nil
}

func fingerprint(payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func responseForCompletion(resp enmiddleware.Response) (idempotencystore.StoredResponse, bool, error) {
	if resp.Err == nil {
		body, err := json.Marshal(resp.Payload)
		if err != nil {
			return idempotencystore.StoredResponse{}, false, err
		}
		return idempotencystore.StoredResponse{
			HTTPStatus: responseStatus(resp),
			Body:       body,
		}, true, nil
	}

	stored, ok, err := replayableErrorResponse(resp)
	if err != nil {
		return idempotencystore.StoredResponse{}, false, err
	}
	if !ok {
		return idempotencystore.StoredResponse{}, false, nil
	}
	return stored, true, nil
}

func replayableErrorResponse(resp enmiddleware.Response) (idempotencystore.StoredResponse, bool, error) {
	var encoreErr *errs.Error
	if !errors.As(resp.Err, &encoreErr) {
		return idempotencystore.StoredResponse{}, false, nil
	}
	if !isReplayableErrCode(encoreErr.Code) {
		return idempotencystore.StoredResponse{}, false, nil
	}

	var details json.RawMessage
	if encoreErr.Details != nil {
		body, err := json.Marshal(encoreErr.Details)
		if err != nil {
			return idempotencystore.StoredResponse{}, false, err
		}
		details = body
	}

	body, err := json.Marshal(replayErrorEnvelope{
		Kind:         replayErrorKind,
		ErrorCode:    encoreErr.Code.String(),
		ErrorMessage: encoreErr.Message,
		ErrorDetails: details,
	})
	if err != nil {
		return idempotencystore.StoredResponse{}, false, err
	}

	status := resp.HTTPStatus
	if status == 0 {
		status = encoreErr.Code.HTTPStatus()
	}
	return idempotencystore.StoredResponse{
		HTTPStatus: status,
		Body:       body,
	}, true, nil
}

func responseStatus(resp enmiddleware.Response) int {
	if resp.HTTPStatus != 0 {
		return resp.HTTPStatus
	}
	if resp.Payload == nil {
		return 200
	}

	value := reflect.ValueOf(resp.Payload)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 200
		}
		value = value.Elem()
	}
	if status, ok := statusFromMapValue(value); ok {
		return status
	}
	if value.Kind() != reflect.Struct {
		return 200
	}

	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		statusField := value.Field(i)
		if status, ok := statusFromStructField(field, statusField); ok {
			return status
		}
	}
	return 200
}

func statusFromStructField(field reflect.StructField, value reflect.Value) (int, bool) {
	if !isHTTPStatusField(field) {
		return 0, false
	}
	if !value.IsValid() || value.Kind() != reflect.Int {
		return 0, false
	}
	status := int(value.Int())
	if status < 100 || status > 999 {
		return 0, false
	}
	return status, true
}

func isHTTPStatusField(field reflect.StructField) bool {
	if field.Tag.Get("encore") == "httpstatus" {
		return true
	}
	return field.Name == "Status" || field.Name == "HTTPStatus"
}

func statusFromMapValue(value reflect.Value) (int, bool) {
	if !value.IsValid() || value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String {
		return 0, false
	}

	for _, key := range []string{"status", "http_status"} {
		entry := value.MapIndex(reflect.ValueOf(key))
		if !entry.IsValid() {
			continue
		}
		status, ok := reflectNumericToInt(entry)
		if ok && status > 0 {
			return status, true
		}
	}
	return 0, false
}

func reflectNumericToInt(value reflect.Value) (int, bool) {
	if !value.IsValid() {
		return 0, false
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, false
		}
		return reflectNumericToInt(value.Elem())
	}

	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		return int(value.Float()), true
	default:
		return 0, false
	}
}

const replayErrorKind = "error"

type replayErrorEnvelope struct {
	Kind         string          `json:"kind"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	ErrorDetails json.RawMessage `json:"error_details,omitempty"`
}

type replayErrorDetails map[string]any

func (replayErrorDetails) ErrDetails() {}

func unwrapReplaySuccessBody(body []byte) []byte {
	var envelope struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Kind == "success" && len(envelope.Payload) > 0 {
		return envelope.Payload
	}
	return body
}

func parseErrCode(code string) (errs.ErrCode, bool) {
	switch code {
	case "invalid_argument":
		return errs.InvalidArgument, true
	case "not_found":
		return errs.NotFound, true
	case "already_exists":
		return errs.AlreadyExists, true
	case "permission_denied":
		return errs.PermissionDenied, true
	case "failed_precondition":
		return errs.FailedPrecondition, true
	case "aborted":
		return errs.Aborted, true
	case "out_of_range":
		return errs.OutOfRange, true
	case "unimplemented":
		return errs.Unimplemented, true
	case "unauthenticated":
		return errs.Unauthenticated, true
	default:
		return 0, false
	}
}

func isReplayableErrCode(code errs.ErrCode) bool {
	switch code {
	case errs.InvalidArgument,
		errs.NotFound,
		errs.AlreadyExists,
		errs.PermissionDenied,
		errs.FailedPrecondition,
		errs.Aborted,
		errs.OutOfRange,
		errs.Unimplemented,
		errs.Unauthenticated:
		return true
	default:
		return false
	}
}

func invalidArgument(message string) error {
	return &errs.Error{Code: errs.InvalidArgument, Message: message}
}

func conflictError(message string) error {
	return &errs.Error{Code: errs.Aborted, Message: message}
}

func internalError(message string) error {
	return &errs.Error{Code: errs.Internal, Message: message}
}
