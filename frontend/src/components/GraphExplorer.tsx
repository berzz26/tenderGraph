import { useEffect, useMemo, useRef, useState } from 'react';
import cytoscape, { Core, ElementDefinition } from 'cytoscape';

export type ExplorerNode = {
  node_id?: string; gid?: string; bid_id?: number | string; bid_gid?: string;
  node_type?: string; participant_name?: string; observed_participant_key?: string;
  depth?: number; source_table?: string; source_pk?: string; tender_id?: number | string;
  tender_gid?: string; external_tender_id?: string;
};
export type ExplorerEdge = {
  edge_id: string; source: string; target: string; type: string; label?: string;
  observation_gid?: string; tender_id?: number | string; tender_gid?: string;
  evidence?: { source_table: string; source_pk: string; gid: string }[];
};
export type ExplorerPayload = { nodes: ExplorerNode[]; edges: ExplorerEdge[]; depth: number; limit: number; truncated?: boolean };

function nodeID(n: ExplorerNode) { return String(n.node_id || n.bid_id || n.gid || n.bid_gid || ''); }
function nodeLabel(n: ExplorerNode) {
  if (n.node_type === 'CoBidObservation') return n.participant_name || n.tender_gid || 'Shared tender observation';
  return n.participant_name || (n.bid_id !== undefined ? `Participant record #${n.bid_id}` : 'Graph node');
}

export default function GraphExplorer({ data, onExpand, onNavigate }: { data: ExplorerPayload; onExpand?: (bidID: string) => void; onNavigate?: (node: ExplorerNode) => void }) {
  const host = useRef<HTMLDivElement>(null); const cyRef = useRef<Core | null>(null);
  const [selected, setSelected] = useState<ExplorerNode | null>(null); const [selectedEdge, setSelectedEdge] = useState<ExplorerEdge | null>(null);
  const [visible, setVisible] = useState<Record<string, boolean>>({ Bid: true, CoBidObservation: true });
  const nodesByID = useMemo(() => new Map((data.nodes || []).map(n => [nodeID(n), n])), [data.nodes]);

  const render = () => {
    const cy = cyRef.current; if (!cy) return;
    cy.elements().remove();
    const elements: ElementDefinition[] = (data.nodes || []).filter(n => visible[n.node_type || 'Bid'] !== false).map(n => ({ data: { id: nodeID(n), label: nodeLabel(n), type: n.node_type || 'Bid', node: n } }));
    const allowed = new Set(elements.map(e => String(e.data?.id)));
    (data.edges || []).filter(e => allowed.has(String(e.source)) && allowed.has(String(e.target))).forEach(e => elements.push({ data: { id: e.edge_id, source: String(e.source), target: String(e.target), label: e.label || e.type, type: e.type, edge: e } }));
    cy.add(elements); cy.layout({ name: 'cose', animate: false, fit: true, padding: 42, randomize: true }).run();
  };
  useEffect(() => {
    if (!host.current) return;
    const cy = cytoscape({ container: host.current, minZoom: .25, maxZoom: 3, wheelSensitivity: .18, boxSelectionEnabled: false, style: [
      { selector: 'node', style: { 'background-color': '#2d6691', 'border-width': 2, 'border-color': '#ffffff', 'label': 'data(label)', 'color': '#172231', 'font-family': 'DM Sans', 'font-size': 11, 'text-wrap': 'wrap', 'text-max-width': '110px', 'text-valign': 'bottom', 'text-margin-y': 8, 'width': 30, 'height': 30 } },
      { selector: 'node[type="CoBidObservation"]', style: { 'background-color': '#a8641e', 'shape': 'diamond', 'width': 24, 'height': 24, 'font-size': 9 } },
      { selector: 'node:selected', style: { 'border-color': '#d8a467', 'border-width': 5, 'overlay-color': '#d8a467', 'overlay-opacity': .13 } },
      { selector: 'edge', style: { 'curve-style': 'bezier', 'line-color': '#aebbc4', 'target-arrow-color': '#aebbc4', 'target-arrow-shape': 'triangle', 'width': 1.5, 'label': 'data(label)', 'font-size': 8, 'color': '#6f7c8c', 'text-background-color': '#fff', 'text-background-opacity': .9, 'text-background-padding': '2px' } },
      { selector: 'edge:selected', style: { 'line-color': '#a8641e', 'target-arrow-color': '#a8641e', 'width': 3 } },
    ] });
    cy.on('tap', 'node', e => { const picked = e.target.data('node') as ExplorerNode; if (picked.node_id) picked.bid_id = picked.node_id; setSelected(picked); setSelectedEdge(null); });
    cy.on('tap', 'edge', e => { setSelectedEdge(e.target.data('edge') as ExplorerEdge); setSelected(null); });
    cyRef.current = cy; render(); return () => { cy.destroy(); cyRef.current = null; };
  }, []);
  useEffect(render, [data, visible]);
  const selectedID = selected && nodeID(selected);
  return <div className="explorer-shell"><div className="graph-toolbar"><div><b>Bounded graph</b><small>{data.nodes?.length || 0} nodes · {data.edges?.length || 0} edges · depth {data.depth} · cap {data.limit}</small></div><div className="graph-actions"><button onClick={() => cyRef.current?.zoom(cyRef.current.zoom() * 1.2)}>+</button><button onClick={() => cyRef.current?.zoom(cyRef.current.zoom() / 1.2)}>−</button><button onClick={() => cyRef.current?.fit(undefined, 38)}>Fit</button><button onClick={() => render()}>Reset layout</button></div></div><div className="graph-filters"><label><input type="checkbox" checked={visible.Bid !== false} onChange={e => setVisible(v => ({ ...v, Bid: e.target.checked }))}/> Bid records</label><label><input type="checkbox" checked={visible.CoBidObservation !== false} onChange={e => setVisible(v => ({ ...v, CoBidObservation: e.target.checked }))}/> Shared-tender observations</label>{data.truncated&&<span className="graph-warning">Result capped; expand with an explicit request.</span>}</div><div className="graph-main"><div className="cy-canvas" ref={host} aria-label="Interactive procurement relationship graph"/><aside className="graph-detail">{selected&&<><div className="kicker">SELECTED NODE</div><h3>{nodeLabel(selected)}</h3><span className="graph-type">{selected.node_type || 'Bid'}</span><dl><dt>GID</dt><dd>{selected.gid || selected.bid_gid || 'Unavailable'}</dd><dt>Source</dt><dd>{selected.source_table || 'Unavailable'} / {selected.source_pk || 'Unavailable'}</dd>{selected.observed_participant_key&&<><dt>Observed key</dt><dd>{selected.observed_participant_key}</dd></>}{selected.tender_gid&&<><dt>Tender</dt><dd>{selected.tender_gid}</dd></>}</dl>{selected.node_type==='Bid'&&selected.bid_id!==undefined&&<><button className="detail-link" onClick={()=>onNavigate?.(selected)}>Open bidder investigation <span>→</span></button><button className="detail-link" onClick={()=>onExpand?.(String(selected.bid_id))}>Expand this bidder <span>+</span></button></>}{selected.node_type==='CoBidObservation'&&selected.tender_id!==undefined&&<button className="detail-link" onClick={()=>onNavigate?.(selected)}>Open shared tender <span>→</span></button>}</>}{selectedEdge&&<><div className="kicker">SELECTED RELATIONSHIP</div><h3>{selectedEdge.type}</h3><p className="graph-note">This is a tender-specific observation edge. It is not a direct legal-company relationship and is not evidence of collusion.</p><dl><dt>Source</dt><dd>{selectedEdge.source}</dd><dt>Target</dt><dd>{selectedEdge.target}</dd><dt>Observation</dt><dd>{selectedEdge.observation_gid || 'Unavailable'}</dd><dt>Tender</dt><dd>{selectedEdge.tender_gid || 'Unavailable'}</dd><dt>Evidence</dt><dd>{selectedEdge.evidence?.length || 0} source references</dd></dl>{selectedEdge.tender_id!==undefined&&<button className="detail-link" onClick={()=>onNavigate?.(nodesByID.get(selectedEdge.target) || { tender_id: selectedEdge.tender_id, tender_gid: selectedEdge.tender_gid, node_type: 'CoBidObservation' })}>Open tender evidence <span>→</span></button>}{selectedEdge.evidence?.map(e=><div className="evidence-ref" key={`${e.source_table}-${e.source_pk}`}><b>{e.source_table}</b><span>#{e.source_pk}</span><small>{e.gid}</small></div>)}</>}{!selected&&!selectedEdge&&<div className="graph-empty"><b>Select a node or relationship</b><span>Inspect identifiers, provenance and the source tender observation here.</span></div>}</aside></div></div>;
}
