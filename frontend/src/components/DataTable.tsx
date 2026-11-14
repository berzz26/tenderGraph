import React, { useMemo, useState } from 'react';
import { Loader, ErrorBanner, Empty } from './ui';
import { numOrNull, str } from '../api';

export type Col = {
  key: string;
  label: string;
  align?: 'left' | 'right';
  render?: (row: Row) => React.ReactNode;
  csv?: (row: Row) => string;
  sortKey?: string;
  num?: boolean;
};

type Row = Record<string, any>;

export function DataTable({
  columns,
  rows,
  total,
  page,
  pageSize,
  onPage,
  onSort,
  sort,
  dir,
  loading,
  error,
  onRowClick,
  csvName,
  dense,
  maxHeight,
}: {
  columns: Col[];
  rows: Row[];
  total: number;
  page: number;
  pageSize: number;
  onPage: (p: number) => void;
  onSort?: (key: string, dir: 'asc' | 'desc') => void;
  sort?: string;
  dir?: string;
  loading?: boolean;
  error?: string | null;
  onRowClick?: (row: Row) => void;
  csvName?: string;
  dense?: boolean;
  maxHeight?: number;
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const [copied, setCopied] = useState(false);

  const exportCsv = () => {
    if (!csvName) return;
    const head = columns.map((c) => `"${c.label.replace(/"/g, '""')}"`).join(',');
    const lines = rows.map((r) =>
      columns
        .map((c) => {
          const v = c.csv ? c.csv(r) : cellText(c, r);
          return `"${String(v).replace(/"/g, '""')}"`;
        })
        .join(',')
    );
    const blob = new Blob(['\ufeff' + head + '\n' + lines.join('\n')], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${csvName}-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  };

  if (error) return <ErrorBanner message={error} />;
  if (loading) return <Loader />;

  return (
    <div>
      {csvName && rows.length > 0 && (
        <div className="table-tools">
          <button className="btn small" onClick={exportCsv}>
            ⬇ Export CSV
          </button>
        </div>
      )}
      {rows.length === 0 ? (
        <Empty text="No records match the filters" />
      ) : (
        <div className="table-wrap" style={maxHeight ? { maxHeight, overflowY: 'auto' } : undefined}>
          <table className={`table${dense ? ' dense' : ''}`}>
            <thead>
              <tr>
                {columns.map((c) => (
                  <th
                    key={c.key}
                    className={`${c.align === 'right' ? 'right' : ''} ${onSort && c.sortKey ? 'sortable' : ''} ${sort === c.sortKey ? (dir === 'desc' ? 'sorted-desc' : 'sorted-asc') : ''}`}
                    onClick={onSort && c.sortKey ? () => onSort(c.sortKey!, dir === 'desc' ? 'asc' : 'desc') : undefined}
                  >
                    {c.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr
                  key={i}
                  className={onRowClick ? 'clickable' : ''}
                  onClick={onRowClick ? () => onRowClick(r) : undefined}
                >
                  {columns.map((c) => (
                    <td key={c.key} className={c.align === 'right' ? 'right' : ''}>
                      {c.render ? c.render(r) : cellReact(c, r)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {total > 0 && (
        <div className="table-foot">
          <span className="muted">
            {total.toLocaleString('en-IN')} records · page {page} / {pages}
          </span>
          <div>
            <button className="btn small" disabled={page <= 1} onClick={() => onPage(page - 1)}>
              ‹ Prev
            </button>{' '}
            <button className="btn small" disabled={page >= pages} onClick={() => onPage(page + 1)}>
              Next ›
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function cellText(c: Col, r: Row): string {
  if (c.render) return str(c.render(r));
  const v = r[c.key];
  if (v === null || v === undefined) return '';
  if (typeof v === 'number') return c.num ? numOrNull(v)!.toLocaleString('en-IN') : String(v);
  return String(v);
}

function cellReact(c: Col, r: Row): React.ReactNode {
  const v = r[c.key];
  if (v === null || v === undefined || v === '') return <span className="muted">-</span>;
  if (typeof v === 'number' && c.num)
    return numOrNull(v)!.toLocaleString('en-IN', { maximumFractionDigits: 2 });
  return String(v);
}
