//go:build prod

package main

import "testing"

func TestProdContractShoreNoBuiltInEmbedder(t *testing.T) {
	if _, err := newEmbedder("mock"); err == nil {
		t.Fatal("prod shore accepted -infer mock")
	}
	if emb, err := newEmbedder(""); err != nil || emb != nil {
		t.Fatalf("empty -infer must disable embedding: %v %v", emb, err)
	}
}
