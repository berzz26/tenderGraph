import React, { FormEvent, useEffect, useRef, useState } from 'react';
import { api, str } from './api';
import { useHashRoute } from './router';
import Overview from './pages/Overview';
import FinancialEvaluation from './pages/FinancialEvaluation';
import AwardedContracts from './pages/AwardedContracts';
import AwardIntelligence from './pages/AwardIntelligence';
import EntityResolution from './pages/EntityResolution';
import Materiality from './pages/Materiality';
import Companies from './pages/Companies';
import DataQuality from './pages/DataQuality';
import TenderDetail from './pages/TenderDetail';
import TenderDirectory from './pages/TenderDirectory';

const NAV = [
  { id: 'overview', label: 'Overview', group: 'Workspace' },
  { id: 'financial-evaluation', label: 'Financial Evaluation', group: 'Procurement' },
  { id: 'awards', label: 'Awarded Contracts', group: 'Procurement' },
  { id: 'award-intelligence', label: 'Award Intelligence', group: 'Procurement' },
  { id: 'tender-detail', label: 'Tender Detail', group: 'Procurement' },
  { id: 'resolution', label: 'Entity Resolution', group: 'Data operations' },
  { id: 'materiality', label: 'Financial Materiality', group: 'Research' },
  { id: 'companies', label: 'Company Intelligence', group: 'Research' },
  { id: 'quality', label: 'Data Quality', group: 'Data operations' },
];

export default function App() {
  const { segments } = useHashRoute();
  const [search, setSearch] = useState('');
  const [searchOpen, setSearchOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  const [results, setResults] = useState<{ type: string; label: string; meta: string; href: string }[]>([]);
  const searchRef = useRef<HTMLDivElement>(null);
  const page = segments[0] || 'overview';
  const entityId = page === 'company' ? segments[1] : undefined;
  const materialityEntityId = page === 'materiality' && segments[1] === 'company' ? segments[2] : undefined;
  const tenderId = page === 'tender' ? segments[1] : undefined;

  const knownPage =
    NAV.some((n) => n.id === page) || page === 'company' || (page === 'tender' && tenderId !== undefined);

  const runSearch = async (event: FormEvent) => {
    event.preventDefault();
    const query = search.trim();
    if (!query) { setResults([]); setSearchOpen(false); return; }
    setSearching(true); setSearchOpen(true);
    try {
      const [companies, evaluations, awards] = await Promise.all([
        api.get<any>('companies', { q: query, page: 1, page_size: 5 }),
        api.get<any>('financial-evaluation', { q: query, page: 1, page_size: 5 }),
        api.get<any>('awards', { q: query, page: 1, page_size: 5 }),
      ]);
      const next = [
        ...(companies.data || []).map((row: any) => ({ type: 'Company', label: str(row.canonical_name), meta: `${str(row.ticker_nse) || 'No NSE ticker'} · ${str(row.sector) || 'Sector unavailable'}`, href: `#!/company/${row.entity_id}` })),
        ...(evaluations.data || []).map((row: any) => ({ type: 'Tender', label: str(row.title) || str(row.external_tender_id), meta: `${str(row.external_tender_id)} · ${str(row.buyer_org) || 'Organization unavailable'}`, href: `#!/tender/${encodeURIComponent(str(row.external_tender_id))}` })),
        ...(awards.data || []).map((row: any) => ({ type: 'Award', label: str(row.awardee_name) || str(row.external_tender_id), meta: `${str(row.external_tender_id)} · ${str(row.buyer_org) || 'Organization unavailable'}`, href: `#!/tender/${encodeURIComponent(str(row.external_tender_id))}` })),
      ].filter((row) => row.label).slice(0, 12);
      setResults(next);
    } catch {
      setResults([]);
    } finally { setSearching(false); }
  };

  useEffect(() => {
    const closeOnOutsideClick = (event: MouseEvent) => {
      if (searchRef.current && !searchRef.current.contains(event.target as Node)) setSearchOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') setSearchOpen(false); };
    document.addEventListener('mousedown', closeOnOutsideClick);
    document.addEventListener('keydown', closeOnEscape);
    return () => { document.removeEventListener('mousedown', closeOnOutsideClick); document.removeEventListener('keydown', closeOnEscape); };
  }, []);

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">VY</div>
          <div>
            <div className="brand-name">VYUHA</div>
            <div className="brand-sub">Procurement Intelligence</div>
          </div>
        </div>
        <nav className="primary-nav" aria-label="Primary navigation">
          {['Workspace', 'Procurement', 'Research', 'Data operations'].map((group) => <div className="nav-group" key={group}>
            <div className="nav-group-label">{group}</div>
            {NAV.filter((n) => n.group === group).map((n) => <a key={n.id} href={`#!/${n.id}`} className={page === n.id || (page === 'company' && n.id === 'companies') || (page === 'tender' && n.id === 'tender-detail') ? 'active' : ''}><span className="nav-marker" aria-hidden="true" />{n.label}</a>)}
          </div>)}
        </nav>
        <div className="sidebar-foot">
          <span className="status-dot" /> live research workspace
          <br />
          deterministic entity resolution
        </div>
      </aside>
      <main className="content">
        <div className="topbar"><div className="search-anchor" ref={searchRef}><form className="topbar-search" onSubmit={runSearch} role="search"><span aria-hidden="true">⌕</span><input aria-label="Search tenders, companies, and organizations" placeholder="Search tenders, companies, organizations…" value={search} onChange={(event) => { setSearch(event.target.value); if (event.target.value) setSearchOpen(true); }} onFocus={() => search && setSearchOpen(true)} /><button type="button" className="search-clear" aria-label="Close search" onClick={() => { setSearch(''); setResults([]); setSearchOpen(false); }}>×</button><kbd>⌘ K</kbd></form>{searchOpen && <div className="search-results">{searching ? <div className="search-empty">Searching Vyuha records…</div> : results.length ? results.map((result, index) => <a key={`${result.href}-${index}`} href={result.href} className="search-result" onClick={() => setSearchOpen(false)}><span className="search-result-type">{result.type}</span><span><b>{result.label}</b><small>{result.meta}</small></span></a>) : <div className="search-empty">No matching companies, tenders, or awards found.</div>}</div>}</div><div className="topbar-actions"><span className="date-control">Last 12 months⌄</span><span className="notification-dot" aria-label="Notifications" /></div></div>
        {page === 'overview' && <Overview />}
        {page === 'financial-evaluation' && <FinancialEvaluation />}
        {page === 'awards' && <AwardedContracts />}
        {page === 'award-intelligence' && <AwardIntelligence />}
        {page === 'tender-detail' && <TenderDirectory />}
        {page === 'resolution' && <EntityResolution />}
        {page === 'materiality' && <Materiality detailId={materialityEntityId} />}
        {page === 'companies' && <Companies />}
        {page === 'company' && entityId !== undefined && <Companies detailId={entityId} />}
        {page === 'tender' && tenderId !== undefined && <TenderDetail tenderId={tenderId} />}
        {page === 'quality' && <DataQuality />}
        {!knownPage && <div className="empty">Unknown page</div>}
      </main>
    </div>
  );
}
