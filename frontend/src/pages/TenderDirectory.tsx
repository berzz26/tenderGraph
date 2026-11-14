import React, { useEffect, useState } from 'react';
import { api, fmtDate, str } from '../api';
import { DataTable, type Col } from '../components/DataTable';
import { PageHeader } from '../components/ui';

type Row = Record<string, any>;

const columns: Col[] = [
  { key: 'external_tender_id', label: 'Tender ID', render: (row) => <a className="tender-link mono" href={`#!/tender/${encodeURIComponent(str(row.external_tender_id))}`}>{str(row.external_tender_id)}</a> },
  { key: 'title', label: 'Tender title', render: (row) => <b>{str(row.title) || '-'}</b> },
  { key: 'buyer_org', label: 'Organization' },
  { key: 'category', label: 'Category' },
  { key: 'location', label: 'Location' },
  { key: 'submitted_date', label: 'Tender date', render: (row) => fmtDate(row.submitted_date) },
  { key: 'financial_evaluation_date', label: 'Financial evaluation', render: (row) => fmtDate(row.financial_evaluation_date) },
  { key: 'awardee_name', label: 'Awardee' },
];

export default function TenderDirectory() {
  const [query, setQuery] = useState('');
  const [rows, setRows] = useState<Row[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 50;

  useEffect(() => {
    let active = true;
    setLoading(true);
    api.get<any>('financial-evaluation', { q: query, page, page_size: pageSize, sort: 'submitted', dir: 'desc' })
      .then((response) => {
        if (!active) return;
        setRows(response.data || []);
        setTotal(response.total || 0);
        setError(null);
      })
      .catch((exception: any) => active && setError(exception.message))
      .finally(() => active && setLoading(false));
    return () => { active = false; };
  }, [query, page]);

  return <>
    <PageHeader eyebrow="PROCUREMENT / TENDER DETAIL" title="Tender detail register" description="Search the tender ledger and open a complete tender record with award evidence, participants, documents, and BOQ data." meta={<>Search and inspect<br />Document downloads enabled</>} />
    <section className="panel">
      <div className="filters">
        <input placeholder="Search tender ID, title, organization…" value={query} onChange={(event) => { setPage(1); setQuery(event.target.value); }} />
      </div>
      <DataTable columns={columns} rows={rows} total={total} page={page} pageSize={pageSize} onPage={setPage} loading={loading} error={error} onRowClick={(row) => { window.location.hash = `#!/tender/${encodeURIComponent(str(row.external_tender_id))}`; }} csvName="tender-detail-register" dense />
    </section>
  </>;
}
