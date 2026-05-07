package fees

import (
	"fmt"
	"strings"
	"time"

	"encore.dev/beta/errs"
	"github.com/google/uuid"

	"encore.app/fees/internal/domain"
	"encore.app/fees/internal/pagination"
	billingstore "encore.app/fees/internal/store/billing"
	workflowpkg "encore.app/fees/internal/workflow"
)

type HealthResponse struct {
	Service string `json:"service"`
}

type CreateBillRequest struct {
	IdempotencyKey              string    `header:"Idempotency-Key" json:"-"`
	AccountID                   string    `json:"account_id"`
	ExternalReferenceID         string    `json:"external_reference_id"`
	CurrencyCode                string    `json:"currency_code"`
	BillingPeriodStartAt        time.Time `json:"billing_period_start_at"`
	BillingPeriodEndAt          time.Time `json:"billing_period_end_at"`
	LineItemsSubmissionDeadline time.Time `json:"line_items_submission_deadline"`
}

type CreateBillResponse struct {
	BillID string `json:"bill_id"`
	Status int    `encore:"httpstatus" json:"http_status"`
}

type AddLineItemsRequest struct {
	IdempotencyKey string               `header:"Idempotency-Key" json:"-"`
	CurrencyCode   string               `json:"currency_code"`
	LineItems      []AddLineItemRequest `json:"line_items"`
}

type AddLineItemRequest struct {
	ExternalReferenceID string    `json:"external_reference_id"`
	OccurredAt          time.Time `json:"occurred_at"`
	AmountMinor         int64     `json:"amount_minor"`
	Description         *string   `json:"description,omitempty"`
}

type AddLineItemsResponse struct {
	SuccessLineItemIDsMap    map[string]string `json:"success_line_item_ids_map"`
	FailedLineItemReasonsMap map[string]string `json:"failed_line_item_reasons_map"`
	Status                   int               `encore:"httpstatus" json:"http_status"`
}

type CloseBillRequest struct {
	IdempotencyKey string `header:"Idempotency-Key" json:"-"`
}

type CloseBillResponse struct {
	BillID           string `json:"bill_id"`
	BillStatus       string `json:"bill_status"`
	TotalAmountMinor int64  `json:"total_amount_minor"`
	Status           int    `encore:"httpstatus" json:"http_status"`
}

type GetBillResponse struct {
	BillID                      string     `json:"bill_id"`
	AccountID                   string     `json:"account_id"`
	ExternalReferenceID         string     `json:"external_reference_id"`
	BillStatus                  string     `json:"bill_status"`
	CurrencyCode                string     `json:"currency_code"`
	BillingPeriodStartAt        time.Time  `json:"billing_period_start_at"`
	BillingPeriodEndAt          time.Time  `json:"billing_period_end_at"`
	LineItemsSubmissionDeadline time.Time  `json:"line_items_submission_deadline"`
	SnapshotTotalAmountMinor    *int64     `json:"snapshot_total_amount_minor,omitempty"`
	FinalTotalAmountMinor       *int64     `json:"final_total_amount_minor,omitempty"`
	ClosedAt                    *time.Time `json:"closed_at,omitempty"`
	FailedAt                    *time.Time `json:"failed_at,omitempty"`
	FailureReason               *string    `json:"failure_reason,omitempty"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

type ListBillLineItemsParams struct {
	PageSize  int    `query:"page_size"`
	PageToken string `query:"page_token"`
}

type ListBillLineItemsResponse struct {
	BillID        string             `json:"bill_id"`
	CurrencyCode  string             `json:"currency_code"`
	Items         []LineItemResponse `json:"items"`
	NextPageToken *string            `json:"next_page_token,omitempty"`
}

type LineItemResponse struct {
	LineItemID          string    `json:"line_item_id"`
	ExternalReferenceID string    `json:"external_reference_id"`
	OccurredAt          time.Time `json:"occurred_at"`
	AmountMinor         int64     `json:"amount_minor"`
	Description         *string   `json:"description,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

func (r *CreateBillRequest) Validate() error {
	if r == nil {
		return invalidArgument("request body is required")
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return invalidArgument("Idempotency-Key header is required")
	}
	if r.AccountID == "" {
		return invalidArgument("account_id is required")
	}
	if r.ExternalReferenceID == "" {
		return invalidArgument("external_reference_id is required")
	}
	if !domain.IsSupportedCurrency(domain.CurrencyCode(r.CurrencyCode)) {
		return invalidArgument("currency_code must be one of GEL or USD")
	}
	if r.BillingPeriodStartAt.IsZero() || r.BillingPeriodEndAt.IsZero() {
		return invalidArgument("billing period timestamps are required")
	}
	if r.BillingPeriodEndAt.Before(r.BillingPeriodStartAt) {
		return invalidArgument("billing_period_end_at must be on or after billing_period_start_at")
	}
	if r.LineItemsSubmissionDeadline.IsZero() {
		return invalidArgument("line_items_submission_deadline is required")
	}
	if r.LineItemsSubmissionDeadline.Before(r.BillingPeriodEndAt) {
		return invalidArgument("line_items_submission_deadline must be on or after billing_period_end_at")
	}
	return nil
}

func (r *AddLineItemsRequest) Validate() error {
	if r == nil {
		return invalidArgument("request body is required")
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return invalidArgument("Idempotency-Key header is required")
	}
	if !domain.IsSupportedCurrency(domain.CurrencyCode(r.CurrencyCode)) {
		return invalidArgument("currency_code must be one of GEL or USD")
	}
	if len(r.LineItems) == 0 {
		return invalidArgument("line_items must not be empty")
	}
	if len(r.LineItems) > 100 {
		return invalidArgument("line_items must contain at most 100 items")
	}
	seenExternalReferenceIDs := make(map[string]struct{}, len(r.LineItems))
	for i, item := range r.LineItems {
		if item.ExternalReferenceID == "" {
			return invalidArgument(fmt.Sprintf("line_items[%d].external_reference_id is required", i))
		}
		if _, exists := seenExternalReferenceIDs[item.ExternalReferenceID]; exists {
			return invalidArgument("duplicate external_reference_id values are not allowed within a single batch")
		}
		seenExternalReferenceIDs[item.ExternalReferenceID] = struct{}{}
		if item.OccurredAt.IsZero() {
			return invalidArgument(fmt.Sprintf("line_items[%d].occurred_at is required", i))
		}
		if item.AmountMinor <= 0 {
			return invalidArgument(fmt.Sprintf("line_items[%d].amount_minor must be positive", i))
		}
	}
	return nil
}

func (r *AddLineItemsRequest) ValidateBusinessRules(bill *domain.Bill) error {
	for i, item := range r.LineItems {
		if item.OccurredAt.Before(bill.BillingPeriodStartAt) || item.OccurredAt.After(bill.BillingPeriodEndAt) {
			return invalidArgument(fmt.Sprintf("line_items[%d].occurred_at must fall within the bill billing period", i))
		}
	}
	return nil
}

func (r *CloseBillRequest) Validate() error {
	if r == nil {
		return invalidArgument("request body is required")
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return invalidArgument("Idempotency-Key header is required")
	}
	return nil
}

func (p *ListBillLineItemsParams) Validate() error {
	if p == nil {
		return nil
	}
	if p.PageSize > 100 {
		return invalidArgument("page_size must be between 1 and 100")
	}
	if p.PageToken != "" {
		var token pagination.LineItemPaginationToken
		if err := pagination.Decode(p.PageToken, &token); err != nil {
			return invalidArgument("page_token is invalid")
		}
	}
	return nil
}

func (r *CreateBillRequest) toWorkflowInput(bill *domain.Bill) workflowpkg.StartBillWorkflowInput {
	return workflowpkg.StartBillWorkflowInput{
		BillID:                      bill.ID,
		CurrencyCode:                bill.CurrencyCode,
		InitialStatus:               bill.Status,
		BillingPeriodStartAt:        bill.BillingPeriodStartAt,
		LineItemsSubmissionDeadline: bill.LineItemsSubmissionDeadline,
		SnapshotTotalAmountMinor:    0,
	}
}

func (r *AddLineItemsRequest) toWorkflowInput(billID string) workflowpkg.AddLineItemsInput {
	lineItems := make([]domain.AddLineItemInput, 0, len(r.LineItems))
	for _, item := range r.LineItems {
		lineItems = append(lineItems, domain.AddLineItemInput{
			ExternalReferenceID: item.ExternalReferenceID,
			OccurredAt:          item.OccurredAt,
			AmountMinor:         item.AmountMinor,
			Description:         item.Description,
		})
	}
	return workflowpkg.AddLineItemsInput{
		BillID:         billID,
		IdempotencyKey: r.IdempotencyKey,
		CurrencyCode:   domain.CurrencyCode(r.CurrencyCode),
		LineItems:      lineItems,
	}
}

func mapGetBillResponse(bill *domain.Bill) *GetBillResponse {
	if bill == nil {
		return nil
	}

	resp := &GetBillResponse{
		BillID:                      bill.ID,
		AccountID:                   bill.AccountID,
		ExternalReferenceID:         bill.ExternalReferenceID,
		BillStatus:                  string(bill.Status),
		CurrencyCode:                string(bill.CurrencyCode),
		BillingPeriodStartAt:        bill.BillingPeriodStartAt,
		BillingPeriodEndAt:          bill.BillingPeriodEndAt,
		LineItemsSubmissionDeadline: bill.LineItemsSubmissionDeadline,
		ClosedAt:                    bill.ClosedAt,
		FailedAt:                    bill.FailedAt,
		FailureReason:               bill.FailureReason,
		CreatedAt:                   bill.CreatedAt,
		UpdatedAt:                   bill.UpdatedAt,
	}

	total := bill.TotalAmountMinor
	if bill.Status == domain.BillStatusClosed {
		resp.FinalTotalAmountMinor = &total
	} else {
		resp.SnapshotTotalAmountMinor = &total
	}
	return resp
}

func mapGetBillResponseWithWorkflowSnapshot(bill *domain.Bill, state *workflowpkg.State) *GetBillResponse {
	resp := mapGetBillResponse(bill)
	if resp == nil || state == nil {
		return resp
	}

	total := state.SnapshotTotalAmountMinor
	resp.SnapshotTotalAmountMinor = &total
	resp.FinalTotalAmountMinor = nil
	resp.BillStatus = string(state.Status)
	resp.CurrencyCode = string(firstNonZeroCurrency(state.CurrencyCode, bill.CurrencyCode))
	return resp
}

func firstNonZeroCurrency(primary, fallback domain.CurrencyCode) domain.CurrencyCode {
	if primary != "" {
		return primary
	}
	return fallback
}

type addLineItemsFailureDetails struct {
	RecordReasons map[string]string `json:"record_reasons"`
}

func (addLineItemsFailureDetails) ErrDetails() {}

func lineItemsBatchFailureError(recordReasons map[string]string) error {
	return &errs.Error{
		Code:    errs.Aborted,
		Message: "line items batch rejected",
		Details: addLineItemsFailureDetails{RecordReasons: recordReasons},
	}
}

func (p *ListBillLineItemsParams) toRepositoryParams(billID string) (billingstore.ListLineItemsParams, error) {
	if err := p.Validate(); err != nil {
		return billingstore.ListLineItemsParams{}, err
	}
	params := billingstore.ListLineItemsParams{
		BillID: billID,
		Limit:  p.PageSize,
	}
	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 100 {
		return billingstore.ListLineItemsParams{}, invalidArgument("page_size must be between 1 and 100")
	}
	if p.PageToken == "" {
		return params, nil
	}

	var token pagination.LineItemPaginationToken
	if err := pagination.Decode(p.PageToken, &token); err != nil {
		return billingstore.ListLineItemsParams{}, invalidArgument("page_token is invalid")
	}
	params.OccurredAtAfter = &token.OccurredAt
	params.LineItemIDAfter = &token.LineItemID
	return params, nil
}

func mapListBillLineItemsResponse(bill *domain.Bill, result *billingstore.ListLineItemsResult) (*ListBillLineItemsResponse, error) {
	resp := &ListBillLineItemsResponse{
		BillID:       bill.ID,
		CurrencyCode: string(bill.CurrencyCode),
		Items:        make([]LineItemResponse, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		resp.Items = append(resp.Items, LineItemResponse{
			LineItemID:          item.ID,
			ExternalReferenceID: item.ExternalReferenceID,
			OccurredAt:          item.OccurredAt,
			AmountMinor:         item.AmountMinor,
			Description:         item.Description,
			CreatedAt:           item.CreatedAt,
		})
	}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		token, err := pagination.Encode(&pagination.LineItemPaginationToken{
			OccurredAt: last.OccurredAt,
			LineItemID: last.ID,
		})
		if err != nil {
			return nil, internalError("failed to encode next_page_token")
		}
		resp.NextPageToken = &token
	}
	return resp, nil
}

func validateUUIDv4Field(fieldName, value string) error {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return invalidArgument(fmt.Sprintf("%s must be a valid UUIDv4", fieldName))
	}
	if parsed.Version() != 4 {
		return invalidArgument(fmt.Sprintf("%s must be a valid UUIDv4", fieldName))
	}
	return nil
}
