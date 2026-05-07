CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE bills (
    bill_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id TEXT NOT NULL,
    external_reference_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    currency_code TEXT NOT NULL,
    bill_status TEXT NOT NULL,
    billing_period_start_at TIMESTAMPTZ NOT NULL,
    billing_period_end_at TIMESTAMPTZ NOT NULL,
    line_items_submission_deadline TIMESTAMPTZ NOT NULL,
    total_amount_minor BIGINT NOT NULL DEFAULT 0,
    closed_at TIMESTAMPTZ NULL,
    failed_at TIMESTAMPTZ NULL,
    failure_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_bills_account_id_external_reference_id UNIQUE (account_id, external_reference_id),
    CONSTRAINT ux_bills_bill_id_currency_code UNIQUE (bill_id, currency_code),
    CONSTRAINT chk_bills_currency_code CHECK (currency_code IN ('GEL', 'USD')),
    CONSTRAINT chk_bills_bill_status CHECK (bill_status IN ('SCHEDULED', 'OPEN', 'FINALIZING', 'CLOSED', 'FAILED')),
    CONSTRAINT chk_bills_billing_period_range CHECK (billing_period_start_at <= billing_period_end_at),
    CONSTRAINT chk_bills_submission_deadline CHECK (line_items_submission_deadline >= billing_period_end_at),
    CONSTRAINT chk_bills_closed_at CHECK (
        (bill_status = 'CLOSED' AND closed_at IS NOT NULL AND failed_at IS NULL) OR
        (bill_status <> 'CLOSED' AND closed_at IS NULL)
    ),
    CONSTRAINT chk_bills_failed_at CHECK (
        (bill_status = 'FAILED' AND failed_at IS NOT NULL) OR
        (bill_status <> 'FAILED' AND failed_at IS NULL)
    ),
    CONSTRAINT chk_bills_failure_reason CHECK (
        (bill_status = 'FAILED' AND failure_reason IS NOT NULL) OR
        (bill_status <> 'FAILED' AND failure_reason IS NULL)
    )
);

CREATE INDEX idx_bills_bill_status_created_at_desc
    ON bills (bill_status, created_at DESC);

CREATE INDEX idx_bills_account_external_reference_idempotency
    ON bills (account_id, external_reference_id, idempotency_key);

CREATE TRIGGER trg_bills_set_updated_at
BEFORE UPDATE ON bills
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TABLE line_items (
    line_item_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bill_id UUID NOT NULL,
    request_id TEXT NOT NULL,
    external_reference_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    currency_code TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    amount_minor BIGINT NOT NULL,
    description TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_line_items_bill_id_external_reference_id UNIQUE (bill_id, external_reference_id),
    CONSTRAINT chk_line_items_currency_code CHECK (currency_code IN ('GEL', 'USD')),
    CONSTRAINT fk_line_items_bill_id_currency_code
        FOREIGN KEY (bill_id, currency_code) REFERENCES bills (bill_id, currency_code)
);

CREATE INDEX idx_line_items_bill_id_occurred_at_line_item_id
    ON line_items (bill_id, occurred_at, line_item_id);

CREATE INDEX idx_line_items_bill_external_reference_idempotency
    ON line_items (bill_id, external_reference_id, idempotency_key);

CREATE TABLE idempotency_records (
    idempotency_key TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    request_fingerprint TEXT NULL,
    processing_status TEXT NOT NULL,
    in_progress_expires_at TIMESTAMPTZ NULL,
    response_http_status INTEGER NULL,
    response_body_json JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (idempotency_key, operation_type),
    CONSTRAINT chk_idempotency_processing_status CHECK (processing_status IN ('IN_PROGRESS', 'COMPLETED')),
    CONSTRAINT chk_idempotency_completed_response CHECK (
        (processing_status = 'COMPLETED' AND response_http_status IS NOT NULL AND response_body_json IS NOT NULL) OR
        processing_status <> 'COMPLETED'
    ),
    CONSTRAINT chk_idempotency_in_progress_lease CHECK (
        (processing_status = 'IN_PROGRESS' AND in_progress_expires_at IS NOT NULL) OR
        (processing_status <> 'IN_PROGRESS' AND in_progress_expires_at IS NULL)
    )
);

CREATE INDEX idx_idempotency_records_in_progress_expires_at
    ON idempotency_records (in_progress_expires_at)
    WHERE processing_status = 'IN_PROGRESS';

CREATE INDEX idx_idempotency_records_completed_expires_at
    ON idempotency_records (expires_at)
    WHERE processing_status = 'COMPLETED';
