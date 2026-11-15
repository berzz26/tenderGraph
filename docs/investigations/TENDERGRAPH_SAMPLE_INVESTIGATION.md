# Sample TenderGraph Investigation

## Investigation question

Which observed participant records appear together in the same procurement event, and what can be established from the underlying tender and bid evidence?

## Starting records

- BID:v:1760 — Morrisons Lifecare Pvt Ltd; observed key morrisons lifecare.
- BID:v:1710 — 3M INDIA LIMITED; observed key 3m india.

These are Bid records from tender_participants. Their observed names and normalized keys are not, by themselves, legal-identity confirmation.

## Discovered relationship

The records share one tender-specific co-bidding observation:

- Observation tender: TENDER:v:16813.
- External tender: 2026_NIMHN_894353_2.
- Tender title: Rate Contract for Surgicals.
- Procuring organization: National Institute of Mental Health and Neuro Sciences.
- Graph shape: BID:v:1760 -> CO_BID_IN <- BID:v:1710.
- Source evidence: tender_participants PKs 1760 and 1710; tenders PK 16813.

The observation is an occurrence of two participant records in one tender. It is not a direct company-to-company edge and is not evidence of collusion.

## Quantitative evidence

| Measure | Result |
|---|---:|
| Bid 1760 distinct tenders | 1 |
| Bid 1710 distinct tenders | 3 |
| Shared tenders | 1 |
| Jaccard similarity | 0.3333 |
| Coverage of Bid 1760 | 1.0000 |
| Coverage of Bid 1710 | 0.3333 |

Formulas:

    Jaccard = shared tenders / union of distinct tenders
    Coverage A = shared tenders / A's distinct tenders
    Coverage B = shared tenders / B's distinct tenders

Bid 1710's other observed tender records are 2026_NIMHN_894339_2 and 2025_TMC_847669_1. Bid 1760 has one observed tender record in the current profile response.

## Underlying tender evidence

Tender 2026_NIMHN_894353_2 is an open Goods tender for a rate contract at NIMHANS, Bengaluru. The tender endpoint returned:

- Bid 1710: financial value 18024891, financial rank 1, status Accepted-Finance, winner flag false.
- Bid 1760: financial value 2114700, financial rank 1, status Accepted-Finance, winner flag false.
- No extracted award record.
- No awardee name, contract date or winning bid number.

The participant records are source-backed participation and financial-evaluation observations. They do not establish an award or winner relationship.

## Findings

Observed fact: the two Bid records appeared together in one tender issued by the National Institute of Mental Health and Neuro Sciences.

Derived statistic: the shared tender is the full observed history of Bid 1760 in this dataset slice, while it represents one of three distinct tenders for Bid 1710.

The evidence supports navigating from either participant to the same tender and inspecting both source participant records. It does not support concluding that the records represent the same legal company, that either participant won, or that the observed relationship indicates wrongdoing.

## Uncertainty and limitations

- The graph and pair calculations operate on participant records and stored normalized keys.
- No extracted award evidence was returned for the investigated tender.
- Winner flags were false for both selected participant records.
- Bid 1710 had a mapped corporate candidate in the tender response (3M India Ltd), but this investigation does not convert that mapping into an analyst-confirmed identity assertion.
- Bid 1760 had no mapped corporate entity in the tender response.
- The result is bounded by the current source records and graph/API limits.

## Conclusion

TenderGraph makes the shared procurement observation inspectable: two concrete participant records, one tender, the procuring organization, bid values, ranks and source references can be followed through the graph and tender views. The defensible conclusion is limited to observed co-participation and descriptive overlap. Corporate identity, award outcome and any interpretation beyond those facts remain unresolved.
