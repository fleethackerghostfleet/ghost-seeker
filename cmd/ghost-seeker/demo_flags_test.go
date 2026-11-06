//go:build !prod

package main

import (
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
)

func demoFlagsForTest(t *testing.T) *demoFlags {
	t.Helper()
	old := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet(t.Name(), flag.ContinueOnError)
	t.Cleanup(func() { flag.CommandLine = old })
	return registerDemoFlags()
}

func TestDemoDefaultsAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, admin, peers, token string
		wantAdmin, wantPeers      bool
	}{
		{name: "defaults"},
		{name: "enabled", admin: "TRUE", peers: "1", token: "demo.token", wantAdmin: true, wantPeers: true},
		{name: "numeric admin", admin: "1", peers: "true", token: "none", wantAdmin: true, wantPeers: true},
		{name: "disabled", admin: "false", peers: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GS_ADMIN", tc.admin)
			t.Setenv("GS_DEV_TRUST_PEERS", tc.peers)
			t.Setenv("GS_DEV_TOKEN_FILE", tc.token)
			d := demoFlagsForTest(t)
			if *d.admin != tc.wantAdmin || *d.devTrustPeers != tc.wantPeers {
				t.Fatal("incorrect inherited demo permissions")
			}
			wantToken := tc.token
			if wantToken == "" {
				wantToken = opauth.DefaultDevFile
			}
			if d.tokenFile() != wantToken || d.linkControl() || *d.demoAcceptAnyToken || *d.captureEvery != 700*time.Millisecond || *d.simLinkBps != -1 {
				t.Fatal("incorrect demo defaults")
			}
			if err := flag.CommandLine.Set("demo-link-control", "true"); err != nil {
				t.Fatal(err)
			}
			if !d.linkControl() {
				t.Fatal("explicit demo link control was ignored")
			}
			if err := simulationAvailable(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDemoFlagValidation(t *testing.T) {
	t.Setenv("GS_ADMIN", "")
	t.Setenv("GS_DEV_TRUST_PEERS", "")
	t.Setenv("GS_DEV_TOKEN_FILE", "none")
	for _, tc := range []struct {
		name, mode, shore, want string
		flags                   []string
	}{
		{name: "defaults", mode: "token"},
		{name: "PIV without insecure bypass", mode: "piv"},
		{name: "outage", mode: "token", flags: []string{"-sim-link-bps=0"}},
		{name: "one nanosecond capture", mode: "token", flags: []string{"-capture-every=1ns"}},
		{name: "zero negatives", mode: "token", flags: []string{"-negatives-rate=0"}},
		{name: "all negatives", mode: "token", flags: []string{"-negatives-rate=1"}},
		{name: "token bypass", mode: "token", flags: []string{"-demo-accept-any-token"}},
		{name: "dataset", mode: "token", flags: []string{"-dataset=manifest.jsonl"}},
		{name: "invalid bandwidth", mode: "token", flags: []string{"-sim-link-bps=-2"}, want: "-sim-link-bps"},
		{name: "zero cadence", mode: "token", flags: []string{"-capture-every=0"}, want: "-capture-every"},
		{name: "negative cadence", mode: "token", flags: []string{"-capture-every=-1s"}, want: "-capture-every"},
		{name: "PIV bypass", mode: "piv", flags: []string{"-demo-accept-any-token"}, want: "incompatible with PIV"},
		{name: "negative sampling", mode: "token", flags: []string{"-negatives-rate=-0.1"}, want: "-negatives-rate"},
		{name: "excess sampling", mode: "token", flags: []string{"-negatives-rate=1.1"}, want: "-negatives-rate"},
		{name: "NaN sampling", mode: "token", flags: []string{"-negatives-rate=NaN"}, want: "-negatives-rate"},
		{name: "infinite sampling", mode: "token", flags: []string{"-negatives-rate=+Inf"}, want: "-negatives-rate"},
		{name: "conflicting live and dataset", mode: "token", shore: "https://shore.example", flags: []string{"-dataset=manifest.jsonl"}, want: "-dataset cannot be used with -shore"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := demoFlagsForTest(t)
			if err := flag.CommandLine.Parse(tc.flags); err != nil {
				t.Fatal(err)
			}
			err := d.validate(tc.mode, tc.shore)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation=%v, want %q", err, tc.want)
			}
		})
	}
}
