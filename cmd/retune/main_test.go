package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeOutputs(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "input")
	os.WriteFile(p, nil, 0600)
	if err := safeOutputs([]string{p}, p, filepath.Join(d, "weights")); err == nil {
		t.Fatal("overwrites input")
	}
	alias := filepath.Join(d, "alias")
	if err := os.Link(p, alias); err != nil {
		t.Fatal(err)
	}
	if err := safeOutputs([]string{p}, alias); err == nil {
		t.Fatal("overwrites hardlink")
	}
	if err := safeOutputs([]string{p}, filepath.Join(d, "report"), filepath.Join(d, "weights")); err != nil {
		t.Fatal(err)
	}
}
