package repo

import "vyuha/api/internal/sqlf"

// TenderDetailSQL resolves a single tender (by external tender_id, falling back
// to the internal integer id when the argument is numeric) with its
// tender_extracted_meta record and the assigned contractor_v2 identity.
var TenderDetailSQL = `
SELECT
  t.id                                     AS tender_internal_id,
  t.tender_id                             AS external_tender_id,
  t.lifecycle_stage,
  t.organization,
  t.website_url,
  t.created_at                            AS tender_created_at,
  t.updated_at                            AS tender_updated_at,
  tem.id                                  AS meta_id,
  tem.org_name,
  tem.title,
  tem.ref_no,
  tem.type,
  tem.category,
  tem.product_category,
  tem.location,
  tem.published_date,
  tem.bid_submission_start_date,
  tem.bid_submission_end_date,
  tem.bid_opening_date,
  tem.contract_type,
  tem.form_of_contract,
  tem.no_of_covers,
  tem.pincode,
  tem.tender_value                        AS tender_value_raw,
  ` + sqlf.NumValExpr("tem.tender_value") + ` AS tender_value,
  tem.emd_value,
  tem.description,
  tem.awardee_name,
  tem.awardee_normalized,
  ` + sqlf.NumValExpr("tem.awarded_value") + ` AS awarded_value,
  tem.awarded_value                       AS awarded_value_raw,
  tem.contract_date,
  tem.contract_updated_date,
  tem.financial_evaluation_date,
  tem.aoc_date,
  tem.winning_bid_number,
  tem.inv_auth_name,
  cv.id                                   AS contractor_v2_id,
  cv.business_name                        AS contractor_business_name,
  cv.gst                                  AS contractor_gstin,
  cv.legal_status                         AS contractor_legal_status,
  cv.address                              AS contractor_address
FROM tenders t
LEFT JOIN tender_extracted_meta tem ON tem.tender_id = t.id
LEFT JOIN contractor_v2 cv          ON cv.id = t.contractor_v2_id
WHERE t.tender_id = $1`

// TenderFilesSQL lists the tender's document files with per-file BOQ status.
// $1 is the internal tenders.id (tender_files.tender_id is an integer FK).
const TenderFilesSQL = `
SELECT tf.id, tf.file_name, tf.file_type, tf.file_extension, tf.file_size_mb,
       tf.is_digitized, tf.is_boq_parsed_success, tf.pdf_parse_state,
       tf.parse_error_message, tf.cloud_file_name, tf.created_at,
       (SELECT COUNT(*) FROM boq_items bi WHERE bi.tender_file_id = tf.id) AS boq_item_count
FROM tender_files tf
WHERE tf.tender_id = $1 AND tf.is_deleted = FALSE
ORDER BY tf.created_at, tf.id`

// TenderBoqItemsSQL returns BOQ line items for one tender (across parsed
// files). $1 is the internal tenders.id, like TenderFilesSQL.
const TenderBoqItemsSQL = `
SELECT bi.sl_no, bi.item_description, bi.quantity, bi.units, bi.rate,
       bi.rrr, bi.awardee_rate, bi.awardee_amount
FROM boq_items bi
JOIN tender_files tf ON tf.id = bi.tender_file_id
WHERE tf.tender_id = $1
ORDER BY tf.id, bi.id`
