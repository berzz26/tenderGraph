package intel

import "testing"

func TestSourceIDFromGID(t *testing.T) {
	if got := sourceIDFromGID("BID:v:3223"); got != 3223 {
		t.Fatalf("sourceIDFromGID=%d", got)
	}
	if got := sourceIDFromGID("ENTITY:v:3223"); got != 0 {
		t.Fatalf("non-Bid GID decoded as %d", got)
	}
}

func TestNativePathDepthBounds(t *testing.T) {
	if _, err := (Service{}).NativeShortestPaths(nil, 1, 2, 0, 1); err == nil {
		t.Fatal("expected invalid depth")
	}
	if _, err := (Service{}).NativeConnections(nil, 1, 5, 1); err == nil {
		t.Fatal("expected invalid depth")
	}
}
