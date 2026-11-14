-- TenderGraph Phase 7.5 reliability additions.
-- Additive only: source tables and source records are not modified.

ALTER TABLE tg_relationship_candidates
    DROP CONSTRAINT IF EXISTS tg_relationship_candidates_current_status_check;
ALTER TABLE tg_relationship_candidates
    ADD CONSTRAINT tg_relationship_candidates_current_status_check
    CHECK (current_status IN ('PENDING', 'STALE', 'CONFIRMED', 'REJECTED', 'NEEDS_REVIEW'));

ALTER TABLE tg_relationship_decisions
    ADD COLUMN IF NOT EXISTS actor_id text;
ALTER TABLE tg_relationship_decisions
    ADD COLUMN IF NOT EXISTS actor_kind text;
UPDATE tg_relationship_decisions
SET actor_id = COALESCE(NULLIF(actor_id, ''), COALESCE(NULLIF(analyst_id, ''), 'operator:legacy')),
    actor_kind = COALESCE(NULLIF(actor_kind, ''), CASE WHEN NULLIF(analyst_id, '') IS NULL THEN 'OPERATOR_SUPPLIED' ELSE 'OPERATOR_SUPPLIED' END)
WHERE actor_id IS NULL OR actor_id = '' OR actor_kind IS NULL OR actor_kind = '';
ALTER TABLE tg_relationship_decisions
    ALTER COLUMN actor_id SET DEFAULT 'operator:legacy',
    ALTER COLUMN actor_id SET NOT NULL,
    ALTER COLUMN actor_kind SET DEFAULT 'OPERATOR_SUPPLIED',
    ALTER COLUMN actor_kind SET NOT NULL;
ALTER TABLE tg_relationship_decisions
    DROP CONSTRAINT IF EXISTS tg_relationship_decisions_actor_kind_check;
ALTER TABLE tg_relationship_decisions
    ADD CONSTRAINT tg_relationship_decisions_actor_kind_check
    CHECK (actor_kind IN ('AUTHENTICATED', 'OPERATOR_SUPPLIED', 'SYSTEM'));

ALTER TABLE tg_relationship_decision_history
    DROP CONSTRAINT IF EXISTS tg_relationship_decision_history_previous_status_check;
ALTER TABLE tg_relationship_decision_history
    ADD CONSTRAINT tg_relationship_decision_history_previous_status_check
    CHECK (previous_status IS NULL OR previous_status IN ('PENDING', 'STALE', 'CONFIRMED', 'REJECTED', 'NEEDS_REVIEW'));
ALTER TABLE tg_relationship_decision_history
    ADD COLUMN IF NOT EXISTS actor_id text;
ALTER TABLE tg_relationship_decision_history
    ADD COLUMN IF NOT EXISTS actor_kind text;
UPDATE tg_relationship_decision_history
SET actor_id = COALESCE(NULLIF(actor_id, ''), COALESCE(NULLIF(analyst_id, ''), 'operator:legacy')),
    actor_kind = COALESCE(NULLIF(actor_kind, ''), CASE WHEN NULLIF(analyst_id, '') IS NULL THEN 'OPERATOR_SUPPLIED' ELSE 'OPERATOR_SUPPLIED' END)
WHERE actor_id IS NULL OR actor_id = '' OR actor_kind IS NULL OR actor_kind = '';
ALTER TABLE tg_relationship_decision_history
    ALTER COLUMN actor_id SET DEFAULT 'operator:legacy',
    ALTER COLUMN actor_id SET NOT NULL,
    ALTER COLUMN actor_kind SET DEFAULT 'OPERATOR_SUPPLIED',
    ALTER COLUMN actor_kind SET NOT NULL;
ALTER TABLE tg_relationship_decision_history
    DROP CONSTRAINT IF EXISTS tg_relationship_decision_history_actor_kind_check;
ALTER TABLE tg_relationship_decision_history
    ADD CONSTRAINT tg_relationship_decision_history_actor_kind_check
    CHECK (actor_kind IN ('AUTHENTICATED', 'OPERATOR_SUPPLIED', 'SYSTEM'));

ALTER TABLE tg_graph_projection_outbox
    DROP CONSTRAINT IF EXISTS tg_graph_projection_outbox_status_check;
ALTER TABLE tg_graph_projection_outbox
    ADD CONSTRAINT tg_graph_projection_outbox_status_check
    CHECK (status IN ('PENDING', 'APPLIED', 'FAILED', 'SUPERSEDED'));
ALTER TABLE tg_graph_projection_outbox
    ADD COLUMN IF NOT EXISTS claimed_by text,
    ADD COLUMN IF NOT EXISTS claimed_at timestamptz;

-- Normal application operations must not rewrite or remove the audit log.
-- This is not a protection against a privileged database administrator.
CREATE OR REPLACE FUNCTION tg_prevent_decision_history_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF current_setting('tg_history_cleanup', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'tg_relationship_decision_history is append-only';
END;
$$;

DROP TRIGGER IF EXISTS tg_relationship_decision_history_immutable
    ON tg_relationship_decision_history;
CREATE TRIGGER tg_relationship_decision_history_immutable
BEFORE UPDATE OR DELETE ON tg_relationship_decision_history
FOR EACH ROW EXECUTE FUNCTION tg_prevent_decision_history_mutation();

ALTER TABLE tg_reconciliation_runs
    ADD COLUMN IF NOT EXISTS cursor_time timestamptz,
    ADD COLUMN IF NOT EXISTS cursor_pk text,
    ADD COLUMN IF NOT EXISTS records_scanned bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS checkpoint_at timestamptz;
