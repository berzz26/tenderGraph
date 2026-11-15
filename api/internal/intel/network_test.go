package intel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNetworkPreservesStringNodeIdentityAndObservationEvidence(t *testing.T) {
	n := Network{RootBidID: 9007199254740991, Nodes: []NetworkNode{{BidID: 9007199254740991, NodeID: "9007199254740991", GID: "BID:v:9007199254740991", NodeType: "Bid", Name: "Participant record #9007199254740991"}, {NodeID: "COBID:v:7:12:9007199254740991", GID: "COBID:v:7:12:9007199254740991", NodeType: "CoBidObservation"}}, Edges: []NetworkEdge{{EdgeID: "edge-1", Source: "9007199254740991", Target: "COBID:v:7:12:9007199254740991", Type: "CO_BID_IN", ObservationGID: "COBID:v:7:12:9007199254740991", Evidence: []Evidence{{SourceTable: "tender_participants", SourcePK: "9007199254740991", GID: "BID:v:9007199254740991"}}}}}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"node_id":"9007199254740991"`) {
		t.Fatalf("string node identity was not serialized exactly: %s", s)
	}
	if !strings.Contains(s, `"source_pk":"9007199254740991"`) {
		t.Fatalf("source PK was not serialized exactly: %s", s)
	}
	if !strings.Contains(s, `"observation_gid":"COBID:v:7:12:9007199254740991"`) {
		t.Fatalf("observation identity missing: %s", s)
	}
}
