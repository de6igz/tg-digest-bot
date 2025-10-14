BEGIN;

ALTER TABLE billing_payments DROP CONSTRAINT IF EXISTS billing_payments_amount_check;
ALTER TABLE billing_payments ADD CONSTRAINT billing_payments_amount_check CHECK (amount <> 0);

COMMIT;
