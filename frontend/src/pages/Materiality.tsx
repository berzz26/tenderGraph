import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtDate, fmtInt, str } from '../api';
import { DataTable, type Col } from '../components/DataTable';
import { ErrorBanner, Kpi, PageHeader, Section } from '../components/ui';

type Row = Record<string, any>;

const columns: Col[] = [
  { key: 'financial_evaluation_date', label: 'Evaluation date', sortKey: 'evaluation', render: (row) => <span className="mono">{fmtDate(row.evaluation_date || row.financial_evaluation_date)}</span>, csv: (row) => str(row.evaluation_date || row.financial_evaluation_date) },
  { key: 'external_tender_id', label: 'Tender ID', render: (row) => <a className="tender-link mono" href={`#!/tender/${encodeURIComponent(str(row.external_tender_id))}`}>{str(row.external_tender_id)}</a>, csv: (row) => str(row.external_tender_id) },
  { key: 'title', label: 'Tender title', render: (row) => <span title={str(row.title)}>{str(row.title).slice(0, 70) || '-'}</span>, csv: (row) => str(row.title) },
  { key: 'organization', label: 'Organization', csv: (row) => str(row.organization) },
  { key: 'ref_no', label: 'Reference', csv: (row) => str(row.ref_no) },
  { key: 'category', label: 'Category', csv: (row) => str(row.category) },
  { key: 'location', label: 'Location', csv: (row) => str(row.location) },
  { key: 'tender_value', label: 'Tender value', align: 'right', sortKey: 'value', render: (row) => fmtCr(row.tender_value), csv: (row) => str(row.tender_value) },
  { key: 'l1_participant', label: 'L1 participant', render: (row) => str(row.l1_participant) || <span className="muted">Not identified</span>, csv: (row) => str(row.l1_participant) },
  { key: 'l1_bid_value', label: 'L1 bid value', align: 'right', sortKey: 'bid', render: (row) => fmtCr(row.l1_bid_value), csv: (row) => str(row.l1_bid_value) },
  { key: 'mapped_participants', label: 'Mapped participants', render: (row) => Array.isArray(row.mapped_participants) && row.mapped_participants.length ? row.mapped_participants.join(', ') : <span className="muted">None mapped</span>, csv: (row) => Array.isArray(row.mapped_participants) ? row.mapped_participants.join(', ') : '' },
  { key: 'participant_count', label: 'Participants', align: 'right', render: (row) => fmtInt(row.participant_count), csv: (row) => str(row.participant_count) },
];

export default function Materiality({ detailId: _detailId }: { detailId?: string }) {
  const [rows, setRows] = useState<Row[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [query, setQuery] = useState('');
  const [mappedParticipants, setMappedParticipants] = useState('');
  const [sort, setSort] = useState('evaluation');
  const [dir, setDir] = useState<'asc' | 'desc'>('desc');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 50;

  useEffect(() => {
    let active = true;
    setLoading(true);
    api.get<any>('materiality', { page, page_size: pageSize, q: query, sort, dir, mapped_participants: mappedParticipants })
      .then((response) => {
        if (!active) return;
        setRows(response.data || []);
        setTotal(response.total || 0);
        setError(null);
      })
      .catch((exception: any) => active && setError(exception.message))
      .finally(() => active && setLoading(false));
    return () => { active = false; };
  }, [page, query, sort, dir, mappedParticipants]);

  if (error) return <ErrorBanner message={error} />;

  return <>
    <PageHeader eyebrow="RESEARCH / FINANCIAL MATERIALITY" title="Financial evaluation tender register" description="Investigate tenders in the financial-evaluation stage, with tender values, L1 evidence, bid values, and direct tender detail access." meta={<>Tender-level records<br />Latest evaluations first</>} />
    <div className="kpi-grid">
      <Kpi label="Evaluated tenders" value={fmtInt(total)} hint="Server-side tender register" tone="accent" />
      <Kpi label="Visible records" value={fmtInt(rows.length)} hint={`Page ${page}`} />
      <Kpi label="Latest evaluation" value={rows.length ? fmtDate(rows[0].evaluation_date || rows[0].financial_evaluation_date) : '-'} hint="Current result ordering" tone="good" />
      <Kpi label="L1 identified" value={fmtInt(rows.filter((row) => str(row.l1_participant)).length)} hint="On the current page" />
    </div>
    <Section title="Financial evaluation tenders" sub="One row per tender. Click a tender ID to open its complete tender detail, participants, documents, and BOQ records.">
      <div className="filters">
        <input aria-label="Search financial evaluation tenders" placeholder="Search tender ID, title, organization, reference…" value={query} onChange={(event) => { setPage(1); setQuery(event.target.value); }} />
        <select aria-label="Mapped participant filter" value={mappedParticipants} onChange={(event) => { setPage(1); setMappedParticipants(event.target.value); }}><option value="">All participants</option><option value="mapped">Mapped participants</option><option value="unmapped">Unmapped participants</option></select>
      </div>
      <DataTable columns={columns} rows={rows} total={total} page={page} pageSize={pageSize} onPage={setPage} onSort={(key, direction) => { setSort(key); setDir(direction); setPage(1); }} sort={sort} dir={dir} loading={loading} csvName="financial-evaluation-tenders" onRowClick={(row) => { window.location.hash = `#!/tender/${encodeURIComponent(str(row.external_tender_id))}`; }} dense />
    </Section>
  </>;
}
