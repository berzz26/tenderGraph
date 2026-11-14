import React, { useEffect, useState } from 'react';
import { api, str, methodLabel, fmtCr } from '../api';
import { Section, Tabs, Badge, Loader, ErrorBanner, TenderLink, PageHeader } from '../components/ui';
import { DataTable, type Col } from '../components/DataTable';

type Row = Record<string, any>;

const TABS = [
  { id: 'direct', label: 'Direct matches' },
  { id: 'alias', label: 'Alias matches' },
  { id: 'gst', label: 'GST matches' },
  { id: 'canonical', label: 'Canonical matches' },
  { id: 'unresolved', label: 'Unresolved' },
  { id: 'ambiguous', label: 'Ambiguous' },
];

const tabMethod: Record<string, string | undefined> = {
  direct: 'direct_normalized_match',
  alias: 'alias_match',
  gst: 'gst',
  canonical: 'canonical_name_match',
};

const FE_METHOD_COLS: Col[] = [
  { key: 'participant_name', label: 'Participant original name', render: (r) => <b>{str(r.participant_name)}</b>, csv: (r) => str(r.participant_name) },
  { key: 'participant_normalized', label: 'Normalized name', csv: (r) => str(r.participant_normalized) },
  { key: 'canonical_name', label: 'Canonical company', csv: (r) => str(r.canonical_name) },
  { key: 'ticker_nse', label: 'NSE' },
  { key: 'ticker_bse', label: 'BSE' },
  { key: 'external_tender_id', label: 'Tender ID', render: (r) => <TenderLink id={r.external_tender_id} />, csv: (r) => str(r.external_tender_id) },
  { key: 'financial_rank', label: 'Rank' },
  { key: 'bid_value', label: 'Bid value', align: 'right', num: true, render: (r) => fmtCr(r.bid_value), csv: (r) => str(r.bid_value) },
  { key: 'mapping_method', label: 'Method', render: (r) => methodLabel(r.mapping_method), csv: (r) => str(r.mapping_method) },
  { key: 'mapping_status', label: 'Status', render: (r) => <Badge status={r.mapping_status} />, csv: (r) => str(r.mapping_status) },
];

const AW_METHOD_COLS: Col[] = [
  { key: 'awardee_name', label: 'Awardee original name', render: (r) => <b>{str(r.awardee_name)}</b>, csv: (r) => str(r.awardee_name) },
  { key: 'canonical_name', label: 'Canonical company', csv: (r) => str(r.canonical_name) },
  { key: 'ticker_nse', label: 'NSE' },
  { key: 'ticker_bse', label: 'BSE' },
  { key: 'contractor_gstin', label: 'Contractor GSTIN' },
  { key: 'external_tender_id', label: 'Tender ID', render: (r) => <TenderLink id={r.external_tender_id} />, csv: (r) => str(r.external_tender_id) },
  { key: 'awarded_value', label: 'Awarded value', align: 'right', num: true, render: (r) => fmtCr(r.awarded_value), csv: (r) => str(r.awarded_value) },
  { key: 'mapping_method', label: 'Method', render: (r) => methodLabel(r.mapping_method), csv: (r) => str(r.mapping_method) },
  { key: 'mapping_status', label: 'Status', render: (r) => <Badge status={r.mapping_status} />, csv: (r) => str(r.mapping_status) },
];

const FE_AMBIG_COLS: Col[] = [
  { key: 'participant_name', label: 'Participant original name', render: (r) => <b>{str(r.participant_name)}</b>, csv: (r) => str(r.participant_name) },
  { key: 'participant_normalized', label: 'Normalized name', csv: (r) => str(r.participant_normalized) },
  { key: 'candidate_names', label: 'Candidate canonical entities', render: (r) => <span className="warn-text">{str(r.candidate_names)}</span>, csv: (r) => str(r.candidate_names) },
  { key: 'candidate_count', label: 'Candidates', align: 'right', num: true },
  { key: 'external_tender_id', label: 'Example tender', render: (r) => <TenderLink id={r.external_tender_id} /> },
];

const AW_AMBIG_COLS: Col[] = [
  { key: 'awardee_name', label: 'Awardee original name', render: (r) => <b>{str(r.awardee_name)}</b>, csv: (r) => str(r.awardee_name) },
  { key: 'awardee_normalized', label: 'Normalized name', csv: (r) => str(r.awardee_normalized) },
  { key: 'candidate_names', label: 'Candidate canonical entities', render: (r) => <span className="warn-text">{str(r.candidate_names)}</span>, csv: (r) => str(r.candidate_names) },
  { key: 'candidate_count', label: 'Candidates', align: 'right', num: true },
  { key: 'external_tender_id', label: 'Example tender', render: (r) => <TenderLink id={r.external_tender_id} /> },
];

export default function EntityResolution() {
  const [tab, setTab] = useState('direct');
  const [scope, setScope] = useState<'fe' | 'awards'>('fe');
  const [list, setList] = useState<Row[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 60;

  useEffect(() => {
    let live = true;
    setLoading(true);
    setError(null);

    const base = scope === 'fe' ? 'financial-evaluation' : 'awards';
    const isAmbiguous = tab === 'ambiguous';
    const isUnresolved = tab === 'unresolved' ? 'unresolved' : 'mapped';
    const method =
      tab === 'gst' ? (scope === 'fe' ? 'gst_company_name_match' : 'gst_company_name_match') : tabMethod[tab];

    const fetcher = isAmbiguous
      ? Promise.all([
          api.get('mappings/ambiguous', { dataset: scope }),
        ]).then((d: any) => {
          const part = scope === 'fe' ? d[0].financial_evaluation : d[0].awards;
          setList(part?.data ?? []);
          setTotal(part?.total ?? 0);
        })
      : api
          .get(base, { page, page_size: pageSize, mapping_status: isUnresolved, method: method as string })
          .then((d: any) => {
            setList(d.data);
            setTotal(d.total);
          });

    fetcher
      .catch((e) => live && setError(e.message))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [tab, scope, page]);

  const cols = tab === 'ambiguous' ? (scope === 'fe' ? FE_AMBIG_COLS : AW_AMBIG_COLS) : scope === 'fe' ? FE_METHOD_COLS : AW_METHOD_COLS;

  return (
    <>
    <PageHeader eyebrow="DATA OPERATIONS / ENTITY RESOLUTION" title="Entity resolution workbench" description="Review deterministic participant and award mappings, candidate collisions, and unresolved names without introducing fuzzy assumptions." meta={<>Stored source names remain intact<br />Ambiguity is never silently resolved</>} />
    <Section
      title="Entity Resolution"
      sub="original names → normalized → canonical entity; every mapping keeps its method and ambiguity trail"
      actions={
        <div className="mini-scope">
          <button className={scope === 'fe' ? 'btn small active' : 'btn small'} onClick={() => { setScope('fe'); setPage(1); }}>
            Financial evaluation
          </button>
          <button className={scope === 'awards' ? 'btn small active' : 'btn small'} onClick={() => { setScope('awards'); setPage(1); }}>
            Awarded contracts
          </button>
        </div>
      }
    >
      <Tabs tabs={TABS} active={tab} onChange={(t) => { setTab(t); setPage(1); }} />
      {error && <ErrorBanner message={error} />}
      {loading ? (
        <Loader />
      ) : (
        <DataTable
          columns={cols}
          rows={list}
          total={total}
          page={page}
          pageSize={pageSize}
          onPage={setPage}
          csvName={scope === 'fe' ? `resolution-fe-${tab}` : `resolution-awards-${tab}`}
          dense
        />
      )}
      <div className="note">
        {tab === 'ambiguous'
          ? 'Ambiguous records keep every candidate entity visible; the pipeline never silently picks one.'
          : tab === 'gst'
            ? 'GST company-name evidence is a secondary candidate and never overrides a direct or alias match. Awards additionally support GSTIN identity through the tender\'s assigned contractor_v2 record.'
            : 'Matching priority: 1 = direct normalized, 2 = alias, 3 = canonical name, 4 = GST company name (awards also use GSTIN via contractor). No fuzzy matching is used anywhere.'}
      </div>
    </Section>
    </>
  );
}
