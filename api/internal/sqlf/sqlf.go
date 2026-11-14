// Package sqlf holds the shared SQL fragments used to build the entity-mapping
// queries. The normalization expression was validated against production data:
// it reproduces entities.normalized_name for 5973/5973 rows and reproduces the
// stored participant_normalized / awardee_normalized convention for ~99.8%.
//
// Stored normalized columns are always preferred: the expression below is only
// used as a fallback / for comparing against canonical_name and GST names.
package sqlf

import "fmt"

const suffixRx = `\y(pvt|private|ltd|limited|llp|limted|limte)\y`

// NormExpr returns the canonical normalization of the given column: lowercase,
// non-alphanumeric (except &) collapsed to single spaces, "&" expanded to "and",
// legal suffix tokens removed, whitespace collapsed.
func NormExpr(col string) string {
	return fmt.Sprintf(`TRIM(REGEXP_REPLACE(
        REGEXP_REPLACE(
          REGEXP_REPLACE(
            REGEXP_REPLACE(TRIM(REGEXP_REPLACE(LOWER(TRIM(%s)), '[^a-z0-9&]+', ' ', 'g')), '&', ' and ', 'g'),
          '%s', '', 'g'),
        '\s+', ' ', 'g'),
      '\s+', ' ', 'g'))`, col, suffixRx)
}

// NumValExpr casts a varchar money column (may contain thousands separators
// such as "4,610,386" western or "7,71,805.72" Indian grouping) to numeric.
// Leading '-' is preserved (a handful of stored bid values are negative).
func NumValExpr(col string) string {
	return fmt.Sprintf(`NULLIF(REGEXP_REPLACE(TRIM(%s), '[, ]', '', 'g'), '')::numeric`, col)
}

// FYExpr derives the Indian fiscal-year label (April–March) for a date column,
// e.g. 2026-05-01 -> "FY2027".
func FYExpr(dateCol string) string {
	return fmt.Sprintf(`('FY' || (CASE WHEN EXTRACT(MONTH FROM %s) >= 4
             THEN EXTRACT(YEAR FROM %s)::int + 1
             ELSE EXTRACT(YEAR FROM %s)::int END)::text)`, dateCol, dateCol, dateCol)
}
