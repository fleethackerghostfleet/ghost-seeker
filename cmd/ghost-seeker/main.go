package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/pivauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"
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
	reviewPath := flag.String("review-log", "data/reviews.jsonl", "append-only review log (empty disables)")
	shoreURL := flag.String("shore", "", "live mode: back the UI with this shore console (e.g. http://localhost:8080) instead of the internal simulation")
	shoreCA := flag.String("shore-ca", os.Getenv("GS_SHORE_CA"), "live mode: optional PEM CA bundle added to system roots for HTTPS shore/edge consoles")
	operatorID := flag.String("operator-id", envOr("GS_OPERATOR_ID", shorebridge.DefaultOperator), "live mode: identity recorded as the issuer of policies edited from this console (never taken from the browser)")
	edgeURL := flag.String("edge", "http://localhost:8081", "live mode: edge console, polled for the ship-side item count (optional)")
	linkListen := flag.String("link-listen", ":18080", "live mode: automatic 0–2 MB/s ship-to-shore link; point the edge's -shore at it (empty = externally shaped link)")
	linkBps := flag.Int64("link-bps", -1, linkBpsUsage)
	poll := flag.Duration("poll", time.Second, "live mode: shore poll interval")
	llmURL := flag.String("llm-url", os.Getenv("GS_LLM_URL"), "OpenAI-compatible local LLM base URL")
	researchCompilerURL := flag.String("research-compiler-url", os.Getenv("GS_RESEARCH_COMPILER_URL"), "opt-in local research language compiler origin (e.g. http://localhost:8097)")
	llmModel := flag.String("llm-model", os.Getenv("GS_LLM_MODEL"), "local LLM model name")
	llmTimeout := flag.Duration("llm-timeout", llmTimeoutDefault, "local LLM request timeout")
	opToken := flag.String("operator-token", os.Getenv("GS_OPERATOR_TOKEN"), "operator bearer token shared with shore/edge; the browser exchanges it for a session cookie at /login (prefer env or file)")
	opTokenFile := flag.String("operator-token-file", os.Getenv("GS_OPERATOR_TOKEN_FILE"), "operator token file")
	authMode := flag.String("auth-mode", envOr("GS_AUTH_MODE", "token"), "web authentication: token or piv")
	reviewLinkKeyFile := flag.String("review-link-key-file", os.Getenv("GS_REVIEW_LINK_KEY_FILE"), "enable review-only bearer link using this private key file (empty = disabled)")
	tlsCert := flag.String("tls-cert", os.Getenv("GS_TLS_CERT"), "HTTPS server certificate")
	tlsKey := flag.String("tls-key", os.Getenv("GS_TLS_KEY"), "HTTPS server private key")
	ca := flag.String("piv-ca-bundle", os.Getenv("GS_PIV_CA_BUNDLE"), "local PIV CA bundle")
	crls := flag.String("piv-crl-dir", os.Getenv("GS_PIV_CRL_DIR"), "local PIV CRL directory")
	grace := flag.Duration("piv-crl-grace", envDuration("GS_PIV_CRL_GRACE", 0), "CRL grace period")
	reload := flag.Duration("piv-reload", envDuration("GS_PIV_RELOAD", time.Minute), "PIV reload interval")
	ocsp := flag.String("piv-ocsp-url", os.Getenv("GS_PIV_OCSP_URL"), "optional on-prem OCSP responder")
	oids := flag.String("piv-policy-oids", os.Getenv("GS_PIV_POLICY_OIDS"), "comma-separated policy OIDs")
	users := flag.String("piv-users", os.Getenv("GS_PIV_USERS"), "PIV user role mapping")
	demo := registerDemoFlags()
	flag.Parse()
	if *shoreURL == "" {
		if err := simulationAvailable(); err != nil {
			log.Fatal(err)
		}
	}
	if err := demo.validate(*authMode, *shoreURL); err != nil {
		log.Fatal(err)
	}
	if *linkBps < -1 {
		log.Fatal("-link-bps must be >= -1")
	}
	if err := validateReviewLinkAuthMode(*authMode, *reviewLinkKeyFile, *shoreURL); err != nil {
		log.Fatal(err)
	}
	reviewLogSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "review-log" {
			reviewLogSet = true
		}
	})
	if err := validateAuthMode(*authMode, *tlsCert, *tlsKey, *ca, *crls, *users); err != nil {
		log.Fatal(err)
	}
	var transport *tls.Config

	researchCompiler, err := command.NewResearchClient(*researchCompilerURL)
	if err != nil {
		log.Fatal(err)
	}
	compiler := &command.Compiler{LLM: command.NewLLMClient(*llmURL, *llmModel, *llmTimeout), Research: researchCompiler}
	if researchCompiler != nil {
		log.Printf("NL compiler: research interpreter at %s (existing demo classes; failures block Apply)", *researchCompilerURL)
	} else if *llmURL == "" {
		log.Printf("NL compiler: rules parser (set GS_LLM_URL to enable LLM)")
	} else {
		log.Printf("NL compiler: local LLM at %s", *llmURL)
	}

	operator, devMode, err := opauth.Load(*opToken, *opTokenFile, demo.tokenFile())
	if err != nil {
		log.Fatal(err)
	}
	sessions := demo.configureSessions(opauth.NewSessions(operator, devMode))
	if *reviewLinkKeyFile != "" {
		key, err := opauth.LoadOrCreateReviewLinkKey(*reviewLinkKeyFile)
		if err != nil {
			log.Fatalf("review link key: %v", err)
		}
		sessions.WithReviewJoinKey(key)
		log.Printf("review link enabled; key file: %s", *reviewLinkKeyFile)
	}
	if *authMode == "piv" {
		var policies []string
		for _, oid := range strings.Split(*oids, ",") {
			if oid = strings.TrimSpace(oid); oid != "" {
				policies = append(policies, oid)
			}
		}
		auth, err := pivauth.New(pivauth.Config{CABundle: *ca, CRLDir: *crls, CRLGrace: *grace, ReloadInterval: *reload, OCSPURL: *ocsp, PolicyOIDs: policies, UsersFile: *users})
		if err != nil {
			log.Fatalf("PIV authentication: %v", err)
		}
		transport, err = auth.TLSConfig(*tlsCert, *tlsKey)
		if err != nil {
			log.Fatalf("PIV HTTPS: %v", err)
		}
		auth.Start(context.Background())
		sessions.WithCerts(auth, nil)
		log.Print("PIV mode: dev loopback auto-login disabled; bridge bearer token unchanged")
	}
	demo.logDevMode(devMode, *authMode)

	if *shoreURL != "" {
		upstream, err := httpx.ClientTransport(*shoreCA)
		if err != nil {
			log.Fatal(err)
		}
		runShoreMode(*addr, *shoreURL, *edgeURL, *linkListen, *poll, compiler, operator, sessions, *operatorID, transport, *reviewPath, demo.linkControl(), *linkBps, upstream)
		return
	}

	runSimulationMode(*addr, demo, *linkBps, *reviewPath, reviewLogSet, compiler, operator, sessions, transport)
}

func serve(addr string, handler http.Handler, transport ...*tls.Config) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if len(transport) > 0 && transport[0] != nil {
		srv.TLSConfig = transport[0]
		return srv.ListenAndServeTLS("", "")
	}
	return srv.ListenAndServe()
}

// runShoreMode serves the operator UI on top of a live shore node: the
// bridge polls the shore ConsoleService and the shaper is the ship-to-shore
// link the edge talks through, with an automatically varying normal rate.
func runShoreMode(addr, shoreURL, edgeURL, linkListen string, poll time.Duration, compiler *command.Compiler, operator string, sessions *opauth.Sessions, operatorID string, transport *tls.Config, reviewPath string, demoLinkControl bool, linkBps int64, upstream http.RoundTripper) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var link shorebridge.LinkControl
	if linkListen != "" {
		target, err := shaperTarget(shoreURL)
		if err != nil {
			log.Fatalf("-shore: %v", err)
		}
		sh := liveLinkShaper(target, linkBps)
		go func() {
			if err := sh.ListenAndServe(ctx, linkListen); err != nil {
				log.Fatalf("shaper: %v", err)
			}
		}()
		if linkBps >= 0 {
			log.Printf("link shaper on %s -> %s: fixed %d bits/s", linkListen, target, linkBps)
		} else {
			scheme := "http"
			if strings.HasPrefix(shoreURL, "https://") {
				scheme = "https"
			}
			log.Printf("link shaper on %s -> %s: automatic normal distribution, 0–2 MB/s, mean 0.5 MB/s, stddev 0.30 MB/s, ~5%% outages, every 5s (start the edge with -shore %s://localhost%s)", linkListen, target, scheme, linkListen)
		}
		link = sh
	}
	bridge := enableDemoLinkControl(shorebridge.New(shoreURL, edgeURL, link).WithToken(operator).WithOperator(operatorID).WithTransport(upstream), demoLinkControl)
	compiler.Locks = bridge.PolicyLocks()
	compiler.EdgeSubtypes = true
	compiler.Capabilities = bridge.InferenceCapabilities()
	bridge.Compiler = compiler
	console := shorebridge.NewConsole(bridge)
	if reviewPath != "" {
		l, err := reviews.Open(reviewPath)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		console.SetReviewLog(l)
	}
	go bridge.Run(ctx, poll)

	// One site: Patrick's operator console (index.html / cdr.html) on live
	// shore items. The console speaks the simulation's API (/api/config,
	// /api/stream, /api/items, /api/directives); shore-only actions (pulls,
	// overrides) and link status stay under /api/shore/ and blobs are proxied.
	mux := http.NewServeMux()
	mux.Handle("/api/shore/", bridge.Handler())
	mux.Handle("/api/v1/", bridge.Handler())
	mux.Handle("/blob/", bridge.Handler())
	mux.Handle("/api/", console.Handler())
	mux.Handle("GET /cdr", http.RedirectHandler("/cdr.html", http.StatusFound))
	mux.Handle("GET /shore.html", http.RedirectHandler("/", http.StatusFound)) // retired F2 UI; same console at /
	mux.Handle("/", web.Handler())
	log.Printf("Ghost Seeker (live shore mode, shore=%s) listening on %s", shoreURL, addr)
	log.Fatal(serve(addr, httpx.Secure(httpx.SameOrigin(sessions.Wrap(mux))), transport))
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

func liveLinkShaper(target string, rate int64) *shaper.Shaper {
	if rate >= 0 {
		return shaper.New(target, rate)
	}
	return shaper.NewNormal(target)
}
