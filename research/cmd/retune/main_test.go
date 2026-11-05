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

func TestOutputHardlinkRejected(t *testing.T) {
	dir := t.TempDir()
	report, weights := filepath.Join(dir, "report"), filepath.Join(dir, "weights")
	if err := os.WriteFile(report, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(report, weights); err != nil {
		t.Fatal(err)
	}
	if err := safeOutputs([]string{filepath.Join(dir, "input")}, report, weights); err == nil {
		t.Fatal("output hardlink accepted")
	}
	b, _ := os.ReadFile(report)
	if string(b) != "original" {
		t.Fatal("output changed during validation")
	}
}
