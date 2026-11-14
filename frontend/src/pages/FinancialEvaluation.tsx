import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtInt, fmtDate, fmtMonth, numOrNull, str, methodLabel } from '../api';
import { Section, Badge, TenderLink, PageHeader, Kpi } from '../components/ui';
import { BarChart, LineChart } from '../components/charts';
import { DataTable, type Col } from '../components/DataTable';

type Row = Record<string, any>;

const COLUMNS: Col[] = [
  { key: 'participant_name', label: 'Participant', render: (r) => <b>{str(r.participant_name)}</b>, sortKey: 'participant', csv: (r) => str(r.participant_name) },
  { key: 'participant_normalized', label: 'Normalized', csv: (r) => str(r.participant_normalized) },
  { key: 'canonical_name', label: 'Canonical company' },
  { key: 'ticker_nse', label: 'NSE' },
  { key: 'ticker_bse', label: 'BSE' },
  { key: 'external_tender_id', label: 'Tender ID', render: (r) => <TenderLink id={r.external_tender_id} />, csv: (r) => str(r.external_tender_id) },
  { key: 'buyer_org', label: 'Organization' },
  { key: 'title', label: 'Title' },
  { key: 'financial_rank', label: 'Rank', sortKey: 'rank' },
  { key: 'financial_status', label: 'Status' },
  { key: 'financial_remarks', label: 'Remarks' },
  { key: 'bid_value', label: 'Bid value', align: 'right', num: true, sortKey: 'bid_value', render: (r) => (r.bid_value == null ? '-' : fmtCr(r.bid_value)), csv: (r) => str(r.bid_value) },
  { key: 'is_winner', label: 'Winner', render: (r) => (r.is_winner ? <span className="win-mark">YES</span> : <span className="muted">-</span>), csv: (r) => (r.is_winner ? 'true' : 'false') },
  { key: 'mapping_method', label: 'Method', sortKey: 'method', render: (r) => methodLabel(r.mapping_method), csv: (r) => str(r.mapping_method) },
  { key: 'mapping_status', label: 'Status', sortKey: 'status', render: (r) => <Badge status={r.mapping_status} />, csv: (r) => str(r.mapping_status) },
  { key: 'submitted_date', label: 'Submitted', sortKey: 'submitted', render: (r) => fmtDate(r.submitted_date), csv: (r) => str(r.submitted_date) },
];

export default function FinancialEvaluation() {
  const [rows, setRows] = useState<Row[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [sort, setSort] = useState('submitted');
  const [dir, setDir] = useState<'asc' | 'desc'>('desc');
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('');
  const [method, setMethod] = useState('');
  const [winner, setWinner] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [summary, setSummary] = useState<any>(null);
  const pageSize = 50;

  useEffect(() => {
    let live = true;
    setLoading(true);
    api
      .get('financial-evaluation', { page, page_size: pageSize, sort, dir, q, mapping_status: status, method, winner })
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
  }, [page, sort, dir, q, status, method, winner]);
  useEffect(() => { api.get('financial-evaluation/summary').then(setSummary).catch(() => setSummary(null)); }, []);

  return (
    <>
      <PageHeader eyebrow="PROCUREMENT / FINANCIAL EVALUATION" title="Financial evaluation ledger" description="Investigate bidder rankings, L1 positions, financial bid values, and deterministic company mappings across evaluated tenders." meta={<>Participant-level evidence<br />Server-side search and pagination</>} />
      {summary?.totals && <><div className="kpi-grid"><Kpi label="Evaluated records" value={fmtInt(summary.totals.records)} hint={`${fmtInt(summary.totals.unique_tenders)} tenders`} tone="accent" /><Kpi label="L1 / winner flags" value={fmtInt(summary.totals.winner_records || summary.totals.mapped_listed_records)} hint="Observed source evidence" /><Kpi label="Mapped listed" value={fmtInt(summary.totals.mapped_listed_records)} hint={`${fmtInt(summary.totals.unique_mapped_entities)} entities`} tone="good" /><Kpi label="Bid exposure" value={fmtCr(summary.totals.mapped_bid_value_total)} hint="Mapped records only" tone="accent" /><Kpi label="Unresolved records" value={fmtInt(summary.totals.unresolved_records)} hint="Review queue" tone="warn" /></div><div className="grid-2"><Section title="Evaluation activity" sub="Participant records by month"><LineChart points={(summary.participants_by_month || []).map((row: any) => ({ label: fmtMonth(row.month_bucket), value: numOrNull(row.participant_records) || 0 }))} /></Section><Section title="Financial rank distribution" sub="Raw rank values in the evaluated dataset"><BarChart items={(summary.rank_distribution || []).slice(0, 10).map((row: any) => ({ label: str(row.financial_rank), value: numOrNull(row.records) || 0 }))} /></Section></div></>}
      <Section title="Financial Evaluation — participant mapping" sub="deterministic mapping of tender_participants to the listed-company master">
        <div className="filters">
          <input placeholder="Search participant / company / tender…" value={q} onChange={(e) => { setPage(1); setQ(e.target.value); }} />
          <select value={status} onChange={(e) => { setPage(1); setStatus(e.target.value); }}>
            <option value="">All mapping statuses</option>
            <option value="mapped">Mapped</option>
            <option value="ambiguous">Ambiguous</option>
            <option value="unresolved">Unresolved</option>
          </select>
          <select value={method} onChange={(e) => { setPage(1); setMethod(e.target.value); }}>
            <option value="">All methods</option>
            <option value="direct_normalized_match">Direct normalized</option>
            <option value="alias_match">Alias</option>
            <option value="canonical_name_match">Canonical name</option>
            <option value="gst_company_name_match">GST (name)</option>
          </select>
          <select value={winner} onChange={(e) => { setPage(1); setWinner(e.target.value); }}>
            <option value="">Winners and others</option>
            <option value="true">Winners only</option>
            <option value="false">Non-winners</option>
          </select>
        </div>
        <DataTable
          columns={COLUMNS}
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
          csvName="financial-evaluation"
          dense
        />
      </Section>
    </>
  );
}
