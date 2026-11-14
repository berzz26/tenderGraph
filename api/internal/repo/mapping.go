package repo

import (
	"strings"

	"vyuha/api/internal/sqlf"
)

// QB is a tiny dynamic-SQL helper: appends placeholder arguments with
// automatically numbered $n references. All user input reaches PostgreSQL only
// through these parameters.
type QB struct {
	SB   strings.Builder
	Args []any
}

func (q *QB) P(sql string, args ...any) {
	q.SB.WriteString(sql)
	for _, a := range args {
		q.Args = append(q.Args, a)
		q.SB.WriteString("$" + itoa(len(q.Args)))
	}
}

func (q *QB) W(sql string) {
	q.SB.WriteString(sql)
}

func (q *QB) SQL() string { return q.SB.String() }

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// SortKey is one whitelisted ORDER BY column.
type SortKey struct {
	Expr string
	OK   bool
}

// Sorts maps user-supplied sort keys to whitelisted SQL expression lists.
type Sorts map[string]string // key -> "expr, expr2" fragments used in ORDER BY

// OrderBy resolves the requested sort (defaultSort when invalid/missing).
func OrderBy(sortKey, sortDir string, sorts Sorts, def string) string {
	if s, ok := sorts[sortKey]; ok {
		dir := "ASC"
		if strings.EqualFold(sortDir, "desc") {
			dir = "DESC"
		}
		return s + " " + dir + " NULLS LAST"
	}
	return def
}

// LikeArg returns a match-everywhere ILIKE parameter or nil when the search
// text is empty. P() skips nil arguments.
func LikeArg(text string) any {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	return "%" + strings.ToLower(t) + "%"
}

// ParticipantsBaseFilter mirrors the spec: a record is a financial-evaluation
// record when it carries a financial rank, status or winner flag.
const ParticipantsBaseFilter = `(tp.financial_rank IS NOT NULL OR tp.financial_status IS NOT NULL OR tp.is_winner = TRUE)`

const participantsNonEmpty = `COALESCE(tp.participant_normalized, '') <> ''`

// ParticipantMappingCTEs returns the deterministic entity-resolution CTEs for
// tender_participants (candidates -> best priority -> per-participant aggregate).
//
// Matching priority (never fuzzy):
//
//	1 direct_normalized_match  participant_normalized = entities.normalized_name
//	2 alias_match              participant_normalized = entity_aliases.normalized_alias
//	3 canonical_name_match     participant_normalized = normalized(entities.canonical_name)
//	4 gst_company_name_match   participant_normalized = normalized(entity_gst.company_name)
//
// A participant matching MORE THAN ONE entity at the winning priority is kept
// ambiguous (candidate_count > 1): no entity is silently picked.
func ParticipantMappingCTEs() string {
	return `
  cand AS (
    SELECT tp.id AS participant_id, e.id AS entity_id, 1 AS prio
    FROM tender_participants tp
    JOIN entities e ON e.normalized_name = tp.participant_normalized
    WHERE ` + ParticipantsBaseFilter + ` AND ` + participantsNonEmpty + `

    UNION ALL

    SELECT tp.id, e.id, 2
    FROM tender_participants tp
    JOIN entity_aliases ea ON ea.normalized_alias = tp.participant_normalized
    JOIN entities e ON e.id = ea.entity_id
    WHERE ` + ParticipantsBaseFilter + ` AND ` + participantsNonEmpty + `

    UNION ALL

    SELECT tp.id, e.id, 3
    FROM tender_participants tp
    JOIN entities e ON ` + sqlf.NormExpr("e.canonical_name") + ` = tp.participant_normalized
    WHERE ` + ParticipantsBaseFilter + ` AND ` + participantsNonEmpty + `

    UNION ALL

    SELECT tp.id, eg.entity_id, 4
    FROM tender_participants tp
    JOIN entity_gst eg ON ` + sqlf.NormExpr("eg.company_name") + ` = tp.participant_normalized
    WHERE ` + ParticipantsBaseFilter + ` AND ` + participantsNonEmpty + `
  ),
  dedup AS (SELECT DISTINCT participant_id, entity_id, prio FROM cand),
  bp AS (SELECT participant_id, MIN(prio) AS best_prio FROM dedup GROUP BY participant_id),
  best AS (
    SELECT d.participant_id, d.entity_id,
           COUNT(*) OVER (PARTITION BY d.participant_id) AS candidate_count
    FROM dedup d
    JOIN bp ON bp.participant_id = d.participant_id AND bp.best_prio = d.prio
  ),
  agg AS (
    SELECT b.participant_id,
           CASE WHEN b.candidate_count = 1 THEN (MIN(b.entity_id::text))::uuid END AS resolved_entity_id,
           (MAX(b.entity_id::text))::uuid                                AS candidate_max_entity,
           MAX(b.candidate_count)                                    AS candidate_count
    FROM best b
    GROUP BY b.participant_id, b.candidate_count
  )`
}

// ParticipantRowsSelect projects the fully-resolved financial-evaluation row
// set (one row per tender_participants record).
var ParticipantRowsSelect = `,
  prow AS (
    SELECT
      tp.id                                   AS participant_id,
      tp.tender_id,
      t.tender_id                             AS external_tender_id,
      t.lifecycle_stage,
      t.organization,
      tem.org_name                            AS buyer_org,
      tem.title,
      tp.participant_name,
      tp.participant_normalized,
      tp.financial_rank,
      tp.financial_status,
      tp.financial_remarks,
      ` + sqlf.NumValExpr("tp.financial_bid_value") + ` AS bid_value,
      tp.is_winner,
      tp.submitted_date,
      tp.created_at                           AS record_created_at,
      CASE
        WHEN agg.resolved_entity_id IS NOT NULL THEN 'mapped'
        WHEN agg.candidate_count > 1            THEN 'ambiguous'
        ELSE 'unresolved'
      END                                     AS mapping_status,
      CASE bp.best_prio
        WHEN 1 THEN 'direct_normalized_match'
        WHEN 2 THEN 'alias_match'
        WHEN 3 THEN 'canonical_name_match'
        WHEN 4 THEN 'gst_company_name_match'
        ELSE NULL
      END                                     AS mapping_method,
      agg.candidate_count,
      en.id                                   AS entity_id,
      en.canonical_name,
      en.ticker_nse,
      en.ticker_bse,
      en.isin,
      en.is_listed,
      en.sector,
      cn.candidate_names,
      ('FY' || (CASE WHEN EXTRACT(MONTH FROM COALESCE(tp.submitted_date, tp.created_at)) >= 4
               THEN EXTRACT(YEAR FROM COALESCE(tp.submitted_date, tp.created_at))::int + 1
               ELSE EXTRACT(YEAR FROM COALESCE(tp.submitted_date, tp.created_at))::int
          END)::text)                         AS fiscal_year
    FROM tender_participants tp
    JOIN tenders t           ON t.id = tp.tender_id
    LEFT JOIN tender_extracted_meta tem ON tem.tender_id = t.id
    LEFT JOIN agg            ON agg.participant_id = tp.id
    LEFT JOIN bp             ON bp.participant_id = tp.id
    LEFT JOIN entities en    ON en.id = agg.resolved_entity_id
    LEFT JOIN LATERAL (
      SELECT string_agg(DISTINCT x.canonical_name, ' | ') AS candidate_names
      FROM best b JOIN entities x ON x.id = b.entity_id
      WHERE b.participant_id = tp.id
    ) cn ON cn.candidate_names IS NOT NULL
  )`

// ParticipantListSort whitelist.
var ParticipantSorts = Sorts{
	"participant": "participant_name",
	"entity":      "canonical_name",
	"tender":      "t.tender_id",
	"rank":        "financial_rank",
	"bid_value":   "bid_value",
	"status":      "mapping_status",
	"method":      "mapping_method",
	"submitted":   "submitted_date",
}
