-- TenderGraph Phase 7 analyst feedback control plane.
-- Additive only: source tables and source records are not modified.

CREATE TABLE IF NOT EXISTS tg_relationship_candidates (
    candidate_id bigint PRIMARY KEY,
    candidate_gid text NOT NULL UNIQUE,
    relationship_type text NOT NULL,
    source_type text NOT NULL,
    source_id text NOT NULL,
    source_gid text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    target_gid text NOT NULL,
    generation_method text NOT NULL,
    initial_evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    latest_evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    generator_version text NOT NULL,
    current_status text NOT NULL DEFAULT 'PENDING'
        CHECK (current_status IN ('PENDING', 'CONFIRMED', 'REJECTED', 'NEEDS_REVIEW')),
    current_version integer NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    first_detected_at timestamptz NOT NULL DEFAULT now(),
    last_evaluated_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS tg_relationship_candidates_queue_idx
    ON tg_relationship_candidates(current_status, relationship_type, candidate_id);
CREATE INDEX IF NOT EXISTS tg_relationship_candidates_method_idx
    ON tg_relationship_candidates(generation_method, candidate_id);
CREATE INDEX IF NOT EXISTS tg_relationship_candidates_source_idx
    ON tg_relationship_candidates(source_type, source_id);

CREATE TABLE IF NOT EXISTS tg_relationship_decisions (
    decision_id bigserial PRIMARY KEY,
    candidate_id bigint NOT NULL UNIQUE REFERENCES tg_relationship_candidates(candidate_id),
    decision_status text NOT NULL
        CHECK (decision_status IN ('CONFIRMED', 'REJECTED', 'NEEDS_REVIEW')),
    analyst_id text,
    rationale text NOT NULL CHECK (length(btrim(rationale)) > 0),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    decision_version integer NOT NULL CHECK (decision_version > 0),
    request_id text NOT NULL,
    decided_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, decision_version),
    UNIQUE (candidate_id, request_id)
);

CREATE INDEX IF NOT EXISTS tg_relationship_decisions_status_idx
    ON tg_relationship_decisions(decision_status, decided_at DESC);

CREATE TABLE IF NOT EXISTS tg_relationship_decision_history (
    history_id bigserial PRIMARY KEY,
    candidate_id bigint NOT NULL REFERENCES tg_relationship_candidates(candidate_id),
    previous_status text
        CHECK (previous_status IS NULL OR previous_status IN ('PENDING', 'CONFIRMED', 'REJECTED', 'NEEDS_REVIEW')),
    new_status text NOT NULL
        CHECK (new_status IN ('CONFIRMED', 'REJECTED', 'NEEDS_REVIEW')),
    analyst_id text,
    rationale text NOT NULL CHECK (length(btrim(rationale)) > 0),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    decision_version integer NOT NULL CHECK (decision_version > 0),
    request_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, request_id)
);

CREATE INDEX IF NOT EXISTS tg_relationship_decision_history_candidate_idx
    ON tg_relationship_decision_history(candidate_id, decision_version, history_id);

CREATE TABLE IF NOT EXISTS tg_graph_projection_outbox (
    projection_id bigint PRIMARY KEY,
    projection_gid text NOT NULL UNIQUE,
    candidate_id bigint NOT NULL REFERENCES tg_relationship_candidates(candidate_id),
    decision_version integer NOT NULL,
    desired_operation text NOT NULL CHECK (desired_operation IN (
        'UPSERT_CONFIRMED_RELATIONSHIP',
        'NO_CONFIRMED_RELATIONSHIP'
    )),
    relationship_type text NOT NULL,
    source_gid text NOT NULL,
    target_gid text NOT NULL,
    provenance jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'APPLIED', 'FAILED')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, decision_version)
);

CREATE INDEX IF NOT EXISTS tg_graph_projection_outbox_status_idx
    ON tg_graph_projection_outbox(status, created_at, projection_id);
