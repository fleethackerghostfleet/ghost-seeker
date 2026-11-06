package main

import (
	"flag"
	"io"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/bench"
)

func TestAddAccumulatesReportedMetrics(t *testing.T) {
	r := bench.Result{UnknownPersistent: 2, UnknownKept: 3, UnknownP12: 4,
		UnknownApproaching: 5, UnknownP1: 6, UnknownTransient: 7, UnknownP4: 8,
		EdgeDrop: .125, EdgeMissP1: .25, EdgeMissP12: .375, TierExact: .5,
		TierWithin1: .625, P1Recall: .75, P1Precision: .875, CriticalMiss: 1,
		FalseFlash: 1.125, FlashLoad: 1.25, NDCG: 1.375, Burden: 1.5}
	got := bench.Result{Model: "maritime"}
	add(&got, r)
	add(&got, r)
	want := bench.Result{Model: "maritime", UnknownPersistent: 4, UnknownKept: 6, UnknownP12: 8,
		UnknownApproaching: 10, UnknownP1: 12, UnknownTransient: 14, UnknownP4: 16,
		EdgeDrop: .25, EdgeMissP1: .5, EdgeMissP12: .75, TierExact: 1,
		TierWithin1: 1.25, P1Recall: 1.5, P1Precision: 1.75, CriticalMiss: 2,
		FalseFlash: 2.25, FlashLoad: 2.5, NDCG: 2.75, Burden: 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sum=%+v, want %+v", got, want)
	}
	add(&got, bench.Result{})
	if !reflect.DeepEqual(got, want) || r.Model != "" || r.UnknownPersistent != 2 {
		t.Fatal("zero result changed sum or source result mutated")
	}
}

func TestPercentageAndShareFormatting(t *testing.T) {
	for _, tc := range []struct {
		n, total int
		want     string
	}{{0, 0, "—"}, {2, 0, "—"}, {0, 4, "0.0%"}, {1, 3, "33.3%"}, {3, 3, "100.0%"}} {
		if got := share(tc.n, tc.total); got != tc.want {
			t.Errorf("share(%d,%d)=%q, want %q", tc.n, tc.total, got, tc.want)
		}
	}
	if got := pct(.12345); got != "12.3%" {
		t.Fatalf("pct=%q", got)
	}
}

func runBench(t *testing.T, args ...string) string {
	t.Helper()
	oldFlags, oldArgs, oldStdout := flag.CommandLine, os.Args, os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		flag.CommandLine, os.Args, os.Stdout = oldFlags, oldArgs, oldStdout
		r.Close()
		w.Close()
	}()
	flag.CommandLine = flag.NewFlagSet("bench", flag.ContinueOnError)
	os.Args, os.Stdout = append([]string{"bench"}, args...), w
	main()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestBenchRejectsMalformedSeed(t *testing.T) {
	defer func() {
		if _, ok := recover().(*strconv.NumError); !ok {
			t.Fatal("expected seed parse error")
		}
	}()
	runBench(t, "-seeds=not-a-number", "-n=1")
}
