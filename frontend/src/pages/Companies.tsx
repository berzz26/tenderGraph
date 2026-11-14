import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtInt, fmtDate, fmtRatio, fmtPercent, str, numOrNull } from '../api';
import { Section, Badge, Kpi, Loader, ErrorBanner, Empty, TenderLink, PageHeader } from '../components/ui';
import { DataTable, type Col } from '../components/DataTable';
import { BarChart, LineChart } from '../components/charts';

type Row = Record<string, any>;

const LIST_COLS: Col[] = [
  { key: 'canonical_name', label: 'Company', render: (r) => <b>{str(r.canonical_name)}</b>, csv: (r) => str(r.canonical_name) },
  { key: 'ticker_nse', label: 'NSE' },
  { key: 'ticker_bse', label: 'BSE' },
  { key: 'sector', label: 'Sector' },
  { key: 'revenue', label: 'Revenue (cr)', align: 'right', render: (r) => (r.revenue == null ? '-' : fmtCr(r.revenue * 1e7)), sortKey: 'revenue', csv: (r) => str(r.revenue) },
  { key: 'market_cap', label: 'Market cap (cr)', align: 'right', render: (r) => (r.market_cap == null ? '-' : fmtCr(r.market_cap * 1e7)), sortKey: 'market_cap', csv: (r) => str(r.market_cap) },
  { key: 'fe_records', label: 'Feval records', align: 'right', num: true, sortKey: 'records' },
  { key: 'l1_count', label: 'L1 appearances', align: 'right', num: true, sortKey: 'l1' },
  { key: 'awarded_tenders', label: 'Awarded tenders', align: 'right', num: true, sortKey: 'award_tenders' },
  { key: 'awarded_value_total', label: 'Awarded value', align: 'right', sortKey: 'awarded', render: (r) => (r.awarded_value_total == null ? '-' : fmtCr(r.awarded_value_total)), csv: (r) => str(r.awarded_value_total) },
];

export default function Companies({ detailId }: { detailId?: string }) {
  const [rows, setRows] = useState<Row[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [sort, setSort] = useState('awarded');
  const [dir, setDir] = useState<'asc' | 'desc'>('desc');
  const [q, setQ] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 60;

  useEffect(() => {
    if (detailId) return;
    let live = true;
    setLoading(true);
    api
      .get('companies', { page, page_size: pageSize, sort, dir, q })
      .then((d: any) => {
        if (!live) return;
        setRows(d.data);
        setTotal(d.total);
        setError(null);
      })
      .catch((e) => live && setError(e.message))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [page, sort, dir, q, detailId]);

  if (detailId) return <CompanyDetail entityId={detailId} />;

  return (
    <>
      <PageHeader eyebrow="RESEARCH / COMPANY INTELLIGENCE" title="Company intelligence" description="Move from canonical entity identity to financial statements, market snapshots, tender participation, and award exposure." meta={<>Entity master + procurement<br />Click a company to investigate</>} />
    <Section
      title="Company Intelligence"
      sub="listed entities enriched with procurement participation and awards — click a row for the full intelligence card"
    >
      <div className="filters">
        <input placeholder="Search company / ticker / ISIN…" value={q} onChange={(e) => { setPage(1); setQ(e.target.value); }} />
      </div>
      <DataTable
        columns={LIST_COLS}
        rows={rows}
        total={total}
        page={page}
        pageSize={pageSize}
        onPage={setPage}
        onSort={(k, d) => {
          setSort(k);
          setDir(d);
          setPage(1);
        }}
        sort={sort}
        dir={dir}
        loading={loading}
        error={error}
        onRowClick={(r) => (window.location.hash = `#!/company/${r.entity_id}`)}
        csvName="companies"
        dense
      />
    </Section>
    </>
  );
}

function CompanyDetail({ entityId }: { entityId: string }) {
  const [d, setD] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  const [market, setMarket] = useState<any>(null);
  const [procurement, setProcurement] = useState<any>(null);
  const [marketError, setMarketError] = useState<string | null>(null);
  const [marketRange, setMarketRange] = useState('1Y');
  const [marketFocus, setMarketFocus] = useState<{ from: string; to: string; date: string } | null>(null);
  const [marketMode, setMarketMode] = useState<'line' | 'area'>('line');
  const [showBenchmark, setShowBenchmark] = useState(false);
  const [showAllAwards, setShowAllAwards] = useState(false);
  useEffect(() => {
    api
      .get(`companies/${entityId}`)
      .then(setD)
      .catch((e: any) => setErr(e.message));
  }, [entityId]);
  useEffect(() => {
    const end = new Date();
    const start = new Date(end);
    const days: Record<string, number> = { '1M': 31, '3M': 92, '6M': 183, '1Y': 365, '3Y': 1095, '5Y': 1825 };
    const params = marketFocus
      ? { from: marketFocus.from, to: marketFocus.to }
      : (() => {
        if (marketRange !== 'ALL') start.setDate(start.getDate() - (days[marketRange] || 365)); else start.setFullYear(2010);
        return { from: start.toISOString().slice(0, 10), to: end.toISOString().slice(0, 10) };
      })();
    setMarketError(null);
    setMarket(null);
    api.get(`market-data/${entityId}`, params).then(setMarket).catch((e) => setMarketError(e.message));
  }, [entityId, marketRange, marketFocus]);
  useEffect(() => { api.get(`materiality/${entityId}`).then(setProcurement).catch(() => setProcurement(null)); }, [entityId]);

  if (err) return <ErrorBanner message={err} />;
  if (!d) return <Loader />;
  const e = d.entity;
  const mat = d.materiality;
  const feTot = d.financial_participation || {};
  const awTot = d.awarded_exposure || {};
  const cr = (v: any) => (numOrNull(v) === null ? '-' : fmtCr(Number(v) * 1e7));
  const financialHistory = d.financial_history || [];

  return (
    <>
      <button className="btn small" onClick={() => (window.location.hash = '#!/companies')}>
        ‹ Back to companies
      </button>
      <div className="company-head">
        <h2 className="company-name">{str(e.canonical_name)}</h2>
        <div className="company-tickers">
          {str(e.ticker_nse) && <span className="chip">NSE {str(e.ticker_nse)}</span>}
          {str(e.ticker_bse) && <span className="chip">BSE {str(e.ticker_bse)}</span>}
          {str(e.isin) && <span className="chip">ISIN {str(e.isin)}</span>}
          {str(e.sector) && <span className="chip">{str(e.sector)}</span>}
          <span className={`badge badge-${e.is_listed ? 'mapped' : 'unresolved'}`}>
            {e.is_listed ? 'listed' : 'unlisted'}
          </span>
        </div>
        {str(e.aliases) && <div className="muted small">Aliases: {str(e.aliases)}</div>}
        {str(e.gstins) && <div className="muted small">GSTINs: {str(e.gstins)}</div>}
      </div>

      <div className="kpi-grid">
        <Kpi label="Latest financial year" value={str(e.fiscal_year) || '-'} hint={fmtDate(e.financial_snapshot)} />
        <Kpi label="Revenue (cr)" value={cr(e.revenue)} hint={str(e.financial_source)} />
        <Kpi label="Net worth (cr)" value={cr(e.net_worth)} />
        <Kpi label="Market cap (cr)" value={cr(e.market_cap)} hint={fmtDate(e.market_snapshot)} />
        <Kpi label="Mapped feval records" value={fmtInt(feTot.participant_records) || '0'} hint={fmtInt(feTot.tender_count) + ' tenders'} />
        <Kpi label="Awarded value (mapped)" value={(numOrNull(awTot.awarded_value_total) ?? null) === null ? '-' : fmtCr(awTot.awarded_value_total)} tone="accent" hint={fmtInt(awTot.awarded_tenders) + ' tenders'} />
      </div>

      <MarketHistoryPanel market={market} error={marketError} range={marketRange} setRange={(value) => { setMarketFocus(null); setMarketRange(value); }} mode={marketMode} setMode={setMarketMode} benchmark={showBenchmark} setBenchmark={setShowBenchmark} />
      <Section title="Recent awarded contracts" sub="Top five latest contract updates. Select an award date to inspect the company’s stock around that award window." actions={<button className="btn small" onClick={() => setShowAllAwards((value) => !value)}>{showAllAwards ? 'Show top 5' : 'View all'}</button>}>
        <DataTable
          columns={awardCols((date) => {
            const selected = awardWindow(date);
            if (selected) setMarketFocus(selected);
          })}
          rows={showAllAwards ? (d.recent_awards || []) : (d.recent_awards || []).slice(0, 5)}
          total={(d.recent_awards || []).length}
          page={1}
          pageSize={showAllAwards ? 100 : 5}
          onPage={() => {}}
          csvName="company-awards"
          dense
        />
        {marketFocus && <div className="market-meta">Market window focused on award date {marketFocus.date}: {marketFocus.from} to {marketFocus.to}. Use the range selector in Market history to return to a rolling window.</div>}
      </Section>
      {procurement && <ProcurementWorkspace data={procurement} />}

      <div className="grid-2">
        <Section title="Exposure ratios" sub="awarded / bid value ÷ financial denominator (NULL when denominator missing or zero)">
          <table className="table dense">
            <thead>
              <tr><th>Ratio</th><th className="right">Value</th></tr>
            </thead>
            <tbody>
              {[
                ['Awarded / Revenue', mat?.awarded_vs_revenue],
                ['Awarded / Net worth', mat?.awarded_vs_net_worth],
                ['Awarded / Market cap', mat?.awarded_vs_market_cap],
                ['Bid value / Revenue', mat?.bid_vs_revenue],
                ['Bid value / Net worth', mat?.bid_vs_net_worth],
                ['Bid value / Market cap', mat?.bid_vs_market_cap],
              ].map(([k, v]) => (
                <tr key={String(k)}>
                  <td>{k}</td>
                  <td className="right">{fmtRatio(v)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="note">Bid value is financial-stage exposure — not confirmed order inflow.</div>
        </Section>
        <Section title="Financial history" sub="fiscal years ascending · values shown in INR crore">
          {financialHistory.length ? (
            <>
            <LineChart
              points={financialHistory.map((f: any) => ({ label: str(f.fiscal_year), value: numOrNull(f.revenue) === null ? 0 : Number(f.revenue) * 1e7 }))}
              lines={[
                { label: 'Revenue', color: '#4f8fe8', money: true, data: financialHistory.map((f: any) => numOrNull(f.revenue) === null ? null : Number(f.revenue) * 1e7) },
                { label: 'Net profit', color: '#21b78d', money: true, data: financialHistory.map((f: any) => numOrNull(f.net_profit) === null ? null : Number(f.net_profit) * 1e7) },
                { label: 'Net worth', color: '#7564e8', money: true, data: financialHistory.map((f: any) => numOrNull(f.net_worth) === null ? null : Number(f.net_worth) * 1e7) },
              ]}
              height={245}
            />
            <div className="table-wrap" style={{ maxHeight: 220 }}>
              <table className="table dense">
                <thead>
                  <tr><th>FY</th><th className="right">Revenue</th><th className="right">Net profit</th><th className="right">Net worth</th><th>Source</th></tr>
                </thead>
                <tbody>
                  {financialHistory.map((f: any, i: number) => (
                    <tr key={i}>
                      <td>{str(f.fiscal_year)}</td>
                      <td className="right">{cr(f.revenue)}</td>
                      <td className="right">{cr(f.net_profit)}</td>
                      <td className="right">{cr(f.net_worth)}</td>
                      <td className="muted">{str(f.source)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            </>
          ) : (
            <Empty text="No financial history for this entity" />
          )}
        </Section>
      </div>

      <Section title="Recent financial-stage participation (mapped records)">
        <DataTable
          columns={participationCols()}
          rows={d.recent_participation || []}
          total={(d.recent_participation || []).length}
          page={1}
          pageSize={100}
          onPage={() => {}}
          csvName="company-participation"
          dense
        />
      </Section>
    </>
  );
}

function ProcurementWorkspace({ data }: { data: any }) {
  const c = data.company || {};
  const [filter, setFilter] = useState('');
  const tenders = (data.tenders || []).filter((row: any) => !filter || row.outcome_classification === filter);
  const tenderColumns: Col[] = [
    { key: 'external_tender_id', label: 'Tender', render: (r) => <TenderLink id={r.external_tender_id} /> },
    { key: 'tender_title', label: 'Title', render: (r) => <span title={str(r.tender_title)}>{str(r.tender_title).slice(0, 48) || '-'}</span> },
    { key: 'organization', label: 'Organization' }, { key: 'category', label: 'Category' },
    { key: 'l1_bid_value', label: 'L1 bid', align: 'right', render: (r) => fmtCr(r.l1_bid_value) },
    { key: 'financial_evaluation_date', label: 'Evaluation', render: (r) => fmtDate(r.financial_evaluation_date) },
    { key: 'winner_evidence', label: 'Evidence' }, { key: 'outcome_classification', label: 'Outcome', render: (r) => <Badge status={r.outcome_classification} /> },
    { key: 'awarded_value', label: 'Awarded', align: 'right', render: (r) => fmtCr(r.awarded_value) }, { key: 'aoc_date', label: 'AOC', render: (r) => fmtDate(r.aoc_date) },
  ];
  const series = data.series || [];
  return <>
    <Section title="Government procurement" sub="Tender-level aggregation keeps pending L1 exposure separate from confirmed awards."><div className="kpi-grid"><Kpi label="Tenders" value={fmtInt(c.l1_tender_count)} hint="Mapped tender-level records" tone="accent" /><Kpi label="L1 bid exposure" value={fmtCr(c.l1_bid_exposure)} hint="Financial-stage bids" tone="warn" /><Kpi label="Confirmed awards" value={fmtCr(c.confirmed_award_exposure)} hint={`${fmtInt(c.confirmed_aoc_count)} AOCs`} tone="good" /><Kpi label="Pending awards" value={fmtInt(c.pending_award_count)} hint="Winner match, AOC missing" /><Kpi label="Not selected" value={fmtInt(c.not_selected_count)} hint="Reliable mismatch evidence" tone="bad" /></div></Section>
    <div className="grid-2"><Section title="Procurement activity" sub="Monthly tender-level exposure; awards are shown only when AOC evidence exists"><LineChart points={series.map((row: any) => ({ label: String(row.month_bucket).slice(0, 7), value: Number(row.l1_bid_value || 0) }))} lines={[{ label: 'L1 bid exposure', data: series.map((row: any) => numOrNull(row.l1_bid_value)), color: '#159a83', money: true }, { label: 'Confirmed awards', data: series.map((row: any) => numOrNull(row.confirmed_awarded_value)), color: '#2878d0', money: true }]} height={250} /></Section><Section title="Positions versus AOCs" sub="Counts by month, based on actual evaluation dates"><LineChart points={series.map((row: any) => ({ label: String(row.month_bucket).slice(0, 7), value: Number(row.tender_count || 0) }))} lines={[{ label: 'Tender positions', data: series.map((row: any) => numOrNull(row.tender_count)), color: '#6e7fe7' }, { label: 'Confirmed AOCs', data: series.map((row: any) => numOrNull(row.confirmed_aocs)), color: '#159a83' }]} height={250} /></Section></div>
    <div className="grid-2"><Section title="Organization exposure" sub="Top procuring organizations by L1 bid value"><BarChart money items={(data.organizations || []).slice(0, 8).map((row: any) => ({ label: str(row.organization), value: Number(row.l1_bid_value || 0) }))} /></Section><Section title="Category exposure" sub="Top tender categories by L1 bid value"><BarChart money items={(data.categories || []).slice(0, 8).map((row: any) => ({ label: str(row.category), value: Number(row.l1_bid_value || 0) }))} /></Section></div>
    <Section title="Tender history and event evidence" sub="Evaluation, winner evidence, and AOC dates are kept as separate observed fields."><div className="tabs"><button className={`tab ${!filter ? 'active' : ''}`} onClick={() => setFilter('')}>All</button><button className={`tab ${filter === 'AOC_CONFIRMED' ? 'active' : ''}`} onClick={() => setFilter('AOC_CONFIRMED')}>AOC confirmed</button><button className={`tab ${filter === 'L1_SELECTED_AOC_PENDING' ? 'active' : ''}`} onClick={() => setFilter('L1_SELECTED_AOC_PENDING')}>Pending AOC</button><button className={`tab ${filter === 'L1_NOT_SELECTED' ? 'active' : ''}`} onClick={() => setFilter('L1_NOT_SELECTED')}>Not selected</button><button className={`tab ${filter === 'L1_AWAITING_DECISION' ? 'active' : ''}`} onClick={() => setFilter('L1_AWAITING_DECISION')}>Awaiting decision</button></div><DataTable columns={tenderColumns} rows={tenders} total={tenders.length} page={1} pageSize={200} onPage={() => {}} csvName={`${str(c.canonical_name)}-procurement-history`} dense /></Section>
  </>;
}

function rollingAverage(values: (number | null)[], period: number): (number | null)[] {
  return values.map((_, index) => {
    if (index + 1 < period) return null;
    const window = values.slice(index + 1 - period, index + 1);
    return window.every((value) => value !== null) ? window.reduce((sum, value) => sum + (value as number), 0) / period : null;
  });
}

function exponentialAverage(values: (number | null)[], period: number): (number | null)[] {
  const result: (number | null)[] = [];
  const alpha = 2 / (period + 1);
  let previous: number | null = null;
  values.forEach((value, index) => {
    if (value === null) { result.push(null); return; }
    if (index + 1 < period) { result.push(null); return; }
    if (previous === null) previous = rollingAverage(values.slice(0, index + 1), period)[index];
    else previous = value * alpha + previous * (1 - alpha);
    result.push(previous);
  });
  return result;
}

function bollinger(values: (number | null)[], period = 20, multiplier = 2) {
  const middle = rollingAverage(values, period);
  const upper = values.map((_, index) => {
    if (index + 1 < period || middle[index] === null) return null;
    const window = values.slice(index + 1 - period, index + 1) as number[];
    const mean = middle[index] as number;
    const deviation = Math.sqrt(window.reduce((sum, value) => sum + (value - mean) ** 2, 0) / period);
    return mean + multiplier * deviation;
  });
  const lower = upper.map((value, index) => value === null || middle[index] === null ? null : 2 * (middle[index] as number) - value);
  return { middle, upper, lower };
}

function rsi(values: (number | null)[], period = 14): (number | null)[] {
  return values.map((_, index) => {
    if (index < period || values.slice(index - period, index + 1).some((value) => value === null)) return null;
    let gains = 0; let losses = 0;
    for (let i = index - period + 1; i <= index; i += 1) {
      const change = (values[i] as number) - (values[i - 1] as number);
      if (change >= 0) gains += change; else losses -= change;
    }
    if (losses === 0) return 100;
    return 100 - (100 / (1 + gains / period / (losses / period)));
  });
}

function MarketHistoryPanel({ market, error, range, setRange, mode, setMode, benchmark, setBenchmark }: { market: any; error: string | null; range: string; setRange: (value: string) => void; mode: 'line' | 'area'; setMode: (value: 'line' | 'area') => void; benchmark: boolean; setBenchmark: (value: boolean) => void }) {
  const [indicators, setIndicators] = useState<string[]>([]);
  const rows = market?.data || [];
  const points = rows.map((row: any) => ({ label: String(row.trading_date).slice(0, 10), value: Number(row.adjusted_close ?? row.close) })).filter((row: any) => Number.isFinite(row.value));
  const closes = points.map((point: any) => point.value as number);
  const closeSeries = closes as (number | null)[];
  const bands = bollinger(closeSeries);
  const benchmarkRows = market?.benchmark || [];
  const benchmarkMap = new Map<string, number>(benchmarkRows.map((row: any) => [String(row.trading_date).slice(0, 10), Number(row.adjusted_close ?? row.close)] as [string, number]));
  const base = points[0]?.value;
  const benchmarkBase = benchmarkRows[0] ? Number(benchmarkRows[0].adjusted_close ?? benchmarkRows[0].close) : null;
  const compare = benchmark && base && benchmarkBase ? points.map((point: any) => { const benchmarkValue = benchmarkMap.get(point.label); return benchmarkValue ? ((point.value / base) - (benchmarkValue / benchmarkBase)) * 100 : null; }) : [];
  const toggleIndicator = (name: string) => setIndicators((current) => current.includes(name) ? current.filter((item) => item !== name) : [...current, name]);
  const overlays = [
    ...(indicators.includes('SMA20') ? [{ label: 'SMA 20', data: rollingAverage(closeSeries, 20), color: '#f0a640' }] : []),
    ...(indicators.includes('SMA50') ? [{ label: 'SMA 50', data: rollingAverage(closeSeries, 50), color: '#8b6de8' }] : []),
    ...(indicators.includes('SMA200') ? [{ label: 'SMA 200', data: rollingAverage(closeSeries, 200), color: '#d25a66' }] : []),
    ...(indicators.includes('EMA20') ? [{ label: 'EMA 20', data: exponentialAverage(closeSeries, 20), color: '#159a83' }] : []),
    ...(indicators.includes('EMA50') ? [{ label: 'EMA 50', data: exponentialAverage(closeSeries, 50), color: '#2878d0' }] : []),
    ...(indicators.includes('BB') ? [{ label: 'BB upper', data: bands.upper, color: '#a5b3c5' }, { label: 'BB lower', data: bands.lower, color: '#a5b3c5' }] : []),
  ];
  const rsiLine = indicators.includes('RSI') ? [{ label: 'RSI 14', data: rsi(closeSeries, 14), color: '#d25a66' }] : [];
  return <Section title="Market history" sub="Live NSE end-of-day data fetched through Vyuha from TejHQ; no TejHQ market rows are persisted in PostgreSQL" actions={<div className="chart-controls"><select aria-label="Market range" value={range} onChange={(event) => setRange(event.target.value)}>{['1M', '3M', '6M', '1Y', '3Y', '5Y', 'ALL'].map((item) => <option key={item}>{item}</option>)}</select><select aria-label="Chart type" value={mode} onChange={(event) => setMode(event.target.value as 'line' | 'area')}><option value="line">Line</option><option value="area">Area</option></select><label className="chart-toggle"><input type="checkbox" checked={benchmark} onChange={(event) => setBenchmark(event.target.checked)} disabled={!benchmarkRows.length} /> NIFTY 50</label></div>}>
    {error ? <ErrorBanner message={error} /> : !market ? <Loader label="Loading market history" /> : !rows.length ? <Empty text="No live TejHQ OHLCV data is available for this mapped NSE symbol and date range." /> : <><div className="indicator-strip"><span className="muted small">Indicators</span>{['SMA20', 'SMA50', 'SMA200', 'EMA20', 'EMA50', 'BB', 'RSI'].map((item) => <label key={item} className="chart-toggle"><input type="checkbox" checked={indicators.includes(item)} onChange={() => toggleIndicator(item)} /> {item}</label>)}</div><LineChart points={points} lines={[...(benchmark && compare.length ? [{ label: 'Excess return vs NIFTY 50', data: compare, color: '#7564e8', area: mode === 'area' }] : [{ label: 'Price', data: points.map((point: any) => point.value), color: '#4f8fe8', area: mode === 'area' }]), ...overlays]} height={300} />{rsiLine.length > 0 && <LineChart points={points} lines={rsiLine} height={150} />}<div className="market-meta">{rows.length.toLocaleString('en-IN')} sessions · {String(rows[0].trading_date).slice(0, 10)} to {String(rows[rows.length - 1].trading_date).slice(0, 10)} · source: {str(rows[0].source) || 'unavailable'}</div><BarChart items={rows.slice(-30).map((row: any) => ({ label: String(row.trading_date).slice(5, 10), value: Number(row.volume || 0) }))} height={120} /></>}
  </Section>;
}

function participationCols(): Col[] {
  return [
    { key: 'external_tender_id', label: 'Tender ID', render: (r) => <TenderLink id={r.external_tender_id} /> },
    { key: 'participant_name', label: 'Participant' },
    { key: 'financial_rank', label: 'Rank' },
    { key: 'financial_status', label: 'Status' },
    { key: 'bid_value', label: 'Bid value', align: 'right', render: (r) => fmtCr(r.bid_value) },
    { key: 'mapping_method', label: 'Method', render: (r) => str(r.mapping_method) },
    { key: 'mapping_status', label: 'Status', render: (r) => <Badge status={r.mapping_status} /> },
  ];
}

function awardWindow(value: any): { from: string; to: string; date: string } | null {
  if (!value) return null;
  const date = String(value).slice(0, 10);
  const parsed = new Date(`${date}T00:00:00Z`);
  if (Number.isNaN(parsed.getTime())) return null;
  const from = new Date(parsed);
  const to = new Date(parsed);
  from.setUTCDate(from.getUTCDate() - 30);
  to.setUTCDate(to.getUTCDate() + 30);
  return { from: from.toISOString().slice(0, 10), to: to.toISOString().slice(0, 10), date };
}

function awardCols(onAwardDate?: (date: any) => void): Col[] {
  return [
    { key: 'external_tender_id', label: 'Tender ID', render: (r) => <TenderLink id={r.external_tender_id} /> },
    { key: 'awardee_name', label: 'Awardee' },
    { key: 'contract_date', label: 'Award date', render: (r) => r.contract_date ? <button type="button" className="date-link mono" onClick={() => onAwardDate?.(r.contract_date)} title="Focus market history around this award date">{fmtDate(r.contract_date)}</button> : '-' },
    { key: 'buyer_org', label: 'Buyer' },
    { key: 'title', label: 'Title' },
    { key: 'fiscal_year', label: 'FY' },
    { key: 'awarded_value', label: 'Awarded value', align: 'right', render: (r) => fmtCr(r.awarded_value) },
    { key: 'contractor_gstin', label: 'GSTIN' },
    { key: 'mapping_method', label: 'Method', render: (r) => str(r.mapping_method) },
  ];
}
