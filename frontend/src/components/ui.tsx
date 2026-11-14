import React from 'react';
import { str } from '../api';

export function Loader({ label = 'Loading' }: { label?: string }) {
  return (
    <div className="loader-wrap">
      <div className="loader" />
      <span>{label}…</span>
    </div>
  );
}

export function ErrorBanner({ message }: { message: string }) {
  return <div className="error-banner">⚠ {message}</div>;
}

export function Section({ title, actions, children, sub }: { title: string; actions?: React.ReactNode; children: React.ReactNode; sub?: string }) {
  return (
    <div className="panel">
      <div className="panel-head">
        <div>
          <h3>{title}</h3>
          {sub && <div className="panel-sub">{sub}</div>}
        </div>
        {actions && <div className="panel-actions">{actions}</div>}
      </div>
      {children}
    </div>
  );
}

export function PageHeader({ eyebrow, title, description, meta }: { eyebrow: string; title: string; description: string; meta?: React.ReactNode }) {
  return <div className="page-hero"><div><div className="eyebrow">{eyebrow}</div><h1>{title}</h1><p>{description}</p></div>{meta && <div className="hero-note">{meta}</div>}</div>;
}

export function Kpi({
  label,
  value,
  hint,
  tone = 'default',
}: {
  label: string;
  value: React.ReactNode;
  hint?: string;
  tone?: 'default' | 'good' | 'warn' | 'bad' | 'accent';
}) {
  return (
    <div className={`kpi tone-${tone}`}>
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
      {hint && <div className="kpi-hint">{hint}</div>}
    </div>
  );
}

export function Badge({ status }: { status: string }) {
  const s = str(status) || 'unresolved';
  return <span className={`badge badge-${s}`}>{s}</span>;
}

export function Tabs({
  tabs,
  active,
  onChange,
}: {
  tabs: { id: string; label: string; count?: number }[];
  active: string;
  onChange: (id: string) => void;
}) {
  return (
    <div className="tabs">
      {tabs.map((t) => (
        <button
          key={t.id}
          className={`tab ${active === t.id ? 'active' : ''}`}
          onClick={() => onChange(t.id)}
        >
          {t.label}
          {t.count !== undefined && <span className="tab-count">{t.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function Empty({ text = 'No records' }: { text?: string }) {
  return <div className="empty">{text}</div>;
}

/** Hyperlinked tender id — points at the tender detail page (#!/tender/<id>). */
export function TenderLink({ id }: { id: any }) {
  const s = str(id);
  if (!s) return <span className="muted">-</span>;
  return (
    <a className="tender-link mono" href={`#!/tender/${encodeURIComponent(s)}`}>
      {s}
    </a>
  );
}
