import { FormEvent, useEffect, useState } from "react";
import { api, fmtDate, fmtInt, str } from "./api";
import GraphExplorer from "./components/GraphExplorer";
import Tutorial from "./components/Tutorial";
import "./tutorial.css";
import {
  Activity,
  ArrowRight,
  Building2,
  ClipboardCheck,
  FileText,
  Network,
  Search,
} from "lucide-react";

type Route = { page: string; id?: string };
const getRoute = (): Route => {
  const raw = location.hash.replace(/^#!?\/?/, "") || "workspace";
  const p = raw.split("?")[0].split("/").filter(Boolean);
  return { page: p[0] || "workspace", id: p[1] };
};
const go = (p: string) => {
  location.hash = `#!/${p}`;
};
function Icon({ name }: { name: string }) {
  const icons: Record<string, any> = {
    search: Search,
    network: Network,
    building: Building2,
    tender: FileText,
    review: ClipboardCheck,
    arrow: ArrowRight,
    pulse: Activity,
  };
  const I = icons[name] || ArrowRight;
  return <I className="icon" size={17} strokeWidth={1.8} aria-hidden="true" />;
}
function Badge({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: string;
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}
function Fact({
  label,
  value,
  sub,
}: {
  label: string;
  value: React.ReactNode;
  sub?: string;
}) {
  return (
    <div className="fact">
      <span>{label}</span>
      <strong>{value}</strong>
      {sub && <small>{sub}</small>}
    </div>
  );
}
function mergeNetwork(prev: any, next: any) {
  if (!prev) return next;
  const nodes = [...(prev.nodes || [])];
  const ids = new Set(
    nodes.map((n: any) => String(n.node_id || n.bid_id || n.gid)),
  );
  for (const n of next.nodes || []) {
    const id = String(n.node_id || n.bid_id || n.gid);
    if (!ids.has(id)) {
      nodes.push(n);
      ids.add(id);
    }
  }
  const edges = [...(prev.edges || [])];
  const edgeIds = new Set(edges.map((e: any) => String(e.edge_id)));
  for (const e of next.edges || []) {
    if (!edgeIds.has(String(e.edge_id))) {
      edges.push(e);
      edgeIds.add(String(e.edge_id));
    }
  }
  return {
    ...prev,
    nodes,
    edges,
    depth: Math.max(prev.depth || 0, next.depth || 0),
    limit: Math.max(prev.limit || 0, next.limit || 0),
    truncated: Boolean(prev.truncated || next.truncated),
  };
}
function availableCount(value: any, loading: boolean) {
  return loading
    ? "Loading…"
    : value === null || value === undefined
      ? "Unavailable"
      : fmtInt(value);
}
function Shell({ children }: { children: React.ReactNode }) {
  const [r, setR] = useState(getRoute());
  const [q, setQ] = useState("");
  const [rs, setRs] = useState<any[]>([]);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const f = () => setR(getRoute());
    addEventListener("hashchange", f);
    return () => removeEventListener("hashchange", f);
  }, []);
  useEffect(() => {
    if (q.trim().length < 2) {
      setRs([]);
      return;
    }
    const t = setTimeout(
      () =>
        api
          .get<any>("search", { q })
          .then((x) => setRs(x.data || []))
          .catch(() => setRs([])),
      250,
    );
    return () => clearTimeout(t);
  }, [q]);
  const choose = (x: any) => {
    setQ("");
    setOpen(false);
    go(
      x.type === "company"
        ? `company/${x.id}`
        : x.type === "tender"
          ? `tender/${encodeURIComponent(x.id)}`
          : `bidder/${x.id}`,
    );
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (rs[0]) choose(rs[0]);
  };
  return (
    <div className="app-shell">
      <Tutorial onStart={(bid) => go("bidder/" + bid)} />
      <aside className="rail">
        <button className="wordmark" onClick={() => go("home")}>
          <span className="mark">
            <i />
            <i />
            <i />
          </span>
          TENDER<span>GRAPH</span>
        </button>
        <div className="rail-label">Investigate</div>
        <nav>
          <a
            className={r.page === "workspace" ? "active" : ""}
            href="#!/workspace"
          >
            <Icon name="search" />
            Workspace
          </a>
          <a href="#!/compare">
            <Icon name="network" />
            Compare
          </a>
          <a href="#!/company">
            <Icon name="building" />
            Companies
          </a>
          <a href="#!/tender">
            <Icon name="tender" />
            Tenders
          </a>
          <a className={r.page === "notable" ? "active" : ""} href="#!/notable">
            <Icon name="network" />
            Notable bidders
          </a>
        </nav>
        <div className="rail-bottom">
          <div className="live">
            <span />
            PostgreSQL connected
          </div>
          <small>HydraDB · bounded graph reads</small>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <form className="global-search" onSubmit={submit}>
            <Icon name="search" />
            <input
              value={q}
              onChange={(e) => {
                setQ(e.target.value);
                setOpen(true);
              }}
              onFocus={() => setOpen(true)}
              placeholder="Search companies, bidders, tenders, organizations"
              aria-label="Search records"
            />
            <kbd>⌘ K</kbd>
          </form>
          {open && q.length > 1 && (
            <div className="search-pop">
              {rs.length ? (
                rs.slice(0, 8).map((x, i) => (
                  <button key={i} onClick={() => choose(x)}>
                    <Badge tone={x.type}>{x.type}</Badge>
                    <span>
                      <strong>{str(x.label) || "Unnamed record"}</strong>
                      <small>{str(x.meta) || "Source record"}</small>
                    </span>
                    <Icon name="arrow" />
                  </button>
                ))
              ) : (
                <div className="search-none">No matching source records</div>
              )}
            </div>
          )}
          <div className="top-actions">
            <span className="env-dot" />
            Development evidence environment
          </div>
        </header>
        <div className="content-wrap">{children}</div>
      </main>
    </div>
  );
}
function Workspace() {
  const [k, setK] = useState<any>();
  useEffect(() => {
    api
      .get<any>("overview/kpis")
      .then(setK)
      .catch(() => {});
  }, []);
  return (
    <>
      <div className="hero">
        <div>
          <div className="kicker">TENDERGRAPH / INVESTIGATION WORKSPACE</div>
          <h1>Follow the evidence.</h1>
          <p>
            Explore procurement relationships across bidders, tenders and
            corporate records. Every connection stays tied to its source, method
            and uncertainty.
          </p>
        </div>
        <div className="hero-note">
          <Icon name="pulse" />
          Source facts are not conclusions.
          <br />
          Candidate relationships require review.
        </div>
      </div>
      <div className="entry-grid">
        <Entry
          icon="search"
          title="Start with a record"
          text="Search a company, bidder, tender or government organization."
          onClick={() =>
            document.querySelector<HTMLInputElement>(".global-search")?.focus()
          }
          primary
        />
        <Entry
          icon="network"
          title="Trace a bidder network"
          text="Inspect repeat co-bidding through actual tender observations."
          onClick={() => go("network")}
        />
        <Entry
          icon="network"
          title="Find notable bidders"
          text="Start with participant records that have many observed connections."
          onClick={() => go("notable")}
        />
      </div>
      <div className="workspace-actions">
        <button
          className="button small"
          onClick={() => window.dispatchEvent(new Event("tg:tutorial:replay"))}
        >
          Replay guided investigation
        </button>
      </div>
      <div className="section-head">
        <div>
          <div className="kicker">ORIENTATION</div>
          <h2>Read the graph carefully</h2>
        </div>
        <span className="quiet">
          A bounded workspace for evidence-backed investigation
        </span>
      </div>
      <div className="principles">
        <Principle
          n="01"
          t="Observed"
          x="Participant records, tender facts and award evidence come directly from PostgreSQL source tables."
        />
        <Principle
          n="02"
          t="Derived"
          x="Co-bidding counts, overlap and paths are calculations over observed records—not legal identities."
        />
        <Principle
          n="03"
          t="Asserted"
          x="Corporate associations remain candidates until an analyst records an explicit decision and rationale."
        />
      </div>
      <section className="availability">
        <div className="kicker">LIVE DATA SURFACE</div>
        <h3>Available to investigate</h3>
        <div className="availability-grid">
          <Fact
            label="Participant rows"
            value={availableCount(k?.financial_evaluation?.records, !k)}
          />
          <Fact
            label="Award evidence"
            value={availableCount(k?.awards?.award_records, !k)}
          />
          <Fact
            label="Source tenders"
            value={availableCount(k?.tender_lifecycle?.total_tenders, !k)}
          />
          <Fact
            label="Identity caution"
            value="Required"
            sub="ambiguity is visible"
          />
        </div>
      </section>
    </>
  );
}
function Entry({
  icon,
  title,
  text,
  onClick,
  primary = false,
}: {
  icon: string;
  title: string;
  text: string;
  onClick: () => void;
  primary?: boolean;
}) {
  return (
    <button
      className={`entry-card ${primary ? "primary" : ""}`}
      onClick={onClick}
    >
      <span className="entry-icon">
        <Icon name={icon} />
      </span>
      <span>
        <b>{title}</b>
        <small>{text}</small>
      </span>
      <Icon name="arrow" />
    </button>
  );
}
function Principle({ n, t, x }: { n: string; t: string; x: string }) {
  return (
    <div>
      <em>{n}</em>
      <b>{t}</b>
      <p>{x}</p>
    </div>
  );
}
function Bidder({ id }: { id?: string }) {
  const [d, setD] = useState<any>();
  const [n, setN] = useState<any>();
  const [left, setLeft] = useState("");
  const [right, setRight] = useState("");
  const [pair, setPair] = useState<any>();
  useEffect(() => {
    if (id) {
      api
        .get<any>(`bidders/${id}`, { limit: 30 })
        .then((x) => setD(x.data))
        .catch(() => {});
      api
        .get<any>(`bidders/${id}/network`, { depth: 2, limit: 40 })
        .then((x) => setN(x.data))
        .catch(() => {});
    }
  }, [id]);
  useEffect(() => {
    if (left && right && left !== right)
      api
        .get<any>("networks/pair", { left, right, limit: 50 })
        .then((x) => setPair(x.data))
        .catch(() => setPair(undefined));
  }, [left, right]);
  if (!id)
    return (
      <Landing
        title="Procurement network"
        text="Select a bidder from search to inspect participation and co-bidding evidence."
      />
    );
  if (!d) return <Loading />;
  const p = d;
  const bidNodes = (n?.nodes || []).filter((x: any) => x.node_type === "Bid");
  const navigate = (x: any) => {
    if (x.node_type === "CoBidObservation")
      go(
        `tender/${encodeURIComponent(x.external_tender_id || x.tender_gid || "")}`,
      );
    else if (x.bid_id !== undefined) go(`bidder/${String(x.bid_id)}`);
  };
  const expand = (bid: string) =>
    api
      .get<any>(`bidders/${bid}/network`, { depth: 3, limit: 50 })
      .then((x) => setN((prev: any) => mergeNetwork(prev, x.data)))
      .catch(() => {});
  return (
    <>
      <Crumb t="Network investigation" />
      <Heading
        kicker={`BIDDER RECORD / ${p.bid?.bid_gid || `BID:v:${id}`}`}
        title={p.bid?.name || `Participant record #${id}`}
        text={
          p.bid?.name
            ? `Observed key ${p.bid?.observed_participant_key || "unavailable"}. This is a participant record, not a confirmed legal identity.`
            : "Source participant name unavailable. This record has not been identified as a legal entity."
        }
      />
      <div className="facts four">
        <Fact label="Distinct tenders" value={fmtInt(p.distinct_tenders)} />
        <Fact label="Organizations" value={fmtInt(p.distinct_organizations)} />
        <Fact label="Bid records" value={fmtInt(p.bid_records)} />
        <Fact label="Shared tenders" value={fmtInt(p.co_bid_tender_count)} />
      </div>
      <section className="panel graph-panel">
        <PanelTitle
          e="RELATIONSHIP EXPLORER"
          t="Observed co-bidding network"
          n="Bid → CoBidObservation ← Bid. Select a node or edge to inspect its evidence."
        />
        {n ? (
          <GraphExplorer
            data={n}
            onExpand={expand}
            onNavigate={navigate}
            onCompare={(edge) =>
              go(
                "compare?left=" +
                  encodeURIComponent(edge.source) +
                  "&right=" +
                  encodeURIComponent(edge.target),
              )
            }
          />
        ) : (
          <Loading />
        )}
      </section>
      <div className="split">
        <section className="panel">
          <PanelTitle
            e="REPEAT CO-BIDDING"
            t="Frequent observed co-bidders"
            n="Shared tender counts"
          />
          <div className="list">
            {(p.top_co_bidders || []).map((x: any, i: number) => (
              <div className="list-row" key={i}>
                <span className="rank">0{i + 1}</span>
                <div>
                  <b>
                    {x.example_name ||
                      `Observed participant ${x.observed_participant_key || "key unavailable"}`}
                  </b>
                  <small>
                    {x.observed_participant_key || "Observed key unavailable"}
                  </small>
                </div>
                <strong>
                  {fmtInt(x.shared_tenders)} <small>shared</small>
                </strong>
              </div>
            ))}
            {!p.top_co_bidders?.length && (
              <Empty t="No repeat co-bidding observations for this record." />
            )}
          </div>
        </section>
        <section className="panel">
          <PanelTitle
            e="PARTICIPATION HISTORY"
            t="Observed tender records"
            n="Most recent first"
          />
          <Table
            headers={["Tender", "Organization", "Observed", "Rank"]}
            rows={(p.participation_history || []).map((x: any) => [
              <a href={`#!/tender/${encodeURIComponent(x.external_tender)}`}>
                {x.external_tender}
              </a>,
              x.organization || "Unavailable",
              fmtDate(x.submitted_at),
              x.financial_rank || "Unavailable",
            ])}
          />
        </section>
      </div>
      <section className="panel pair-panel">
        <PanelTitle
          e="PAIR COMPARISON"
          t="Compare observed tender histories"
          n="Overlap metrics are descriptive statistics, not collusion or identity scores."
        />
        <div className="pair-selects">
          <label>
            Bidder A
            <select value={left} onChange={(e) => setLeft(e.target.value)}>
              <option value="">Select a Bid record</option>
              {bidNodes.map((x: any) => (
                <option key={x.node_id} value={String(x.node_id || x.bid_id)}>
                  {x.participant_name || `Participant record #${x.bid_id}`}
                </option>
              ))}
            </select>
          </label>
          <label>
            Bidder B
            <select value={right} onChange={(e) => setRight(e.target.value)}>
              <option value="">Select a Bid record</option>
              {bidNodes.map((x: any) => (
                <option key={x.node_id} value={String(x.node_id || x.bid_id)}>
                  {x.participant_name || `Participant record #${x.bid_id}`}
                </option>
              ))}
            </select>
          </label>
        </div>
        {pair && <PairResult data={pair} />}
      </section>
    </>
  );
}
function PairResult({ data }: { data: any }) {
  return (
    <div className="pair-result">
      <div className="facts three">
        <Fact label="Shared tenders" value={fmtInt(data.shared_tender_count)} />
        <Fact
          label="Jaccard similarity"
          value={data.jaccard_similarity?.toFixed?.(3) || "—"}
          sub="shared / union"
        />
        <Fact
          label="Coverage A · B"
          value={`${(Number(data.left_coverage || 0) * 100).toFixed(1)}% · ${(Number(data.right_coverage || 0) * 100).toFixed(1)}%`}
          sub="shared / each history"
        />
      </div>
      <Table
        headers={["Shared tender", "Organization", "Observed", "Evidence"]}
        rows={(data.shared_tenders || []).map((x: any) => [
          <a href={`#!/tender/${encodeURIComponent(x.external_tender)}`}>
            {x.external_tender}
          </a>,
          x.organization || "—",
          fmtDate(x.observed_at),
          `${x.evidence?.length || 0} source refs`,
        ])}
      />
      <p className="microcopy">
        Jaccard = shared tenders / union of distinct tenders. Coverage measures
        the shared set relative to each bidder’s own observed tender history.
      </p>
    </div>
  );
}
function Compare() {
  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const [a, setA] = useState(params.get("left") || "");
  const [b, setB] = useState(params.get("right") || "");
  const [data, setData] = useState<any>();
  useEffect(() => {
    if (a && b && a !== b)
      api
        .get<any>("networks/pair", { left: a, right: b, limit: 50 })
        .then((x) => setData(x.data))
        .catch(() => setData(undefined));
  }, [a, b]);
  return (
    <>
      <Crumb t="Pair comparison" />
      <Heading
        kicker="PAIR COMPARISON / OBSERVED TENDER HISTORIES"
        title="Compare two bidder records"
        text="Overlap statistics describe shared tender histories. They are not collusion, fraud or identity scores."
      />
      <div className="compare-controls">
        <input
          aria-label="Bidder A"
          value={a}
          onChange={(e) => setA(e.target.value)}
          placeholder="Bidder A ID"
        />
        <span>×</span>
        <input
          aria-label="Bidder B"
          value={b}
          onChange={(e) => setB(e.target.value)}
          placeholder="Bidder B ID"
        />
      </div>
      {data ? (
        <PairResult data={data} />
      ) : (
        <Empty t="Enter two Bid record IDs to compare observed tender histories." />
      )}
      <div className="comparison-note">
        <b>Reading this:</b> shared tenders are observations. A small sample
        should not be treated as a signal or conclusion.
      </div>
    </>
  );
}
function Company({ id }: { id?: string }) {
  const [d, setD] = useState<any>();
  useEffect(() => {
    if (id)
      api
        .get<any>(`companies/${id}/intelligence`, { limit: 50 })
        .then((x) => setD(x.data))
        .catch(() => {});
  }, [id]);
  if (!id)
    return (
      <Landing
        title="Company intelligence"
        text="Select a CorporateEntity from search to inspect procurement exposure."
      />
    );
  if (!d) return <Loading />;
  const c = d.entity,
    e = d.procurement_exposure;
  return (
    <>
      <Crumb t="Company intelligence" />
      <Heading
        kicker={`CORPORATE ENTITY / ${c.entity_gid}`}
        title={c.canonical_name}
        text="Corporate records, procurement exposure and candidate vendor links in one evidence trail."
      />
      <div className="facts four">
        <Fact
          label="Contractor tenders"
          value={fmtInt(e.tender_count)}
          sub="on-record exposure"
        />
        <Fact
          label="Evidenced awards"
          value={fmtInt(e.evidenced_award_count)}
          sub="not recognized revenue"
        />
        <Fact
          label="Award exposure"
          value={
            e.total_award_value_inr_exact
              ? `₹${e.total_award_value_inr_exact}`
              : "—"
          }
          sub="exact source values"
        />
        <Fact
          label="Market snapshot"
          value={d.latest_market ? "Available" : "Unavailable"}
        />
      </div>
      <div className="split">
        <section className="panel">
          <PanelTitle
            e="IDENTITY REVIEW"
            t="Vendor associations"
            n="Candidate links never become automatic confirmations"
          />
          {(d.link_candidates || []).map((x: any, i: number) => (
            <div className="candidate-row" key={i}>
              <div>
                <b>{x.business_name || "Unnamed vendor"}</b>
                <small>
                  {x.vendor_gid} · {x.gstin || "GSTIN unavailable"}
                </small>
              </div>
              <div className="candidate-methods">
                {(x.methods || []).map((m: string) => (
                  <Badge
                    key={m}
                    tone={m.includes("AMBIGUOUS") ? "warn" : "candidate"}
                  >
                    {m}
                  </Badge>
                ))}
              </div>
            </div>
          ))}
          {!d.link_candidates?.length && (
            <Empty t="No candidate vendor associations were returned." />
          )}
        </section>
        <section className="panel">
          <PanelTitle
            e="SOURCE LIMITATIONS"
            t="What this view cannot establish"
          />
          <ul className="caveats">
            <li>{e.interpretation}</li>
            {(d.data_limitations || []).map((x: string) => (
              <li key={x}>{x}</li>
            ))}
          </ul>
        </section>
      </div>
      <section className="panel">
        <PanelTitle
          e="PROCUREMENT EXPOSURE"
          t="Tender and award evidence"
          n="Contractor-on-record association is separate from winner evidence"
        />
        <Table
          headers={[
            "Tender",
            "Organization",
            "Awardee record",
            "Value",
            "Association",
          ]}
          rows={(e.awards || []).map((x: any) => [
            <a href={`#!/tender/${encodeURIComponent(x.external_tender)}`}>
              {x.external_tender}
            </a>,
            x.organization || "—",
            x.awardee_name || "No awardee evidence",
            x.award_value_inr_exact ? `₹${x.award_value_inr_exact}` : "—",
            <Badge tone="fact">{x.association}</Badge>,
          ])}
        />
      </section>
    </>
  );
}
function Tender({ id }: { id?: string }) {
  const [d, setD] = useState<any>();
  useEffect(() => {
    if (id)
      api
        .get<any>(`tenders/${id}`)
        .then(setD)
        .catch(() => {});
  }, [id]);
  if (!id)
    return (
      <Landing
        title="Tender intelligence"
        text="Open a tender from search or an evidence table."
      />
    );
  if (!d) return <Loading />;
  const t = d.tender;
  return (
    <>
      <Crumb t="Tender intelligence" />
      <Heading
        kicker={`TENDER / ${str(t?.external_tender_id || id)}`}
        title={t?.title || t?.external_tender_id || id}
        text={`${t?.organization || "Issuing organization unavailable"} · source tender record`}
      />
      <div className="facts four">
        <Fact label="Participants" value={fmtInt(d.participants?.length)} />
        <Fact
          label="Award record"
          value={d.award ? "Available" : "Unavailable"}
        />
        <Fact label="Tender value" value={t?.tender_value_raw || "—"} />
        <Fact label="Lifecycle" value={t?.lifecycle_stage || "—"} />
      </div>
      <div className="split">
        <section className="panel">
          <PanelTitle
            e="PARTICIPANTS"
            t="Actual bid records"
            n="Participation is not a winner assertion"
          />
          <Table
            headers={["Participant", "Rank", "Bid value", "Status"]}
            rows={(d.participants || []).map((x: any) => [
              <>
                <a href={`#!/bidder/${x.participant_id}`}>
                  {x.participant_name || "Unnamed"}
                </a>
                <small className="block-code">
                  {x.participant_normalized || "observed key unavailable"}
                </small>
              </>,
              x.financial_rank || "—",
              x.bid_value || "—",
              x.is_winner ? (
                <Badge tone="fact">winner flag</Badge>
              ) : (
                "not classified"
              ),
            ])}
          />
        </section>
        <section className="panel">
          <PanelTitle
            e="AWARD EVIDENCE"
            t="Extracted outcome"
            n="Structural award evidence is separate from WON evidence"
          />
          {d.award ? (
            <div className="award-box">
              <Fact label="Awardee" value={d.award.awardee_name || "—"} />
              <Fact label="Value" value={d.award.awarded_value || "—"} />
              <Fact
                label="Contract date"
                value={fmtDate(d.award.contract_date)}
              />
              <Fact
                label="Winning bid no."
                value={d.award.winning_bid_number || "—"}
              />
            </div>
          ) : (
            <Empty t="No extracted award record is available." />
          )}
        </section>
      </div>
    </>
  );
}
function Home() {
  const guide = () => {
    localStorage.removeItem("tg.tutorial.v1");
    go("workspace");
    window.setTimeout(
      () => window.dispatchEvent(new Event("tg:tutorial:replay")),
      120,
    );
  };
  return (
    <main className="home-page">
      <section className="home-hero">
        <div className="home-hero-copy">
          <div className="kicker">INDIA · PUBLIC PROCUREMENT INTELLIGENCE</div>
          <h1>Procurement records are connected.</h1>
          <p>
            TenderGraph turns fragmented tender, bidder, award and corporate
            records into an evidence-backed investigation workspace.
          </p>
          <div className="home-actions">
            <button className="home-cta" onClick={guide}>
              <span>Guide me through TenderGraph</span>
              <ArrowRight size={18} />
              <i />
            </button>
            <button className="home-secondary" onClick={() => go("workspace")}>
              Open the ontology map
            </button>
          </div>
          <small className="home-caveat">
            Discover patterns. Inspect the evidence. Decide what deserves
            scrutiny.
          </small>
        </div>
        <div
          className="home-schematic"
          aria-label="TenderGraph relationship schematic"
        >
          <span className="schematic-line l1" />
          <span className="schematic-line l2" />
          <span className="schematic-line l3" />
          <div className="schematic-node primary">
            <b>Bidder</b>
            <small>observed record</small>
          </div>
          <div className="schematic-node tender">
            <b>Tender</b>
            <small>procurement event</small>
          </div>
          <div className="schematic-node award">
            <b>Award</b>
            <small>source evidence</small>
          </div>
          <div className="schematic-node company">
            <b>Company</b>
            <small>identity context</small>
          </div>
          <em>source-backed relationships</em>
        </div>
      </section>
      <section className="home-scale">
        <div>
          <strong>26,125</strong>
          <span>tenders</span>
        </div>
        <div>
          <strong>22,865</strong>
          <span>bidder participation records</span>
        </div>
        <div>
          <strong>20,329</strong>
          <span>award records</span>
        </div>
        <div>
          <strong>5,973</strong>
          <span>corporate entities</span>
        </div>
      </section>
      <section className="home-story">
        <div>
          <div className="kicker">WHY TENDERGRAPH</div>
          <h2>Move from an isolated tender to the network around it.</h2>
        </div>
        <div className="home-story-copy">
          <p>
            Public procurement data is spread across organisations, portals and
            individual transactions. TenderGraph connects the records without
            erasing their distinctions.
          </p>
          <p>
            Follow a bidder across tenders, inspect repeated shared-tender
            observations, trace award evidence and examine possible
            vendor–corporate links without treating candidates as confirmed
            identities.
          </p>
        </div>
      </section>
      <section className="home-principles">
        <article>
          <span>01</span>
          <h3>Observe</h3>
          <p>
            Participant records, tender facts and awards remain tied to their
            source tables.
          </p>
        </article>
        <article>
          <span>02</span>
          <h3>Connect</h3>
          <p>
            HydraDB makes bounded relationship exploration possible across
            multiple procurement events.
          </p>
        </article>
        <article>
          <span>03</span>
          <h3>Interpret carefully</h3>
          <p>
            Repeated co-bidding is an observation—not proof of collusion, fraud
            or legal identity.
          </p>
        </article>
      </section>
      <footer className="home-footer">
        Built for procurement auditors, oversight teams, investigative
        journalists and public procurement researchers.
      </footer>
    </main>
  );
}
function GodView() {
  const nodes = [
    {
      id: "bid",
      label: "Bid",
      sub: "participant record",
      x: 8,
      y: 42,
      action: () => go("notable"),
    },
    {
      id: "cobid",
      label: "Shared tender",
      sub: "CoBidObservation",
      x: 31,
      y: 42,
      action: () => go("notable"),
    },
    {
      id: "tender",
      label: "Tender",
      sub: "procurement event",
      x: 53,
      y: 22,
      action: () => go("tender"),
    },
    {
      id: "gov",
      label: "Government organization",
      sub: "issuer",
      x: 78,
      y: 13,
      action: () => go("tender"),
    },
    {
      id: "award",
      label: "Award",
      sub: "extracted evidence",
      x: 78,
      y: 40,
      action: () => go("tender"),
    },
    {
      id: "vendor",
      label: "Vendor",
      sub: "contractor record",
      x: 53,
      y: 67,
      action: () => go("company"),
    },
    {
      id: "corp",
      label: "Corporate entity",
      sub: "legal-entity record",
      x: 78,
      y: 67,
      action: () => go("company"),
    },
    {
      id: "evidence",
      label: "Alias / GST registration",
      sub: "supporting identity evidence",
      x: 78,
      y: 88,
      action: () => go("company"),
    },
  ];
  const links = [
    ["bid", "cobid", "CO_BID_IN"],
    ["cobid", "bid", "CO_BID_IN"],
    ["bid", "tender", "SUBMITTED_BID"],
    ["tender", "gov", "ISSUED_BY"],
    ["tender", "award", "HAS_AWARD"],
    ["tender", "vendor", "CONTRACTOR_ON_RECORD"],
    ["vendor", "corp", "IDENTIFIED_AS · only when evidenced"],
    ["corp", "evidence", "HAS_ALIAS / HAS_GST_REGISTRATION"],
  ] as const;
  const byID = (id: string) => nodes.find((n) => n.id === id)!;
  return (
    <>
      <Crumb t="Explore" />
      <Heading
        kicker="TENDERGRAPH / ONTOLOGY MAP"
        title="Explore the evidence model"
        text="A map of the record types and relationships TenderGraph can investigate. Start at a layer, then open the bounded source-backed views behind it."
      />
      <section className="ontology-shell panel">
        <div className="ontology-heading">
          <div>
            <div className="kicker">GOD VIEW · BOUNDED ONTOLOGY</div>
            <h2>How procurement records connect</h2>
            <p>
              Relationships are shown as the system interprets them. A shared
              tender is an observation; an identity link is only available when
              evidence supports it.
            </p>
          </div>
          <div className="ontology-legend">
            <span className="observed-dot" />
            Observed source relationship
            <span className="derived-dot" />
            Derived investigation link
          </div>
        </div>
        <div
          className="ontology-canvas"
          aria-label="TenderGraph ontology relationship map"
        >
          <svg
            viewBox="0 0 100 100"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            {links.map(([a, b, label]) => {
              const from = byID(a),
                to = byID(b);
              return (
                <g key={a + b}>
                  <line
                    x1={`${from.x + 9}%`}
                    y1={`${from.y + 5}%`}
                    x2={`${to.x + 1}%`}
                    y2={`${to.y + 5}%`}
                  />
                  <text
                    x={`${(from.x + to.x) / 2}%`}
                    y={`${(from.y + to.y) / 2 + 3}%`}
                  >
                    {label}
                  </text>
                </g>
              );
            })}
          </svg>
          {nodes.map((n) => (
            <button
              key={n.id}
              className={`ontology-node ontology-${n.id}`}
              style={{ left: `${n.x}%`, top: `${n.y}%` }}
              onClick={n.action}
            >
              <strong>{n.label}</strong>
              <small>{n.sub}</small>
              <span>Explore layer →</span>
            </button>
          ))}
        </div>
        <div className="ontology-relations">
          <div className="kicker">RELATIONSHIP CATALOGUE</div>
          <div className="ontology-relation-grid">
            {links.map(([a, b, label]) => (
              <div key={a + b} className="ontology-relation">
                <b>{label.split(" · ")[0]}</b>
                <span>
                  {byID(a).label} → {byID(b).label}
                </span>
              </div>
            ))}
          </div>
        </div>
        <div className="ontology-caveat">
          This view describes the ontology, not a claim that every possible
          relationship is present for every record. Investigation pages use
          bounded queries and expose the underlying source evidence.
        </div>
      </section>
    </>
  );
}
function NotableBidders() {
  const [rows, setRows] = useState<any[]>([]);
  useEffect(() => {
    api
      .get<any>("networks/notable-bidders", { limit: 40 })
      .then((x) => setRows(x.data || []))
      .catch(() => setRows([]));
  }, []);
  return (
    <>
      <Crumb t="Notable bidders" />
      <Heading
        kicker="OBSERVED NETWORKS / EXPLORATION INDEX"
        title="Bidders with wide observed connections"
        text="Start an investigation with participant records that appear alongside many distinct observed co-bidders. This is a discovery index, not a risk or wrongdoing score."
      />
      <section className="panel notable-intro">
        <PanelTitle
          e="HOW TO READ THIS"
          t="Follow the evidence"
          n="Each row is an observed participant key. Open it to inspect the actual tenders and source records behind the connections."
        />
        <div className="notable-note">
          Repeated participation is an observation. It does not establish
          collusion, legal identity or wrongdoing.
        </div>
      </section>
      <section className="panel notable-list">
        <PanelTitle
          e="NOTABLE BIDDER RECORDS"
          t={`${rows.length} investigation starting points`}
          n="Ordered by distinct observed co-bidders"
        />
        <Table
          headers={[
            "Bidder record",
            "Observed tenders",
            "Organizations",
            "Observed connections",
            "Action",
          ]}
          rows={rows.map((x: any, i: number) => [
            <div>
              <b>{x.example_name || "Unnamed participant"}</b>
              <small className="block-code">
                Observed key: {x.observed_participant_key || "unavailable"}
              </small>
            </div>,
            fmtInt(x.distinct_tenders),
            fmtInt(x.distinct_organizations),
            fmtInt(x.distinct_co_bidders),
            <button
              className="button small"
              onClick={() =>
                go(`bidder/${encodeURIComponent(String(x.bid_id))}`)
              }
            >
              Investigate <span>→</span>
            </button>,
          ])}
        />
        {!rows.length && (
          <Empty t="No notable bidder records are currently available." />
        )}
      </section>
    </>
  );
}
function Review() {
  const [items, setItems] = useState<any[]>([]);
  const [selected, setSelected] = useState<any>();
  const [msg, setMsg] = useState("");
  const load = () =>
    api
      .get<any>("review/candidates", { status: "PENDING", limit: 50 })
      .then((x) => setItems(x.data || []))
      .catch(() => setItems([]));
  useEffect(() => {
    load();
  }, []);
  const decide = async (status: string) => {
    if (!selected) return;
    const rationale = prompt("Rationale required");
    if (!rationale) return;
    try {
      await api.post(`review/candidates/${selected.candidate_id}/decision`, {
        status,
        rationale,
        actor_id: "workspace-operator",
        actor_kind: "OPERATOR_SUPPLIED",
        expected_version: selected.current_version,
      });
      setMsg(
        "Decision recorded in PostgreSQL and queued as desired graph state.",
      );
      setSelected(undefined);
      load();
    } catch (e: any) {
      setMsg(e.message);
    }
  };
  return (
    <>
      <Heading
        kicker="ANALYST REVIEW / CANDIDATE QUEUE"
        title="Review the uncertain edges."
        text="System candidates are hypotheses. Confirming one records an analyst assertion; it does not rewrite the source record."
      />
      <>{msg && <div className="notice">{msg}</div>}</>
      <div className="review-layout">
        <section className="panel queue">
          <PanelTitle
            e="PENDING CANDIDATES"
            t={`${items.length} available`}
            n="Evidence-backed review queue"
          />
          {items.map((x) => (
            <button
              className={`queue-row ${selected?.candidate_id === x.candidate_id ? "selected" : ""}`}
              key={x.candidate_id}
              onClick={() => setSelected(x)}
            >
              <div>
                <Badge tone="candidate">{x.relationship_type}</Badge>
                <b>
                  {x.source_gid} → {x.target_gid}
                </b>
                <small>
                  {x.generation_method} · version {x.current_version}
                </small>
              </div>
              <Icon name="arrow" />
            </button>
          ))}
          {!items.length && <Empty t="No pending candidates are available." />}
        </section>
        <section className="panel review-detail">
          {selected ? (
            <>
              <PanelTitle
                e="CANDIDATE EVIDENCE"
                t={selected.relationship_type}
                n={selected.candidate_gid}
              />
              <div className="relationship">
                <code>{selected.source_gid}</code>
                <span>→</span>
                <code>{selected.target_gid}</code>
              </div>
              <Evidence
                label="Initial evidence"
                value={selected.initial_evidence}
              />
              <Evidence
                label="Latest observed evidence"
                value={selected.latest_evidence}
              />
              <div className="decision-actions">
                <button
                  className="button solid"
                  onClick={() => decide("CONFIRMED")}
                >
                  Confirm assertion
                </button>
                <button
                  className="button"
                  onClick={() => decide("NEEDS_REVIEW")}
                >
                  Needs review
                </button>
                <button
                  className="button danger"
                  onClick={() => decide("REJECTED")}
                >
                  Reject candidate
                </button>
              </div>
              <p className="microcopy">
                Every action requires a rationale and records an
                operator-supplied actor until authentication is available.
              </p>
            </>
          ) : (
            <Empty t="Select a candidate to inspect its evidence and history." />
          )}
        </section>
      </div>
    </>
  );
}
function Empty({ t }: { t: string }) {
  return <div className="empty">{t}</div>;
}
function Evidence({ label, value }: { label: string; value: any }) {
  return (
    <div className="evidence-block">
      <label>{label}</label>
      <pre>{JSON.stringify(value, null, 2)}</pre>
    </div>
  );
}
function Table({ headers, rows }: { headers: string[]; rows: any[][] }) {
  return (
    <div className="table-scroll">
      <table>
        <thead>
          <tr>
            {headers.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i}>
              {row.map((x, j) => (
                <td key={j}>{x}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
function Heading({
  kicker,
  title,
  text,
}: {
  kicker: string;
  title: string;
  text: string;
}) {
  return (
    <div className="page-heading">
      <div>
        <div className="kicker">{kicker}</div>
        <h1>{title}</h1>
        <p className="lede">{text}</p>
      </div>
    </div>
  );
}
function PanelTitle({ e, t, n }: { e: string; t: string; n?: string }) {
  return (
    <div className="panel-title">
      <div>
        <div className="kicker">{e}</div>
        <h2>{t}</h2>
        {n && <p>{n}</p>}
      </div>
    </div>
  );
}
function Crumb({ t }: { t: string }) {
  return (
    <div className="crumb">
      <button onClick={() => go("workspace")}>Workspace</button>
      <span>/</span>
      {t}
    </div>
  );
}
function Landing({ title, text }: { title: string; text: string }) {
  return (
    <>
      <div className="hero compact">
        <div>
          <div className="kicker">INVESTIGATION ENTRY</div>
          <h1>{title}</h1>
          <p>{text}</p>
        </div>
      </div>
      <div className="empty-large">
        <Icon name="search" />
        <b>Use the search field above to begin</b>
        <span>
          Results preserve the distinction between companies, bidders and tender
          records.
        </span>
      </div>
    </>
  );
}
function Loading() {
  return (
    <div className="loading">
      <div />
      <div />
      <div />
    </div>
  );
}
export default function App() {
  const [r, setR] = useState(getRoute());
  useEffect(() => {
    const f = () => setR(getRoute());
    addEventListener("hashchange", f);
    return () => removeEventListener("hashchange", f);
  }, []);
  let p: React.ReactNode =
    r.page === "home" ? (
      <Home />
    ) : r.page === "bidder" ? (
      <Bidder id={r.id} />
    ) : r.page === "company" ? (
      <Company id={r.id} />
    ) : r.page === "tender" ? (
      <Tender id={r.id} />
    ) : r.page === "notable" ? (
      <NotableBidders />
    ) : r.page === "review" ? (
      <Review />
    ) : r.page === "network" ? (
      <GodView />
    ) : r.page === "compare" ? (
      <Compare />
    ) : r.page === "workspace" ? (
      <GodView />
    ) : (
      <WorkspaceReference />
    );
  return <Shell>{p}</Shell>;
}

function HeroSearch() {
  const [q, setQ] = useState("");
  const [rs, setRs] = useState<any[]>([]);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (q.trim().length < 2) {
      setRs([]);
      return;
    }
    const t = setTimeout(
      () =>
        api
          .get<any>("search", { q })
          .then((x) => setRs(x.data || []))
          .catch(() => setRs([])),
      250,
    );
    return () => clearTimeout(t);
  }, [q]);
  const choose = (x: any) => {
    setQ("");
    setOpen(false);
    go(
      x.type === "company"
        ? `company/${x.id}`
        : x.type === "tender"
          ? `tender/${encodeURIComponent(x.id)}`
          : `bidder/${x.id}`,
    );
  };
  return (
    <div className="hero-search-wrap">
      <form
        className="hero-search"
        onSubmit={(e) => {
          e.preventDefault();
          if (rs[0]) choose(rs[0]);
        }}
      >
        <Search size={19} />
        <input
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          placeholder="Search for a company, bidder or tender…"
          aria-label="Search for a company, bidder or tender"
        />
        <kbd>⌘ K</kbd>
      </form>
      {open && q.length > 1 && (
        <div className="search-pop hero-search-pop">
          {rs.length ? (
            rs.slice(0, 6).map((x, i) => (
              <button key={i} onClick={() => choose(x)}>
                <Badge tone={x.type}>{x.type}</Badge>
                <span>
                  <strong>{str(x.label) || "Unnamed record"}</strong>
                  <small>{str(x.meta) || "Source record"}</small>
                </span>
                <ArrowRight size={16} />
              </button>
            ))
          ) : (
            <div className="search-none">No matching source records</div>
          )}
        </div>
      )}
    </div>
  );
}
function WorkspaceReference() {
  const [k, setK] = useState<any>();
  useEffect(() => {
    api
      .get<any>("overview/kpis")
      .then(setK)
      .catch(() => {});
  }, []);
  return (
    <>
      <div className="hero">
        <div>
          <div className="kicker">Procurement investigation</div>
          <h1>Explore procurement relationships</h1>
          <p>
            See how companies, bidders and tenders are connected through real
            procurement records.
          </p>
          <HeroSearch />
          <div className="workspace-actions">
            <button
              className="button small"
              onClick={() =>
                window.dispatchEvent(new Event("tg:tutorial:replay"))
              }
            >
              Replay guided investigation
            </button>
          </div>
        </div>
      </div>
      <div className="entry-grid">
        <Entry
          icon="building"
          title="Find a company"
          text="View its tenders and related bidders"
          onClick={() => go("company")}
        />
        <Entry
          icon="network"
          title="Explore related bidders"
          text="See who appears together"
          onClick={() => go("network")}
        />
        <Entry
          icon="network"
          title="Find notable bidders"
          text="Start with participant records that have many observed connections"
          onClick={() => go("notable")}
        />
      </div>
      <section className="availability">
        <div className="kicker">Current data surface</div>
        <h3>Available to investigate</h3>
        <div className="availability-grid">
          <Fact
            label="Participant rows"
            value={availableCount(k?.financial_evaluation?.records, !k)}
          />
          <Fact
            label="Award evidence"
            value={availableCount(k?.awards?.award_records, !k)}
          />
          <Fact
            label="Source tenders"
            value={availableCount(k?.tender_lifecycle?.total_tenders, !k)}
          />
          <Fact
            label="Identity caution"
            value="Required"
            sub="ambiguity stays visible"
          />
        </div>
      </section>
    </>
  );
}
