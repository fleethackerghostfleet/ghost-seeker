package main

import (
	"context"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
	"github.com/fleethackerghostfleet/ghost-seeker/web"
)

func main() {
	llmTimeoutDefault := 8 * time.Second
	if value := os.Getenv("GS_LLM_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			log.Printf("invalid GS_LLM_TIMEOUT %q; using 8s", value)
		} else {
			llmTimeoutDefault = timeout
		}
	}
	addr := flag.String("addr", ":8080", "HTTP listen address")
	retain := flag.Int("retain", 260, "maximum stored items")
	backfill := flag.Int("backfill", 84, "number of historical items to generate at startup")
	autoCDR := flag.Duration("auto-cdr", 15*time.Second, "average interval at which a simulated watch officer escalates the top P1/P2 image to the commander (0 = off)")
	shoreURL := flag.String("shore", "", "live mode: back the UI with this shore console (e.g. http://localhost:8080) instead of the internal simulation")
	operatorID := flag.String("operator-id", envOr("GS_OPERATOR_ID", shorebridge.DefaultOperator), "live mode: identity recorded as the issuer of policies edited from this console (never taken from the browser)")
	edgeURL := flag.String("edge", "http://localhost:8081", "live mode: edge console, polled for the ship-side item count (optional)")
	linkListen := flag.String("link-listen", ":18080", "live mode: listen here as the shaped ship-to-shore link; point the edge's -shore at it (empty = no shaper, bandwidth buttons disabled)")
	linkBps := flag.Int64("link-bps", 2_000_000, "live mode: initial link rate in bits/s")
	poll := flag.Duration("poll", time.Second, "live mode: shore poll interval")
	adminDefault := strings.EqualFold(os.Getenv("GS_ADMIN"), "true") || os.Getenv("GS_ADMIN") == "1"
	admin := flag.Bool("admin", adminDefault, "enable super-admin policy controls")
	policyState := flag.String("policy-state", "", "persist simulation policy state at this path")
	llmURL := flag.String("llm-url", os.Getenv("GS_LLM_URL"), "OpenAI-compatible local LLM base URL")
	llmModel := flag.String("llm-model", os.Getenv("GS_LLM_MODEL"), "local LLM model name")
	llmTimeout := flag.Duration("llm-timeout", llmTimeoutDefault, "local LLM request timeout")
	opToken := flag.String("operator-token", os.Getenv("GS_OPERATOR_TOKEN"), "operator bearer token shared with shore/edge; the browser exchanges it for a session cookie at /login (prefer env or file)")
	opTokenFile := flag.String("operator-token-file", os.Getenv("GS_OPERATOR_TOKEN_FILE"), "operator token file")
	devTokenFile := flag.String("dev-token-file", envOr("GS_DEV_TOKEN_FILE", opauth.DefaultDevFile), "local demo only: auto-generate the operator token here when none is set and log loopback browsers in automatically ('none' = require an explicit token)")
	devTrustPeers := flag.Bool("dev-trust-peers", envBool("GS_DEV_TRUST_PEERS"), "local demo in containers only: with -dev-token-file, also sign in browsers that address localhost from a non-loopback peer (a port published on the host's 127.0.0.1 arrives from the bridge gateway); publish the port on 127.0.0.1 only")
	flag.Parse()

	compiler := &command.Compiler{LLM: command.NewLLMClient(*llmURL, *llmModel, *llmTimeout)}
	if *llmURL == "" {
		log.Printf("NL compiler: rules parser (set GS_LLM_URL to enable LLM)")
	} else {
		log.Printf("NL compiler: local LLM at %s", *llmURL)
	}

	operator, devMode, err := opauth.Load(*opToken, *opTokenFile, *devTokenFile)
	if err != nil {
		log.Fatal(err)
	}
	sessions := opauth.NewSessions(operator, devMode).TrustPeers(*devTrustPeers)
	if devMode {
		peers := "loopback browsers"
		if *devTrustPeers {
			peers = "browsers addressing localhost from any peer (-dev-trust-peers)"
		}
		log.Printf("operator token auto-generated for a local demo (%s); %s are signed in automatically", *devTokenFile, peers)
	}

	if *shoreURL != "" {
		runShoreMode(*addr, *shoreURL, *edgeURL, *linkListen, *linkBps, *poll, compiler, operator, sessions, *operatorID)
		return
	}

	data := store.New(*retain)
	generator := sensor.NewGenerator()
	fillBacklog(data, generator, *backfill)
	options := []server.Option{server.WithAdmin(*admin), server.WithAdminToken(operator), server.WithNodeID("DDG-51")}
	if *policyState != "" {
		backend, err := policy.NewFileBackend(*policyState)
		if err != nil {
			log.Fatal(err)
		}
		options = append(options, server.WithPolicyBackend(backend))
	}
	app := server.New(data, options...)
	compiler.Locks = app.PolicyLocks()
	app.SetCompiler(compiler)
	go runSimulation(data, generator, app)
	go refreshTimeScopes(app)
	if *autoCDR > 0 {
		for i := 0; i < 4; i++ {
			data.AutoEscalate()
		}
		go runWatchOfficer(data, app, *autoCDR)
	}

	log.Printf("Ghost Seeker listening on %s", *addr)
	log.Fatal(serve(*addr, httpx.Secure(httpx.SameOrigin(sessions.Wrap(app)))))
}

func serve(addr string, handler http.Handler) error {
	return (&http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}).ListenAndServe()
}

func refreshTimeScopes(app *server.Server) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		app.RescoreTimeScoped(now)
	}
}

func fillBacklog(data *store.Store, generator *sensor.Generator, count int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ts := time.Now().Add(-45 * time.Minute)
	for kept := 0; kept < count; {
		ts = ts.Add(time.Duration(rng.Intn(14)+8) * time.Second)
		source := catalog.Sources[rng.Intn(len(catalog.Sources))]
		item, extra := generator.Tick(&source, ts, "maritime", nil)
		if item != nil {
			data.Add(item, extra)
			kept++
		} else {
			data.Add(nil, extra)
		}
	}
}

func runWatchOfficer(data *store.Store, app *server.Server, every time.Duration) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		time.Sleep(every/2 + time.Duration(rng.Int63n(int64(every))))
		if item, cdr, ok := data.AutoEscalate(); ok {
			app.PublishUpdate(item, cdr)
		}
	}
}

// runShoreMode serves the operator UI on top of a live shore node: the
// bridge polls the shore ConsoleService and the shaper is the ship-to-shore
// link the edge talks through (so the bandwidth buttons are real).
func runShoreMode(addr, shoreURL, edgeURL, linkListen string, linkBps int64, poll time.Duration, compiler *command.Compiler, operator string, sessions *opauth.Sessions, operatorID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var link shorebridge.LinkControl
	if linkListen != "" {
		target, err := shaperTarget(shoreURL)
		if err != nil {
			log.Fatalf("-shore: %v", err)
		}
		sh := shaper.New(target, linkBps)
		go func() {
			if err := sh.ListenAndServe(ctx, linkListen); err != nil {
				log.Fatalf("shaper: %v", err)
			}
		}()
		log.Printf("link shaper on %s -> %s at %d bit/s (start the edge with -shore http://localhost%s)", linkListen, target, linkBps, linkListen)
		link = sh
	}
	bridge := shorebridge.New(shoreURL, edgeURL, link).WithToken(operator).WithOperator(operatorID)
	compiler.Locks = bridge.PolicyLocks()
	bridge.Compiler = compiler
	console := shorebridge.NewConsole(bridge)
	go bridge.Run(ctx, poll)

	// One site: Patrick's operator console (index.html / cdr.html) on live
	// shore items. The console speaks the simulation's API (/api/config,
	// /api/stream, /api/items, /api/directives); the shore-only actions (link
	// presets, pulls, overrides) stay under /api/shore/ and blobs are proxied.
	mux := http.NewServeMux()
	mux.Handle("/api/shore/", bridge.Handler())
	mux.Handle("/blob/", bridge.Handler())
	mux.Handle("/api/", console.Handler())
	mux.Handle("GET /cdr", http.RedirectHandler("/cdr.html", http.StatusFound))
	mux.Handle("GET /shore.html", http.RedirectHandler("/", http.StatusFound)) // retired F2 UI; same console at /
	mux.Handle("/", web.Handler())
	log.Printf("Ghost Seeker (live shore mode, shore=%s) listening on %s", shoreURL, addr)
	log.Fatal(serve(addr, httpx.Secure(httpx.SameOrigin(sessions.Wrap(mux)))))
}

func envBool(k string) bool {
	v, _ := strconv.ParseBool(os.Getenv(k))
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func shaperTarget(shoreURL string) (string, error) {
	u, err := url.Parse(shoreURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	return host, nil
}

func runSimulation(data *store.Store, generator *sensor.Generator, app *server.Server) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		time.Sleep(time.Duration(1200+rng.Intn(1201)) * time.Millisecond)
		_, _, _, state := data.Snapshot()
		item, extra := generator.Tick(nil, time.Now(), state.Model, data.Directives())
		stored, evicted := data.Add(item, extra)
		app.PublishEvict(evicted)
		if item != nil {
			stats, _ := data.Stats()
			app.PublishItem(stored, stats)
		} else {
			stats, _ := data.Stats()
			app.PublishStats(stats)
		}
	}
}
