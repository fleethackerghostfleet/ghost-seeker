// shore is the receiving node: SyncService for edges, ConsoleService for
// analysts, and the downlink queue that carries operator intent to the ship.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/console"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

func main() {
	data := flag.String("data", env("DATA_DIR", "data/shore"), "data directory")
	listen := flag.String("listen", env("LISTEN", ":8080"), "listen address")
	inferURL := flag.String("infer", env("INFER_URL", "mock"), "inference URL for text embedding, or 'mock'")
	uiDir := flag.String("ui", env("UI_DIR", "ui"), "static UI directory")
	edgeID := flag.String("edge", env("EDGE_ID", ""), "edge id to address downlink to ('' = all)")
	requireDefault, err := strconv.ParseBool(env("GS_REQUIRE_AUTH", "false"))
	fatal(err)
	requireAuth := flag.Bool("require-auth", requireDefault, "require per-unit JWTs on SyncService")
	key := flag.String("signing-key", env("GS_SIGNING_KEY", ""), "HS256 signing key (prefer env or file)")
	keyFile := flag.String("signing-key-file", env("GS_SIGNING_KEY_FILE", ""), "signing key file")
	signingKey := flag.String("policy-signing-key", env("GS_POLICY_SIGNING_KEY", ""), "JSON Ed25519 policy signing key")
	policyKeysPath := flag.String("policy-keys", env("GS_POLICY_KEYS", ""), "JSON public keyring; higher-echelon layers must be signed by one of these keys")
	opToken := flag.String("operator-token", env("GS_OPERATOR_TOKEN", ""), "operator bearer token for ConsoleService and /blob/ (prefer env or file)")
	opTokenFile := flag.String("operator-token-file", env("GS_OPERATOR_TOKEN_FILE", ""), "operator token file")
	devTokenFile := flag.String("dev-token-file", env("GS_DEV_TOKEN_FILE", opauth.DefaultDevFile), "local demo only: auto-generate the operator token here when none is set ('none' = require an explicit token)")
	corsOrigins := flag.String("cors-origins", env("GS_CORS_ORIGINS", ""), "comma-separated browser origins allowed to call the API cross-origin (default: same-origin only)")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	secret, err := unitauth.Credential(*key, *keyFile)
	fatal(err)
	auth, err := unitauth.New(*requireAuth, secret)
	fatal(err)
	operator, devMode, err := opauth.Load(*opToken, *opTokenFile, *devTokenFile)
	fatal(err)
	if devMode {
		log.Warn("operator token auto-generated for a local demo; set GS_OPERATOR_TOKEN in production", "file", *devTokenFile)
	}
	if !*requireAuth {
		log.Warn("SyncService accepts unauthenticated edges; set -require-auth with a signing key in production")
	}

	st, err := store.Open(*data)
	fatal(err)
	defer st.Close()
	clk := clock.Real{}
	syncSvc := ssync.New(st, clk, log)
	var emb inference.Analyzer
	if *inferURL == "mock" {
		emb = inference.NewMock()
	} else {
		emb = inference.NewRemote(*inferURL)
	}
	con := &console.Service{NodeID: "shore", St: st, Clk: clk, Embedder: emb, EdgeID: *edgeID}
	if *signingKey != "" {
		con.Signer, err = policy.LoadSigningKey(*signingKey)
		fatal(err)
	}
	if *policyKeysPath != "" {
		con.Keys, err = policy.LoadKeyring(*policyKeysPath)
		fatal(err)
	}

	mux := http.NewServeMux()
	p, h := ghostseekerv1connect.NewSyncServiceHandler(ssync.Handler{Service: syncSvc}, append(link.HandlerOptions(), connect.WithInterceptors(auth), connect.WithReadMaxBytes(8<<20))...)
	mux.Handle(p, h)
	// Every ConsoleService RPC (present and future) and every blob sits behind
	// the operator bearer token.
	p, h = ghostseekerv1connect.NewConsoleServiceHandler(con, connect.WithReadMaxBytes(1<<20))
	mux.Handle(p, opauth.RequireBearer(operator, h))
	mux.Handle("/blob/", opauth.RequireBearer(operator, con.BlobHandler()))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	httpx.StaticUI(mux, *uiDir, "shore")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := httpx.Server(*listen, h2c.NewHandler(httpx.CORSAllow(httpx.SplitList(*corsOrigins), mux), &http2.Server{}))
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("shore", "listen", *listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
