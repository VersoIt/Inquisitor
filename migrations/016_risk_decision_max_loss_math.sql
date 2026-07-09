ALTER TABLE risk_decisions
    DROP CONSTRAINT IF EXISTS risk_decisions_approved_max_loss_math;

ALTER TABLE risk_decisions
    ADD CONSTRAINT risk_decisions_approved_max_loss_math CHECK (
        NOT approved
        OR max_loss = final_quantity * abs(entry_price - stop_loss)
    ) NOT VALID;
