import React, { useEffect, useState } from 'react';
import { api, apiURL, fmtCr, fmtDate, fmtInt, str, methodLabel } from '../api';
import { Section, Badge, Empty, Loader, ErrorBanner, TenderLink } from '../components/ui';
import { DataTable, type Col } from '../components/DataTable';

type Row = Record<string, any>;

type Resp = {
  tender?: Row;
  award?: Row | null;
  participants?: Row[];
  files?: Row[];
  boq_items?: Row[];
};

export default function TenderDetail({ tenderId }: { tenderId: string }) {
  const [d, setD] = useState<Resp | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    setLoading(true);
    setD(null);
    setError(null);
    api
      .get<Resp>(`tenders/${encodeURIComponent(tenderId)}`)
      .then((r) => live && setD(r))
      .catch((e) => live && setError(e.message))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [tenderId]);

  if (loading && !d) return <Loader label="Loading tender" />;
  if (error)
    return error.includes('404') ? (
      <div className="empty">No tender found for id {tenderId}</div>
    ) : (
      <ErrorBanner message={error} />
    );
  const t = d?.tender || {};
  const award = d?.award;
  const participants = d?.participants || [];
  const files = d?.files || [];
  const boq = d?.boq_items || [];

  const hasAward = !!(award || t.awardee_name);

  return (
    <>
      <a
        className="tender-back"
        onClick={() => window.history.back()}
        href="#!/tender-detail"
      >
        ← Back
      </a>

      <div className="panel company-head">
        <div className="tender-chips">
          <TenderLink id={t.external_tender_id} />
          {str(t.lifecycle_stage) && <span className="chip">{str(t.lifecycle_stage)}</span>}
          {str(t.type) && <span className="chip">{str(t.type)}</span>}
          {str(t.category) && <span className="chip">{str(t.category)}</span>}
        </div>
        <div className="tender-title">
          {str(t.title) || str(t.external_tender_id)}
        </div>
        <div className="muted">
          {str(t.org_name) || str(t.organization) || '—'}
          {t.location ? ` · ${str(t.location)}` : ''}
          {t.published_date ? ` · published ${fmtDate(t.published_date)}` : ''}
        </div>

        <div className="kv-grid">
          <KV k="Tender value" v={t.tender_value == null ? '-' : fmtCr(t.tender_value)} />
          <KV k="Awarded value" v={t.awarded_value == null ? '-' : fmtCr(t.awarded_value)} />
          <KV k="EMD" v={t.emd_value == null ? '-' : `Rs ${fmtInt(t.emd_value)}`} />
          <KV
            k="Bid submission window"
            v={`${fmtDate(t.bid_submission_start_date)} → ${fmtDate(t.bid_submission_end_date)}`}
          />
          <KV k="Bid opening" v={fmtDate(t.bid_opening_date)} />
          <KV k="Award date" v={fmtDate(t.contract_updated_date)} />
          <KV k="FN evaluation date" v={fmtDate(t.financial_evaluation_date)} />
          <KV k="Contract type" v={str(t.contract_type) || '-'} />
          <KV k="Form of contract" v={str(t.form_of_contract) || '-'} />
          <KV k="Product category" v={str(t.product_category) || '-'} />
          <KV k="No. of covers" v={str(t.no_of_covers) || '-'} />
          <KV k="Ref / Pincode" v={`${str(t.ref_no) || '-'} / ${str(t.pincode) || '-'}`} />
          <KV k="Winning bid #" v={str(t.winning_bid_number) || '-'} />
          <KV k="Inviting authority" v={str(t.inv_auth_name) || '-'} />
          <KV
            k="Contractor (v2) identity"
            v={
              t.contractor_business_name
                ? `${str(t.contractor_business_name)}${t.contractor_gstin ? ` · ${str(t.contractor_gstin)}` : ''}`
                : '-'
            }
          />
        </div>
        {str(t.description) && (
          <div style={{ marginTop: 12 }}>
            <div className="subhead">Description</div>
            <div className="kv-v wide muted" style={{ fontSize: 12 }}>
              {str(t.description)}
            </div>
          </div>
        )}
      </div>

      {hasAward && (
        <Section
          title="Award — awardee entity mapping"
          sub="deterministic mapping of the extracted awardee against the entities master — GST handled as contractor identity evidence"
        >
          <div className="kv-grid">
            <KV k="Awardee name" v={<b>{str(award?.awardee_name || t.awardee_name)}</b>} />
            <KV k="Awarded value" v={t.awarded_value == null ? '-' : fmtCr(t.awarded_value)} />
            <KV k="Award date" v={fmtDate(t.contract_updated_date)} />
            <KV k="Fiscal year (derived)" v={str(award?.fiscal_year) || '-'} />
          </div>
          <div className="kv-grid" style={{ gridTemplateColumns: 'repeat(2, 1fr)' }}>
            <KV
              k="Mapping status"
              v={
                award ? (
                  <span>
                    <Badge status={award.mapping_status} />{' '}
                    <span className="mono">{methodLabel(award.mapping_method)}</span>
                  </span>
                ) : (
                  <span className="muted">unresolved</span>
                )
              }
            />
            <KV
              k="Linked listed entity"
              v={
                award?.entity_id ? (
                  <a href={`#!/company/${award.entity_id}`}>
                    {str(award.canonical_name)} {str(award.ticker_nse) && `(${str(award.ticker_nse)})`}
                  </a>
                ) : (
                  <span className="muted">{award?.candidate_names ? 'see candidates' : '—'}</span>
                )
              }
            />
            {award?.candidate_names && (
              <KV
                k="Candidate entities (ambiguous)"
                v={<span className="warn-text">{str(award.candidate_names)}</span>}
                wide
              />
            )}
          </div>
          {award && !award.entity_id && !award.candidate_names && (
            <div className="note">
              No listed-entity match at any mapping priority — the awardee name is preserved as-is in
              the resolution views; no fuzzy linking is attempted.
            </div>
          )}
        </Section>
      )}

      <Section
        title={`Participants (${participants.length})`}
        sub="financial-stage participation rows resolved with the shared mapping CTEs"
      >
        <DataTable
          columns={participantCols}
          rows={participants}
          total={participants.length}
          page={1}
          pageSize={100}
          onPage={() => {}}
          csvName={participants.length ? `tender-participants-${tenderId}` : undefined}
          dense
        />
      </Section>

      <Section title={`Documents (${files.length})`} sub="tender_files records with BOQ parse state">
        <DataTable
          columns={fileCols(tenderId)}
          rows={files}
          total={files.length}
          page={1}
          pageSize={100}
          onPage={() => {}}
          csvName={files.length ? `tender-files-${tenderId}` : undefined}
          dense
        />
      </Section>

      <Section
        title={`BOQ items (${boq.length})`}
        sub={boq.length ? 'parsed bill-of-quantities line items across all files' : 'no parsed BOQ items for this tender'}
      >
        {boq.length > 0 ? (
          <DataTable
            columns={boqCols}
            rows={boq}
            total={boq.length}
            page={1}
            pageSize={500}
            onPage={() => {}}
            csvName={`tender-boq-${tenderId}`}
            dense
          />
        ) : (
          <Empty text="No BOQ items parsed" />
        )}
      </Section>
    </>
  );
}

function KV({ k, v, wide }: { k: string; v: React.ReactNode; wide?: boolean }) {
  return (
    <div className="kv">
      <div className="kv-k">{k}</div>
      <div className={`kv-v${wide ? ' wide' : ''}`}>{v}</div>
    </div>
  );
}

const participantCols: Col[] = [
  { key: 'financial_rank', label: 'Rank', csv: (r) => str(r.financial_rank) },
  {
    key: 'participant_name',
    label: 'Participant',
    render: (r) => <b>{str(r.participant_name)}</b>,
    csv: (r) => str(r.participant_name),
  },
  { key: 'bid_value', label: 'Bid value', align: 'right', render: (r) => (r.bid_value == null ? '-' : fmtCr(r.bid_value)), csv: (r) => str(r.bid_value) },
  { key: 'is_winner', label: 'Winner', render: (r) => (r.is_winner ? <span className="win-mark">L1 WIN</span> : '-') },
  { key: 'mapping_status', label: 'Status', render: (r) => <Badge status={r.mapping_status} /> },
  { key: 'mapping_method', label: 'Method', render: (r) => methodLabel(r.mapping_method), csv: (r) => str(r.mapping_method) },
  {
    key: 'canonical_name',
    label: 'Linked entity',
    render: (r) =>
      r.entity_id && r.is_listed === true ? (
        <a href={`#!/company/${r.entity_id}`}>{str(r.canonical_name)}{str(r.ticker_nse) ? ` (${str(r.ticker_nse)})` : ''}</a>
      ) : r.canonical_name ? (
        <span>{str(r.canonical_name)}</span>
      ) : (
        <span className="muted">—</span>
      ),
    csv: (r) => str(r.canonical_name),
  },
  { key: 'candidate_names', label: 'Candidates', render: (r) => (r.candidate_names ? <span className="warn-text">{str(r.candidate_names)}</span> : '-') },
  { key: 'submitted_date', label: 'Submitted', render: (r) => fmtDate(r.submitted_date), csv: (r) => str(r.submitted_date) },
];

const fileCols = (tenderId: string): Col[] => [
  { key: 'file_name', label: 'File name', render: (r) => <a className="tender-link mono" href={apiURL(`tenders/${encodeURIComponent(tenderId)}/files/${encodeURIComponent(String(r.id))}/download`)} onClick={async (event) => { event.preventDefault(); try { const result = await api.get<{ url: string }>(`tenders/${encodeURIComponent(tenderId)}/files/${encodeURIComponent(String(r.id))}/download`); window.location.assign(result.url); } catch (error: any) { window.alert(error?.message || 'Unable to prepare download'); } }} download>{str(r.file_name)}</a>, csv: (r) => str(r.file_name) },
  { key: 'file_type', label: 'Type', csv: (r) => str(r.file_type) },
  { key: 'file_extension', label: 'Ext', csv: (r) => str(r.file_extension) },
  { key: 'file_size_mb', label: 'Size (MB)', align: 'right', render: (r) => (r.file_size_mb == null ? '-' : Number(r.file_size_mb).toFixed(2)) },
  {
    key: 'pdf_parse_state',
    label: 'Parse state',
    render: (r) => <span className="chip">{str(r.pdf_parse_state) || '-'}</span>,
    csv: (r) => str(r.pdf_parse_state),
  },
  {
    key: 'is_boq_parsed_success',
    label: 'BOQ parsed',
    render: (r) => (r.is_boq_parsed_success === true ? '✓' : r.is_boq_parsed_success === false ? <span className="warn-text">✗</span> : '-'),
  },
  { key: 'boq_item_count', label: 'BOQ items', align: 'right', render: (r) => fmtInt(r.boq_item_count) },
  { key: 'parse_error_message', label: 'Parse error', render: (r) => (str(r.parse_error_message) ? <span className="warn-text">{str(r.parse_error_message)}</span> : '-') },
];

const boqCols: Col[] = [
  { key: 'sl_no', label: '#', render: (r) => <code className="mono">{str(r.sl_no)}</code>, csv: (r) => str(r.sl_no) },
  { key: 'item_description', label: 'Item description', render: (r) => <span style={{ whiteSpace: 'normal', minWidth: 280 }}>{str(r.item_description)}</span>, csv: (r) => str(r.item_description) },
  { key: 'quantity', label: 'Qty', align: 'right', render: (r) => fmtInt(r.quantity), num: true },
  { key: 'units', label: 'Units' },
  { key: 'rate', label: 'Rate (Rs)', align: 'right', render: (r) => (r.rate == null ? '-' : `Rs ${fmtInt(r.rate)}`), num: true },
  { key: 'rrr', label: 'RRR (Rs)', align: 'right', render: (r) => (r.rrr == null ? '-' : `Rs ${fmtInt(r.rrr)}`), num: true },
  { key: 'awardee_rate', label: 'Awardee rate (Rs)', align: 'right', render: (r) => (r.awardee_rate == null ? '-' : `Rs ${fmtInt(r.awardee_rate)}`), num: true },
  { key: 'awardee_amount', label: 'Awardee amount (Rs)', align: 'right', render: (r) => (r.awardee_amount == null ? '-' : `Rs ${fmtInt(r.awardee_amount)}`), num: true },
];
