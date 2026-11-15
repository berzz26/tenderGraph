export type Row = Record<string, any>;

export type ListResp = {
  data: Row[];
  total: number;
  limit: number;
  offset: number;
};

async function get<T = any>(path: string, params: Record<string, any> = {}): Promise<T> {
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue;
    usp.set(k, String(v));
  }
  const qs = usp.toString();
  const res = await fetch(`/api/v1/${path}${qs ? '?' + qs : ''}`);
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const j = await res.json();
      if (j?.error) msg = j.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  const payload = await res.json();
  // Keep the current investigation components compatible with the workspace
  // response shape while the API exposes the more explicit selected_bid name.
  if (path.startsWith('bidders/') && payload?.data?.selected_bid && !payload.data.bid) {
    payload.data.bid = {
      ...payload.data.selected_bid,
      name: payload.data.selected_bid.name || payload.data.selected_bid.participant_name,
    };
  }
  return payload as T;
}

async function post<T = any>(path: string, body: any): Promise<T> {
  const res = await fetch(`/api/v1/${path}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  if (!res.ok) { let msg = `HTTP ${res.status}`; try { const j = await res.json(); if (j?.error) msg = j.error; } catch { /* ignore */ } throw new Error(msg); }
  return await res.json() as T;
}

export const api = { get, post };

// ---------- formatting ----------

const numFmt = new Intl.NumberFormat('en-IN', { maximumFractionDigits: 2 });
const intFmt = new Intl.NumberFormat('en-IN');

/** rupees -> Rs crore string, "-" for null */
export function fmtCr(v: any): string {
  const n = numOrNull(v);
  if (n === null) return '-';
  const cr = n / 1e7;
  if (Math.abs(cr) >= 1000) {
    return `Rs ${numFmt.format(Math.round(cr))} cr`;
  }
  return `Rs ${numFmt.format(Math.round(cr * 100) / 100)} cr`;
}

export function fmtRatio(v: any): string {
  const n = numOrNull(v);
  if (n === null) return '-';
  if (Math.abs(n) >= 100) return numFmt.format(Math.round(n));
  return `${Math.round(n * 1e4) / 1e4}x`;
}

export function fmtInt(v: any): string {
  const n = numOrNull(v);
  if (n === null) return '-';
  return intFmt.format(n);
}

export function fmtPercent(v: any): string {
  const n = numOrNull(v);
  if (n === null) return '-';
  return `${(Math.round(n * 1e4) / 100).toFixed(2)}%`;
}

export function numOrNull(v: any): number | null {
  if (v === null || v === undefined || v === '') return null;
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isFinite(n) ? n : null;
}

export function fmtDate(v: any): string {
  if (!v) return '-';
  const s = String(v);
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return s;
  return d.toISOString().slice(0, 10);
}

export function fmtMonth(v: any): string {
  if (!v) return '-';
  const d = new Date(String(v));
  if (Number.isNaN(d.getTime())) return String(v);
  return d.toISOString().slice(0, 7);
}

export function str(v: any): string {
  if (v === null || v === undefined) return '';
  return String(v);
}

export function methodLabel(m: any): string {
  const s = str(m);
  if (!s) return '—';
  return s
    .replace(/^direct_normalized_match$/, 'Direct normalized')
    .replace(/^alias_match$/, 'Alias')
    .replace(/^canonical_name_match$/, 'Canonical name')
    .replace(/^gst_contractor_match$/, 'GST (contractor)')
    .replace(/^gst_company_name_match$/, 'GST (name)');
}

export function statusLabel(s: any): string {
  const v = str(s);
  if (!v) return 'unresolved';
  return v;
}
