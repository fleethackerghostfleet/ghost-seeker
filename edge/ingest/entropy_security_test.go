package ingest

import (
	"bytes"
	"io"
	"testing"
)

func TestULIDEntropyNotPredictableFromEdgeID(t *testing.T) {
	a, b := make([]byte, 32), make([]byte, 32)
	if _, err := io.ReadFull(entropyFor("same-edge"), a); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(entropyFor("same-edge"), b); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("ULID entropy is predictable from the edge id")
	}
}
