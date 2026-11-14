# CORRECTIONS.md — Phase 0 documentation correction pass (2026-11-14)

Documentation-only pass. Every finding below was re-verified against the repository, the
live PostgreSQL database (read-only probes), and the current official HydraDB repository and
documentation before the correction was written. Status labels: VERIFIED = fact from
repo/DB/official docs; PROPOSED = architecture decision awaiting approval; runtime claims
remain gated on `spike/hydradb_smoke.md` (NOT EXECUTED — BLOCKED).

## C1 — Identity resolution: automatic confirmation removed (P0)

- **Original issue**: ARCHITECTURE §2.3 allowed CONFIRMED transitions via "dual-rule
  corroboration (R1+R3)", contradicting GRAPH_ONTOLOGY §4 (R3 never auto-confirms) and the
  audit finding that GSTIN equality is ambiguous evidence.
- **Verified evidence**: 733 GSTINs map to >1 `entities` row; 502 GST company names map to
  >1 entity; all `entities.isin` NULL (live probes). Dashboard mapping priorities verified in
  `api/internal/repo/awards.go` (direct normalized → alias → canonical_name → gst_contractor
  → gst_company_name; ambiguity preserved, presentation-time only).
- **Corrected decision**: GRAPH_ONTOLOGY §4 is the single authoritative identity policy.
  R1–R5 create CANDIDATEs only; **all** CONFIRMED/REJECTED transitions are analyst actions;
  no rule combination (R1+R2, R1+R3, or any dual rule) auto-confirms; GSTIN equality must
  never be the second confirming rule. State transitions, re-review-on-new-evidence,
  one-confirmed-entity-per-vendor invariant (PostgreSQL control-table constraint or
  application transaction — design only), and dashboard-vs-graph distinction documented.
  No invented confidence thresholds (numeric `score`/`confidence` removed from the model).
- **Affected files**: GRAPH_ONTOLOGY.md (§1, §2, §4), ARCHITECTURE.md (§2.3), DATA_MAPPING.md
  (Conventions), DECISIONS.md (D6), PHASE_0_REPORT.md.
- **Remaining uncertainty**: none for the policy itself; future automatic-confirmation rules
  require explicit evidence-based justification and cannot use GSTIN equality.

## C2 — Contractor assignment ≠ bid participation (P0)

- **Original issue**: `tenders.contractor_v2_id` was mapped as `Vendor -[PARTICIPATED_IN]->
  Tender`, overstating an FK assignment as bid participation.
- **Verified evidence**: schema (single FK on the tender row to `contractor_v2`); no repo
  logic derives participant evidence from it; actual bid participation source is
  `tender_participants` (22,865 rows, `tender_id` FK, bid numbers, ranks).
- **Corrected decision**: `CONTRACTOR_ON_RECORD` (Vendor→Tender) — contractor of record on
  the tender row; never bid participation, submission, rank, winner flag, or winner evidence.
  `Bid -[SUBMITTED_BID]-> Tender` direction made consistent everywhere; bidder-network
  analysis derives only from `tender_participants` + supported identity links; winner
  evidence rules unchanged (`is_winner=true` alone stays weaker evidence).
- **Affected files**: GRAPH_ONTOLOGY.md (§1, §2, §8), DATA_MAPPING.md (§2.4),
  ARCHITECTURE.md (§2.8), DATABASE_AUDIT.md (§2.1, §3.5), DECISIONS.md (D10).
- **Remaining uncertainty**: whether any Phase-1 query needs an explicit Vendor↔Bid edge
  (documented as a future option, not assumed).

## C3 — Vertex ID convention: numeric hash id, gid canonical (P0)

- **Original issue**: HYDRADB_COMPATIBILITY example `MERGE (n {id: row.gid})` used the GID
  string as the vertex id, contradicting the numeric-hash convention in the ontology and
  mapping docs.
- **Verified evidence**: HydraDB docs state node ids are non-negative integers; `id` is the
  upsert key; string ids are not documented anywhere in the current engine docs.
- **Corrected decision**: one convention — `id` = versioned 63-bit hash of the **complete**
  `gid` string (UTF-8, unsigned output masked to 63 bits, `id_algo_v` recorded); `gid` =
  immutable canonical identity stored as a property; pre-write collision check aborts the
  batch and logs to a `tg_id_collisions`-style table (never a silent merge); hash migration
  re-derives ids with gid unchanged. Hash implementation deferred to Phase 1.
- **Affected files**: GRAPH_ONTOLOGY.md (§1, §3), HYDRADB_COMPATIBILITY.md (matrix rows,
  §3.1), DATA_MAPPING.md (Conventions, §7), DECISIONS.md (D5), spike/hydradb_smoke.md (step I).
- **Remaining uncertainty**: exact hash algorithm + version constant (Phase 1); string-id
  acceptance in the engine (probed in smoke test step I1); 63-bit int precision through the
  Go driver (step I2).

## C4 — Relationship identity and idempotency (P0)

- **Original issue**: docs implied MERGE-by-`id`-property on relationships plus batch dedupe
  were sufficient identity mechanisms ("verified MERGE edge semantics").
- **Verified evidence**: relationship MERGE appears only in batch form in official docs;
  uniqueness semantics under-specified; batch edge writes dedupe on `(src,dst,type)`.
- **Corrected decision**: HydraDB relationship uniqueness is **not assumed**. Row-based
  edges: deterministic row-keyed replay is the idempotency mechanism. Multi-observation
  facts: modeled as nodes (C6). Smoke-test step R added to prove or refute MERGE-by-id
  replay uniqueness.
- **Affected files**: HYDRADB_COMPATIBILITY.md (matrix, §3.2, §4.1, §5), GRAPH_ONTOLOGY.md
  (§3.4), DATA_MAPPING.md (§7), DECISIONS.md (D5/D12), spike/hydradb_smoke.md.
- **Remaining uncertainty**: the runtime answer itself (step R).

## C5 — GeM: contract rows, not sellers (P0)

- **Original issue**: one `Seller` node per `seller_details` row (4,078 pseudo-seller nodes).
- **Verified evidence**: live probe — `gem_seller_id`, `gst_in`, `pan`, `company_name` all
  **0 non-null of 4,078 rows**; `contract_no` is the only DB-unique key; no FK/overlap to the
  tender dataset.
- **Corrected decision**: `GemContract` node per row (`GEMC:v:` + uuid), `contract_no` as
  business dedup key; seller attributes remain on the contract (normally omitted per null
  rule); `IN_CATEGORY` (GemContract→GemCategory) from the verified category FK; no
  `SELLS_VIA_CONTRACT`, no Seller nodes, no link to the procurement domain. A future Seller
  model requires documented stable key + normalization + duplicate/ambiguity handling;
  sellers never merged on GSTIN-only or name-only equality.
- **Affected files**: GRAPH_ONTOLOGY.md (§1, §2, §7, §8), DATA_MAPPING.md (§5.1),
  DATABASE_AUDIT.md (§6.1), DECISIONS.md (D11), ARCHITECTURE.md (GeM island assumptions).
- **Remaining uncertainty**: no reliable seller identity exists in current data —
  seller-level analysis stays unanswerable until new evidence arrives.

## C6 — Co-bid evidence preservation (P0)

- **Original issue**: derived `CO_BID_WITH` (Bid→Bid) edges; batch `(src,dst,type)` dedupe
  would collapse 12 shared tenders between A and B into one edge, destroying evidence.
- **Verified evidence**: HydraDB batch edge dedupe documented on `(src,dst,type)`;
  relationship uniqueness not guaranteed (C4); 733 tender pairs share ≥2 participants
  (78,201 co-bid rows sampled) — the multi-observation case is common, not edge-case.
- **Corrected decision**: Option B — `CoBidObservation` node per (tender, pair)
  (`COBID:v:<tender_pk>:<min_pk>:<max_pk>`), two `CO_BID_IN` edges (`Bid→Obs` from each
  participant); per-tender evidence survives replay; repeat co-bidding = observation count.
  Alternatives A and C evaluated and rejected (unverified relationship uniqueness; derived
  state drift). No collusion labeling without independent evidence; participant↔vendor
  identity only through the R1–R5/analyst pipeline.
- **Affected files**: GRAPH_ONTOLOGY.md (§1, §2, §7, §8), DATA_MAPPING.md (§1.3, §7),
  HYDRADB_COMPATIBILITY.md (§3.3/3.4/3.8, matrix), ARCHITECTURE.md (§2.8),
  DATABASE_AUDIT.md (§2.3), DECISIONS.md (D12), spike/hydradb_smoke.md (step R2).
- **Remaining uncertainty**: fan-out/performance at max participants (85/tender) — bounded
  and batched; verify at Phase-1 load test.

## C7 — Per-table watermark strategy and batch semantics (P1)

- **Original issue**: coarse PK-vs-`updated_at` split; some tables were misclassified
  (e.g. `announcements_raw`/`company_events`/`entity_aliases` have no `updated_at`);
  no delete/late-update story; batch verification implied but unproven.
- **Verified evidence**: schema timestamp audit — all 15 sync-candidate tables have
  `created_at`; `updated_at` missing only in `entity_aliases`, `announcements_raw`,
  `company_events`; `tender_announcement_matches` has `reviewed_at`.
- **Corrected decision**: per-table table in ARCHITECTURE §2.6 — composite
  `(updated_at, id)` + bounded lookback for mutable tables; `MAX(id)` only for verified
  append-only; full re-projection for low-volume mutable tables. Six-step batch ordering
  (read → project → write → verify → mark → advance checkpoint); failure cases enumerated
  (crash-after-write, partial write, duplicate retry, late commit, update, delete, drift,
  unavailability); **at-least-once, not exactly-once**; reconciliation spec (§6) covering
  missing nodes/edges, stale props, deleted records, duplicates, unresolved references —
  node-count equality explicitly insufficient.
- **Affected files**: ARCHITECTURE.md (§2.6, §3, §5, §6), DATA_MAPPING.md (§7),
  DECISIONS.md (D9 update).
- **Remaining uncertainty**: mutable/append-only classification is schema+domain PROPOSED —
  confirm against live update traffic; delete handling awaits smoke-test step D.

## C8 — Financial precision policy (P1)

- **Original issue**: money mapped to floats only (`*_inr`/`*_crore`), implying exactness.
- **Verified evidence**: procurement money is varchar parsed via `NumValExpr`; financials
  are `numeric(20,2)` (Rs crore); HydraDB property values are int/float/bool/string only —
  no decimal type.
- **Corrected decision**: every monetary field stored as `<field>_exact` (canonical exact
  decimal string) + float analytics copy; units explicit per field (rupees vs Rs crore vs
  Rs/share); no silent conversion; exact arithmetic stays in PostgreSQL/Go; float sums are
  documented as non-exact. All mapping rows, node tables, and examples updated.
- **Affected files**: DATA_MAPPING.md (Conventions, §1–§3, §7), GRAPH_ONTOLOGY.md (§1, §7),
  HYDRADB_COMPATIBILITY.md (§3.6), DECISIONS.md (D13).
- **Remaining uncertainty**: none structural; unit confirmations (announcement-side tz,
  market-data units) already flagged in the original docs and unchanged.

## C9 — HydraDB vector capability statement (P1)

- **Original issue**: undifferentiated "No graph-side vector search" — conflates product
  marketing with self-hosted engine capability.
- **Verified evidence**: OSS repo (`hydra-db/hydradb` main) — 741 paths scanned, zero
  vector/embedding code paths; hydradb.com markets graph-memory/hybrid retrieval — product
  claim only; `pg_hydradb` extension needs `shared_preload_libraries` on the shared
  production PG — out of bounds.
- **Corrected decision**: classify per item — OSS engine: NOT AVAILABLE IN VERIFIED TARGET;
  commercial claims: PRODUCT CLAIM ONLY. The no-separate-vector-DB decision is re-grounded
  on PostgreSQL pgvector (preserved, not migrated) + relationship-driven question set — not
  on HydraDB's absence of vectors.
- **Affected files**: HYDRADB_COMPATIBILITY.md (vector row, §6), DECISIONS.md (D3),
  PHASE_0_REPORT.md.
- **Remaining uncertainty**: any future vector capability in the OSS engine (re-evaluate
  only on demonstrated requirement).

## C10 — PostgreSQL permission model and analyst auth (P1)

- **Original issue**: §7 recommended a "separate read-only role" while the service must
  write `tg_*` control tables — internally inconsistent; §2.10 left auth as a soft open
  item.
- **Verified evidence**: no auth layer exists in the API; the only DB role available today
  is `prod` (read/write) on shared production.
- **Corrected decision**: recommend Option B — one narrowly scoped `tendergraph_svc` role
  (SELECT on enumerated source tables; INSERT/UPDATE/SELECT on approved `tg_*` only; no
  writes to source tables). Option A (two roles) documented as fallback. **Analyst write
  endpoints blocked** until an approved authn/authz design exists; an API key is a temporary
  operational control, not a user authorization model. No roles changed in Phase 0.
- **Affected files**: ARCHITECTURE.md (§2.10, §7), PHASE_0_REPORT.md (blocker 3),
  DECISIONS.md (D14).
- **Remaining uncertainty**: the authn/authz design itself (Phase 1 decision).

## C11 — Consistency claims demoted to runtime-unverified (P1)

- **Original issue**: bookmarks / read-your-writes / strong consistency presented as
  verified working behavior in the sync path.
- **Verified evidence**: behavior is documented by the maintainers but the engine has never
  run in this environment (BLOCKED).
- **Corrected decision**: all consistency mechanisms labeled "documented, runtime-unverified";
  sync verification step gated on smoke-test step B; delete visibility gated on step D;
  no claim that every graph read is immediately consistent with every write.
- **Affected files**: ARCHITECTURE.md (§3, §6), HYDRADB_COMPATIBILITY.md (§5),
  spike/hydradb_smoke.md (steps B1/D1), PHASE_0_REPORT.md.
- **Remaining uncertainty**: all of it — pending the runtime smoke test (BLOCKED).

## Scope compliance of this pass

No application or frontend source changed; no PostgreSQL changes; no migrations; no
HydraDB implementation or ingestion; only `docs/phase0/` Markdown was modified. Pre-existing
modifications to `api/internal/srv/handlers_materiality.go` and
`frontend/src/pages/Materiality.tsx` remain untouched (hash-verified; see PHASE_0_REPORT.md
and the final git safety report).
