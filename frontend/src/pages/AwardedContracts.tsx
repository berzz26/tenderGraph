import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtInt, fmtDate, numOrNull, str, methodLabel } from '../api';
import { Section, Badge, Tabs, TenderLink, PageHeader, Kpi } from '../components/ui';
import { BarChart } from '../components/charts';
import { DataTable, type Col } from '../components/DataTable';

type Row = Record<string, any>;

const COLUMNS: Col[] = [
  { key: 'awardee_name', label: 'Awardee', render: (r) => <b>{str(r.awardee_name)}</b>, sortKey: 'awardee', csv: (r) => str(r.awardee_name) },
  { key: 'canonical_name', label: 'Canonical company', sortKey: 'entity' },
  { key: 'ticker_nse', label: 'NSE' },
  { key: 'ticker_bse', label: 'BSE' },
  { key: 'entity_gstins', label: 'GSTIN' },
  { key: 'external_tender_id', label: 'Tender ID', sortKey: 'tender', render: (r) => <TenderLink id={r.external_tender_id} /> },
  { key: 'buyer_org', label: 'Buyer organization', sortKey: 'buyer' },
  { key: 'contract_date', label: 'Contract date', sortKey: 'contract_date', render: (r) => fmtDate(r.contract_date), csv: (r) => str(r.contract_date) },
  { key: 'fiscal_year', label: 'Fiscal year', sortKey: 'fy' },
  { key: 'awarded_value', label: 'Awarded value', align: 'right', num: true, sortKey: 'awarded_value', render: (r) => (r.awarded_value == null ? '-' : fmtCr(r.awarded_value)), csv: (r) => str(r.awarded_value) },
  { key: 'mapping_method', label: 'Method', sortKey: 'method', render: (r) => methodLabel(r.mapping_method), csv: (r) => str(r.mapping_method) },
  { key: 'mapping_status', label: 'Status', sortKey: 'status', render: (r) => <Badge status={r.mapping_status} />, csv: (r) => str(r.mapping_status) },
];

export default function AwardedContracts() {
  const [rows, setRows] = useState<Row[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [sort, setSort] = useState('contract_date');
  const [dir, setDir] = useState<'asc' | 'desc'>('desc');
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('');
  const [method, setMethod] = useState('');
  const [fy, setFy] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [summary, setSummary] = useState<any>(null);
  const pageSize = 50;

  useEffect(() => {
    let live = true;
    setLoading(true);
    api
      .get('awards', { page, page_size: pageSize, sort, dir, q, mapping_status: status, method, fiscal_year: fy })
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
  }, [page, sort, dir, q, status, method, fy]);
  useEffect(() => { api.get('awards/summary').then(setSummary).catch(() => setSummary(null)); }, []);

  return (
    <>
    <PageHeader eyebrow="PROCUREMENT / CONTRACTS" title="Awarded contract register" description="Inspect awardee identity, contract values, procuring organizations, and source dates without conflating an L1 position with a confirmed award." meta={<>Contract evidence<br />Mapping status remains visible</>} />
    {summary?.totals && <><div className="kpi-grid"><Kpi label="Award records" value={fmtInt(summary.totals.records)} hint={`${fmtInt(summary.totals.unique_tenders)} tenders`} tone="accent" /><Kpi label="Award value" value={fmtCr(summary.totals.awarded_value_total)} hint="All source award records" /><Kpi label="Mapped listed" value={fmtInt(summary.totals.mapped_listed_records)} hint={`${fmtInt(summary.totals.unique_mapped_entities)} entities`} tone="good" /><Kpi label="Unique buyers" value={fmtInt(summary.totals.unique_buyers)} hint="Government organizations" /></div><div className="grid-2"><Section title="Award value by fiscal year" sub="Contract-date fiscal-year aggregation"><BarChart money items={(summary.awarded_by_fy || []).map((row: any) => ({ label: str(row.fiscal_year) || 'Unknown', value: numOrNull(row.awarded_value_total) || 0 }))} /></Section><Section title="Largest procuring organizations" sub="Top buyers by source award value"><BarChart money items={(summary.top_buyers || []).slice(0, 8).map((row: any) => ({ label: str(row.buyer_org) || 'Unknown', value: numOrNull(row.awarded_value_total) || 0 }))} /></Section></div></>}
    <Section title="Awarded Contracts — awardee mapping" sub="tender_extracted_meta awardees resolved against the entities master (GST handled as contractor identity evidence)">
      <div className="filters">
        <input placeholder="Search awardee / company / buyer / tender…" value={q} onChange={(e) => { setPage(1); setQ(e.target.value); }} />
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
          <option value="gst_contractor_match">GST (contractor)</option>
          <option value="gst_company_name_match">GST (name)</option>
        </select>
        <input placeholder="Fiscal year (FY2025)" value={fy} onChange={(e) => { setPage(1); setFy(e.target.value); }} className="w-160" />
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
        csvName="awarded-contracts"
        dense
      />
    </Section>
    </>
  );
}
