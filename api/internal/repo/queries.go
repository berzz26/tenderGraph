package repo

// Sort whitelists for the aggregation endpoints.

// ParticipantCore returns the standalone WITH prefix for the participant
// mapping (mapping CTEs + projected rows). Composed queries must use
// ParticipantCTEs instead and assemble their own WITH list.
func ParticipantCore() string {
	return "WITH " + ParticipantCTEs()
}

// ParticipantCTEs returns the mapping + rows CTE fragments (without WITH).
func ParticipantCTEs() string {
	return ParticipantMappingCTEs() + ParticipantRowsSelect
}

// AwardCore returns the standalone WITH prefix for the award mapping.
func AwardCore() string {
	return "WITH " + AwardCTEs()
}

// AwardCTEs returns the award mapping + rows CTE fragments (without WITH).
func AwardCTEs() string {
	return AwardMappingCTEs() + AwardRowsSelect
}

var ParticipantLeaderboardSorts = Sorts{
	"tenders": "unique_tenders",
	"records": "participant_records",
	"l1":      "l1_appearances",
	"bid":     "total_bid_value",
	"entity":  "canonical_name",
}

var AwardLeaderboardSorts = Sorts{
	"value":   "total_awarded_value",
	"tenders": "unique_awarded_tenders",
	"records": "award_records",
	"entity":  "canonical_name",
	"fy":      "fiscal_year",
}

var CompanySorts = Sorts{
	"entity":        "canonical_name",
	"market_cap":    "market_cap",
	"revenue":       "revenue",
	"fe_tenders":    "fe_tenders",
	"awarded":       "awarded_value_total",
	"award_tenders": "awarded_tenders",
	"records":       "fe_records",
	"l1":            "l1_count",
}

var MaterialitySorts = Sorts{
	"entity":                "canonical_name",
	"awarded":               "awarded_value_total",
	"bid":                   "bid_value_total",
	"revenue":               "revenue",
	"net_worth":             "net_worth",
	"market_cap":            "market_cap",
	"awarded_vs_revenue":    "awarded_vs_revenue",
	"awarded_vs_net_worth":  "awarded_vs_net_worth",
	"awarded_vs_market_cap": "awarded_vs_market_cap",
	"bid_vs_revenue":        "bid_vs_revenue",
}
