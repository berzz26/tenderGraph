import React from 'react';
import { BarChart as MUIBarChart } from '@mui/x-charts/BarChart';
import { LineChart as MUILineChart } from '@mui/x-charts/LineChart';
import { fmtInt, fmtCr, numOrNull, str } from '../api';

export type Item = { label: string; value: number; sub?: any };
const chartMargin = { top: 12, right: 18, bottom: 48, left: 72 };
const axisStyle = { tickLabelStyle: { fill: '#71819a', fontSize: 10 } };

export function BarChart({ items, money = false, height = 250 }: { items: Item[]; money?: boolean; height?: number }) {
  if (!items.length) return <div className="empty">No data</div>;
  const labels = items.map((item) => item.label.length > 24 ? `${item.label.slice(0, 23)}…` : item.label);
  const values = items.map((item) => item.value);
  const formatter = money ? (value: number | null) => value == null ? '-' : fmtCr(value) : (value: number | null) => value == null ? '-' : fmtInt(value);
  return <div className="chart-shell" aria-label="Bar chart"><MUIBarChart height={height} margin={chartMargin} xAxis={[{ scaleType: 'band', data: labels, ...axisStyle }]} yAxis={[{ valueFormatter: formatter, ...axisStyle }]} series={[{ data: values, label: money ? 'Value' : 'Count', valueFormatter: formatter, color: '#4f8fe8' }]} borderRadius={5} hideLegend sx={{ '& .MuiChartsAxis-line': { stroke: '#d7e1ed' }, '& .MuiChartsAxis-tick': { stroke: '#d7e1ed' }, '& .MuiChartsGrid-line': { stroke: '#edf1f6' } }} /></div>;
}

export function LineChart({ points, lines, height = 220 }: { points?: { label: string; value: number }[]; lines?: { label: string; data: (number | null)[]; color?: string; money?: boolean; area?: boolean }[]; height?: number; logScale?: boolean }) {
  const labels = points?.map((point) => point.label) || [];
  const chartLines = lines || [{ label: 'Positions', data: (points || []).map((point) => point.value), color: '#4d9ef7' }];
  if (labels.length < 2 || chartLines.every((line) => line.data.length < 2)) return <div className="empty">Not enough dated observations</div>;
  return <div className="chart-shell" aria-label="Trend line chart"><MUILineChart height={height} margin={chartMargin} xAxis={[{ scaleType: 'point', data: labels, ...axisStyle }]} yAxis={[axisStyle]} series={chartLines.map((line) => ({ data: line.data, label: line.label, color: line.color || '#4f8fe8', area: line.area, valueFormatter: (value: number | null) => value == null ? '-' : line.money ? fmtCr(value) : fmtInt(value) }))} sx={{ '& .MuiChartsAxis-line': { stroke: '#d7e1ed' }, '& .MuiChartsAxis-tick': { stroke: '#d7e1ed' }, '& .MuiChartsGrid-line': { stroke: '#edf1f6' } }} /></div>;
}

export function KvGrid({ rows }: { rows: [string, React.ReactNode][] }) {
  return <div className="kv-grid">{rows.map(([key, value], index) => <div className="kv" key={index}><span className="kv-k">{key}</span><span className="kv-v">{value}</span></div>)}</div>;
}

export const asItem = (row: Record<string, any>, labelKey: string, valueKey: string): Item => ({ label: str(row[labelKey]) || '(unknown)', value: numOrNull(row[valueKey]) ?? 0 });
