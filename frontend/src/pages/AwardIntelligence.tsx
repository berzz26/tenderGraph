import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtDate, fmtInt, fmtPercent, str } from '../api';
import { DataTable, type Col } from '../components/DataTable';
import { ErrorBanner, Kpi, Loader, Section, TenderLink, PageHeader } from '../components/ui';
import { BarChart } from '../components/charts';

const columns: Col[] = [
  { key: 'external_tender_id', label: 'Tender', render: (r) => <TenderLink id={r.external_tender_id} /> },
  { key: 'tender_title', label: 'Title', render: (r) => <span title={str(r.tender_title)}>{str(r.tender_title).slice(0, 52) || '-'}</span> },
  { key: 'listed_company_name', label: 'Listed company' },
  { key: 'participant_name', label: 'L1 participant' },
  { key: 'l1_bid_value_numeric', label: 'L1 bid', align: 'right', render: (r) => fmtCr(r.l1_bid_value_numeric) },
  { key: 'l1_identified_at', label: 'L1 identified', render: (r) => fmtDate(r.l1_identified_at) },
  { key: 'days_since_last_known_milestone', label: 'Days since milestone', align: 'right', num: true },
];

export default function AwardIntelligence() {
  const [data, setData] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);
  const [maturity, setMaturity] = useState(90);
  useEffect(() => { api.get('intelligence/awards', { maturity_days: maturity }).then(setData).catch((e) => setError(e.message)); }, [maturity]);
  if (error) return <ErrorBanner message={error} />;
  if (!data) return <Loader label="Loading award intelligence" />;
  const m = data.metrics || {};
  return <>
    <PageHeader eyebrow="PROCUREMENT / AWARD INTELLIGENCE" title="From L1 to confirmed award" description="Separate winner selection from AOC completion. Pending records remain active pipeline, not failed outcomes." meta={<label className="hero-control">Maturity cohort<select value={maturity} onChange={(e) => setMaturity(Number(e.target.value))}><option value={90}>90 days</option><option value={180}>180 days</option><option value={365}>365 days</option></select></label>} />
    <div className="kpi-grid"><Kpi label="Selected positions" value={fmtInt(m.selected_positions)} hint="Winner-confirmed L1s" tone="accent" /><Kpi label="Selection rate" value={fmtPercent(m.winner_selection_rate_pct)} hint="Selected ÷ resolved" /><Kpi label="AOC confirmed" value={fmtInt(m.aoc_confirmed_positions)} hint="Winner match + AOC date" tone="good" /><Kpi label="AOC completion" value={fmtPercent(m.aoc_completion_rate_pct)} hint="Confirmed ÷ selected" /><Kpi label="Pending AOC" value={fmtInt(m.selected_pending_aoc)} hint="No AOC date yet" tone="warn" /><Kpi label="Not selected" value={fmtInt(m.not_selected_positions)} hint="Another bidder identified" tone="bad" /></div>
    <Section title="Mature cohort context" sub={`Only positions identified at least ${data.maturity_days} days ago are included in this cohort count.`}><div className="grid-2"><BarChart items={[{ label: 'Selected', value: Number(m.mature_selected_positions || 0) }, { label: 'Not selected', value: Number(m.mature_not_selected_positions || 0) }]} /><div className="note">Selection rate excludes awaiting-decision positions. AOC completion is a separate measure and is currently limited by the availability of AOC dates in the source data.</div></div></Section>
    <Section title="Pending AOC tracker" sub="Days are measured from the latest available milestone, not necessarily the award date."><DataTable columns={columns} rows={data.pending || []} total={(data.pending || []).length} page={1} pageSize={200} onPage={() => {}} csvName="pending-aoc" dense /></Section>
  </>;
}
