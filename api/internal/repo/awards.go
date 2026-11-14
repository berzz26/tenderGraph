package repo

import (
	"vyuha/api/internal/sqlf"
)

const awardsNonEmpty = `COALESCE(tem.awardee_normalized, '') <> ''`

// AwardBaseFilter: a record is an awarded/AOC record when an awardee exists.
const AwardBaseFilter = `(tem.awardee_name IS NOT NULL AND tem.awardee_name <> '')`

// AwardMappingCTEs returns the deterministic entity-resolution CTEs for
// tender_extracted_meta awardee records.
//
// Matching priority (never fuzzy):
//
//	1 direct_normalized_match  awardee_normalized = entities.normalized_name
//	2 alias_match              awardee_normalized = entity_aliases.normalized_alias
//	3 canonical_name_match     normalized(awardee_name) = normalized(entities.canonical_name)
//	4 gst_contractor_match     the tender's contractor_v2 identity, matched into
//	                           the entity master by exact GSTIN equality — the
//	                           GSTIN match is evidence in itself; the listed
//	                           company is asserted by upto stream entity_gst.
//	5 gst_company_name_match   normalized(awardee_name) = normalized(entity_gst.company_name)
//
// Notes:
//   - contractor (v1) is empty in production; contractor_v2 is used.
//   - seller_details carries no GSTINs in production and is not joinable to
//     tenders, so it cannot contribute; BOQ-derived GST would arrive through
//     the same entity_gst.gstin path.
//   - The awardee name is never treated as proof that the listed entity is the
//     contractor's parent; GSTIN evidence is required for methods 4/5.
func AwardMappingCTEs() string {
	return `
  acand AS (
    SELECT tem.id AS meta_id, e.id AS entity_id, 1 AS prio
    FROM tender_extracted_meta tem
    JOIN entities e ON e.normalized_name = tem.awardee_normalized
    WHERE ` + AwardBaseFilter + ` AND ` + awardsNonEmpty + `

    UNION ALL

    SELECT tem.id, e.id, 2
    FROM tender_extracted_meta tem
    JOIN entity_aliases ea ON ea.normalized_alias = tem.awardee_normalized
    JOIN entities e ON e.id = ea.entity_id
    WHERE ` + AwardBaseFilter + ` AND ` + awardsNonEmpty + `

    UNION ALL

    SELECT tem.id, e.id, 3
    FROM tender_extracted_meta tem
    JOIN entities e ON ` + sqlf.NormExpr("e.canonical_name") + ` = ` + sqlf.NormExpr("tem.awardee_name") + `
    WHERE ` + AwardBaseFilter + `

    UNION ALL

    SELECT tem.id, eg.entity_id, 4
    FROM tender_extracted_meta tem
    JOIN tenders t          ON t.id = tem.tender_id
    JOIN contractor_v2 cv   ON cv.id = t.contractor_v2_id
                           AND cv.gst IS NOT NULL AND cv.gst <> ''
    JOIN entity_gst eg      ON eg.gstin = cv.gst
    WHERE ` + AwardBaseFilter + `

    UNION ALL

    SELECT tem.id, eg.entity_id, 5
    FROM tender_extracted_meta tem
    JOIN entity_gst eg ON ` + sqlf.NormExpr("eg.company_name") + ` = ` + sqlf.NormExpr("tem.awardee_name") + `
    WHERE ` + AwardBaseFilter + `
  ),
  adedup AS (SELECT DISTINCT meta_id, entity_id, prio FROM acand),
  abp AS (SELECT meta_id, MIN(prio) AS best_prio FROM adedup GROUP BY meta_id),
  abest AS (
    SELECT d.meta_id, d.entity_id,
           COUNT(*) OVER (PARTITION BY d.meta_id) AS candidate_count
    FROM adedup d
    JOIN abp ON abp.meta_id = d.meta_id AND abp.best_prio = d.prio
  ),
  aagg AS (
    SELECT b.meta_id,
           CASE WHEN b.candidate_count = 1 THEN (MIN(b.entity_id::text))::uuid END AS resolved_entity_id,
           MAX(b.candidate_count)                                    AS candidate_count
    FROM abest b
    GROUP BY b.meta_id, b.candidate_count
  )`
}

// AwardRowsSelect projects the fully-resolved award row set (one row per
// tender_extracted_meta record that has an awardee). tender_extracted_meta is
// UNIQUE on tender_id, so no join can multiply tender values.
var AwardRowsSelect = `,
  arows AS (
    SELECT
      tem.id                                    AS meta_id,
      tem.tender_id,
      t.tender_id                               AS external_tender_id,
      t.lifecycle_stage,
      t.organization,
      tem.org_name                              AS buyer_org,
      tem.title,
      tem.awardee_name,
      tem.awardee_normalized,
      ` + sqlf.NumValExpr("tem.awarded_value") + `        AS awarded_value,
      tem.contract_date,
      tem.contract_updated_date,
      ` + sqlf.FYExpr("tem.contract_date") + `            AS fiscal_year,
      tem.created_at                            AS record_created_at,
      tem.winning_bid_number,
      CASE
        WHEN aagg.resolved_entity_id IS NOT NULL THEN 'mapped'
        WHEN aagg.candidate_count > 1            THEN 'ambiguous'
        ELSE 'unresolved'
      END                                       AS mapping_status,
      CASE abp.best_prio
        WHEN 1 THEN 'direct_normalized_match'
        WHEN 2 THEN 'alias_match'
        WHEN 3 THEN 'canonical_name_match'
        WHEN 4 THEN 'gst_contractor_match'
        WHEN 5 THEN 'gst_company_name_match'
        ELSE NULL
      END                                       AS mapping_method,
      aagg.candidate_count,
      en.id                                     AS entity_id,
      en.canonical_name,
      en.ticker_nse,
      en.ticker_bse,
      en.isin,
      en.is_listed,
      en.sector,
      cv.business_name                          AS contractor_business_name,
      cv.gst                                    AS contractor_gstin,
      acn.candidate_names,
      (SELECT string_agg(DISTINCT eg2.gstin, ', ')
         FROM entity_gst eg2 WHERE eg2.entity_id = en.id) AS entity_gstins
    FROM tender_extracted_meta tem
    JOIN tenders t               ON t.id = tem.tender_id
    LEFT JOIN contractor_v2 cv   ON cv.id = t.contractor_v2_id
    LEFT JOIN aagg               ON aagg.meta_id = tem.id
    LEFT JOIN abp                ON abp.meta_id = tem.id
    LEFT JOIN entities en        ON en.id = aagg.resolved_entity_id
    LEFT JOIN LATERAL (
      SELECT string_agg(DISTINCT x.canonical_name, ' | ') AS candidate_names
      FROM abest b JOIN entities x ON x.id = b.entity_id
      WHERE b.meta_id = tem.id
    ) acn ON acn.candidate_names IS NOT NULL
    WHERE tem.awardee_name IS NOT NULL AND tem.awardee_name <> ''
  )`

var AwardSorts = Sorts{
	"contract_date": "contract_date",
	"aoc":           "contract_date",
	"awarded_value": "awarded_value",
	"awardee":       "awardee_name",
	"entity":        "canonical_name",
	"tender":        "external_tender_id",
	"buyer":         "buyer_org",
	"status":        "mapping_status",
	"method":        "mapping_method",
	"fy":            "fiscal_year",
}
