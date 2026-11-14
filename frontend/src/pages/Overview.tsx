import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtInt, fmtMonth, numOrNull, str } from '../api';
import { Kpi, Section, Loader, ErrorBanner, Badge } from '../components/ui';
import { BarChart, LineChart } from '../components/charts';

export default function Overview() {
  const [data, setData] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { api.get('intelligence/summary').then(setData).catch((e) => setError(e.message)); }, []);
  if (error) return <ErrorBanner message={error} />;
  if (!data) return <Loader label="Computing tender intelligence" />;
  const k = data.kpis || {};
  const outcomes = data.outcomes || [];
  const monthly = data.monthly || [];
  const companies = data.top_companies || [];
  return <>
    <div className="page-hero"><div><div className="eyebrow">VYUHA / INTELLIGENCE</div><h1>Tender-to-market signal desk</h1><p>Track L1 positions, outcome evidence, and listed-company exposure from observed procurement records.</p></div><div className="hero-note">Facts and derived classifications are kept separate.<br />AOC-confirmed means a winner match plus an AOC date.</div></div>
    <div className="kpi-grid">
      <Kpi label="L1 positions" value={fmtInt(k.total_l1_positions)} hint="Observed from financial rank" tone="accent" />
      <Kpi label="Awaiting decision" value={fmtInt(k.awaiting_decision)} hint="No reliable winner evidence" tone="warn" />
      <Kpi label="AOC pending" value={fmtInt(k.selected_aoc_pending)} hint="Winner match; AOC date missing" tone="warn" />
      <Kpi label="Confirmed AOCs" value={fmtInt(k.confirmed_aocs)} hint="Winner match + AOC date" tone="good" />
      <Kpi label="Not selected" value={fmtInt(k.not_selected)} hint="Another bidder identified" tone="bad" />
      <Kpi label="Listed positions" value={fmtInt(k.mapped_listed_positions)} hint={`${fmtInt(k.unresolved_positions)} unresolved mappings`} />
    </div>
    <div className="kpi-grid">
      <Kpi label="Aggregate L1 bid value" value={fmtCr(k.total_l1_bid_value)} hint="Raw financial bid values parsed" tone="accent" />
      <Kpi label="Confirmed awarded value" value={fmtCr(k.confirmed_awarded_value)} hint="Only AOC-confirmed positions" tone="good" />
    </div>
    <div className="grid-2">
      <Section title="Monthly L1 activity" sub="L1 positions by identification month"><LineChart points={monthly.map((row: any) => ({ label: fmtMonth(row.month_bucket), value: numOrNull(row.l1_positions) ?? 0 }))} /></Section>
      <Section title="Outcome distribution" sub="Classification is evidence-based, not a causal forecast"><BarChart items={outcomes.map((row: any) => ({ label: str(row.outcome_classification).replaceAll('_', ' '), value: numOrNull(row.position_count) ?? 0 }))} /></Section>
    </div>
    <Section title="Leading listed-company exposure" sub="Ranked by aggregate L1 bid value"><div className="company-signal-list">{companies.length ? companies.map((row: any) => <a className="signal-row" key={row.mapped_entity_id} href={`#!/company/${row.mapped_entity_id}`}><span><b>{str(row.listed_company_name)}</b><span className="muted mono">{str(row.ticker_nse) || '—'}</span></span><span><b>{fmtCr(row.l1_bid_value)}</b><span className="muted">{fmtInt(row.l1_positions)} L1</span></span></a>) : <div className="empty">No mapped listed-company positions</div>}</div></Section>
    <Section title="Method note" sub="How to read this dashboard"><div className="note">Winner evidence prioritizes normalized winning bid-number matches, then normalized awardee-name matches. A source `is_winner` flag remains visible but does not by itself create an award classification.</div><div className="status-strip"><Badge status="AOC_CONFIRMED" /><Badge status="L1_SELECTED_AOC_PENDING" /><Badge status="L1_AWAITING_DECISION" /><Badge status="L1_NOT_SELECTED" /></div></Section>
  </>;
}
