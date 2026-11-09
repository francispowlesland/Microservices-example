-- +goose Up
CREATE TABLE payments (
    id              uuid PRIMARY KEY,
    order_id        uuid        NOT NULL,
    amount_cents    bigint      NOT NULL CONSTRAINT payments_amount_positive CHECK (amount_cents > 0),
    currency        char(3)     NOT NULL,
    -- 1 PENDING, 2 SUCCEEDED, 3 FAILED, 4 REFUNDED (payments.v1.PaymentStatus; 0 UNSPECIFIED is invalid)
    status          smallint    NOT NULL CONSTRAINT payments_status_valid CHECK (status IN (1, 2, 3, 4)),
    failure_reason  text        NOT NULL DEFAULT '',
    refund_reason   text        NOT NULL DEFAULT '',
    created_by      uuid        NOT NULL,
    idempotency_key text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payments_created_by_idempotency_key_key UNIQUE (created_by, idempotency_key)
);

CREATE INDEX payments_order_id_created_idx ON payments (order_id, created_at DESC, id DESC);
CREATE INDEX payments_created_by_created_idx ON payments (created_by, created_at DESC, id DESC);

-- Ledger rules: rows are never deleted, and the money-defining columns never change.
-- +goose StatementBegin
CREATE FUNCTION payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payments is an append-only ledger: DELETE is not allowed'
            USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.amount_cents IS DISTINCT FROM OLD.amount_cents
       OR NEW.currency IS DISTINCT FROM OLD.currency
       OR NEW.order_id IS DISTINCT FROM OLD.order_id
       OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
        RAISE EXCEPTION 'payments is an append-only ledger: amount_cents, currency, order_id and created_by are immutable'
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER payments_guard_update
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_guard();

CREATE TRIGGER payments_guard_delete
    BEFORE DELETE ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_guard();

-- +goose Down
DROP TRIGGER payments_guard_delete ON payments;
DROP TRIGGER payments_guard_update ON payments;
DROP FUNCTION payments_guard();
DROP TABLE payments;
