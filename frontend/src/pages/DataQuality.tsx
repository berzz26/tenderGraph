import React, { useEffect, useState } from 'react';
import { api, fmtCr, fmtInt, str } from '../api';
import { Section, Badge, Kpi, ErrorBanner, Loader, PageHeader } from '../components/ui';
import { DataTable, type Col } from '../components/DataTable';

type Row = Record<string, any>;

export default function DataQuality() {
  const [d, setD] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    api
      .get('data-quality')
      .then(setD)
      .catch((e: any) => setErr(e.message));
  }, []);

  if (err) return <ErrorBanner message={err} />;
  if (!d) return <Loader />;

  const aq = d.awards_quality || {};
  const pq = d.participants_quality || {};
  const em = d.entity_data_missing || {};
  const boq = d.boq_parsing || {};
  const gst = d.gst_coverage || {};

  return (
    <>
      <PageHeader eyebrow="DATA OPERATIONS / QUALITY" title="Data quality control" description="Understand coverage, missing values, stale snapshots, and mapping exceptions before using a signal in research." meta={<>Traceable source fields<br />Missing is not zero</>} />
      <div className="kpi-grid">
        <Kpi label="Missing awardee name" value={fmtInt((d.lifecycle || []).reduce((s: number, r: any) => s + Number(r.missing_awardee_tenders || 0), 0))} tone="warn" hint="tenders with fields and no awardee" />
        <Kpi label="Unparseable awarded_value" value={fmtInt(aq.unparseable_awarded_value)} tone={aq.unparseable_awarded_value ? 'bad' : 'good'} />
        <Kpi label="Unparseable bid values" value={fmtInt(pq.unparseable_bid_value)} tone={pq.unparseable_bid_value ? 'bad' : 'good'} />
        <Kpi label="Unresolved names (feval)" value={fmtInt((d.fe_mapping_status || []).find((x: any) => x.mapping_status === 'unresolved')?.records)} tone="warn" />
        <Kpi label="Ambiguous (feval / awards)" value={`${fmtInt((d.fe_mapping_status || []).find((x: any) => x.mapping_status === 'ambiguous')?.records)} / ${fmtInt((d.aw_mapping_status || []).find((x: any) => x.mapping_status === 'ambiguous')?.records)}`} />
        <Kpi label="Entities missing financials" value={fmtInt(em.missing_financials)} hint={`${fmtInt(em.missing_market_data)} missing market data`} />
      </div>

      <div className="grid-2">
        <Section title="Tender lifecycle coverage" sub="tenders by lifecycle stage, with awards missing data">
          <table className="table dense">
            <thead>
              <tr><th>Stage</th><th className="right">Tenders</th><th className="right">Awarded</th><th className="right">Missing awardee</th></tr>
            </thead>
            <tbody>
              {(d.lifecycle || []).map((r: any, i: number) => (
                <tr key={i}>
                  <td>{str(r.lifecycle_stage)}</td>
                  <td className="right">{fmtInt(r.tender_count)}</td>
                  <td className="right">{fmtInt(r.awarded_tenders)}</td>
                  <td className="right">{r.missing_awardee_tenders ? <span className="warn-text">{fmtInt(r.missing_awardee_tenders)}</span> : '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Section>

        <Section title="Duplicate / conflicting aliases" sub="entity_aliases normalized to more than one entity">
          {(d.duplicate_aliases || []).length ? (
            <DataTable
              columns={dupCols()}
              rows={d.duplicate_aliases || []}
              total={(d.duplicate_aliases || []).length}
              page={1}
              pageSize={50}
              onPage={() => {}}
              csvName="duplicate-aliases"
              dense
            />
          ) : (
            <div className="note">No alias maps to more than one entity.</div>
          )}
          {(d.conflicting_mappings || []).length > 0 && (
            <>
              <div className="subhead">One normalized awardee resolving to multiple entities</div>
              <DataTable
                columns={conflictCols()}
                rows={d.conflicting_mappings || []}
                total={(d.conflicting_mappings || []).length}
                page={1}
                pageSize={50}
                onPage={() => {}}
                csvName="conflicting-mappings"
                dense
              />
            </>
          )}
        </Section>
      </div>

      <div className="grid-2">
        <Section title="BOQ parsing coverage" sub="tender_files / boq_items">
          <table className="table dense">
            <tbody>
              <tr><td>Tenders with files</td><td className="right">{fmtInt(boq.tenders_with_files)}</td></tr>
              <tr><td>Files total</td><td className="right">{fmtInt(boq.total_files)}</td></tr>
              <tr><td>BOQ parsed success</td><td className="right">{fmtInt(boq.boq_parsed_success)}</td></tr>
              <tr><td>BOQ parsed failed</td><td className="right">{fmtInt(boq.boq_parsed_failed)}</td></tr>
              <tr><td>BOQ parse unknown</td><td className="right">{fmtInt(boq.boq_parse_unknown)}</td></tr>
              <tr><td>Tenders with parsed BOQ</td><td className="right">{fmtInt(boq.tenders_with_parsed_boq)}</td></tr>
              <tr><td>BOQ line items</td><td className="right">{fmtInt(boq.boq_items)}</td></tr>
            </tbody>
          </table>
        </Section>
        <Section title="GST linkage coverage" sub="entity_gst vs contractor_v2 GSTINs">
          <table className="table dense">
            <tbody>
              <tr><td>entity_gst rows</td><td className="right">{fmtInt(gst.entity_gst_rows)}</td></tr>
              <tr><td>Entities with GST data</td><td className="right">{fmtInt(gst.entities_with_gst)}</td></tr>
              <tr><td>contractor_v2 with GST</td><td className="right">{fmtInt(gst.contractor_v2_with_gst)}</td></tr>
              <tr><td>Contractor GSTINs matching entity_gst</td><td className="right">{fmtInt(gst.contractor_gst_matched)}</td></tr>
            </tbody>
          </table>
          <div className="note">
            seller_details carries no GSTINs in production and is not joinable to tenders. contractor (v1) is empty.
            GST evidence remains a secondary identity channel.
          </div>
        </Section>
      </div>

      <div className="grid-1">
        <Section title="Ambiguous candidates (sample)" sub="kept unresolved rather than silently picked">
          <div className="grid-2">
            <div>
              <div className="subhead">Financial evaluation</div>
              {(d.fe_ambiguous || []).length === 0 ? (
                <div className="note">None</div>
              ) : (
                d.fe_ambiguous.map((r: any, i: number) => (
                  <div className="amb-card" key={i}>
                    <b>{str(r.participant_name)}</b>
                    <div className="muted small">{str(r.participant_normalized)}</div>
                    <div className="warn-text">{str(r.candidate_names)} ({str(r.candidate_count)} candidates)</div>
                  </div>
                ))
              )}
            </div>
            <div>
              <div className="subhead">Awards</div>
              {(d.aw_ambiguous || []).length === 0 ? (
                <div className="note">None</div>
              ) : (
                d.aw_ambiguous.map((r: any, i: number) => (
                  <div className="amb-card" key={i}>
                    <b>{str(r.awardee_name)}</b>
                    <div className="muted small">{str(r.awardee_normalized)}</div>
                    <div className="warn-text">{str(r.candidate_names)} ({str(r.candidate_count)} candidates)</div>
                  </div>
                ))
              )}
            </div>
          </div>
        </Section>
      </div>
    </>
  );
}

function dupCols(): Col[] {
  return [
    { key: 'normalized_alias', label: 'Normalized alias', render: (r) => <code className="mono">{str(r.normalized_alias)}</code> },
    { key: 'canonical_names', label: 'Mapped entities', render: (r) => <span className="warn-text">{str(r.canonical_names)}</span> },
    { key: 'entity_count', label: 'Entities', align: 'right', num: true },
  ];
}

function conflictCols(): Col[] {
  return [
    { key: 'awardee_normalized', label: 'Normalized awardee', render: (r) => <code className="mono">{str(r.awardee_normalized)}</code> },
    { key: 'canonical_names', label: 'Distinct companies', render: (r) => <span className="warn-text">{str(r.canonical_names)}</span> },
    { key: 'entity_count', label: 'Entities', align: 'right', num: true },
    { key: 'records', label: 'Records', align: 'right', num: true },
  ];
}
