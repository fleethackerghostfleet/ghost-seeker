// Package shorebridge backs the operator web app with a live shore node:
// it polls the shore ConsoleService (ListItems/GetStatus/GetPolicy), groups
// items into objects by features.tags.sighting_id, and publishes snapshots
// over SSE. Operator actions are forwarded to shore (SetPolicy, RequestPull)
// while link status comes from the in-process shaper.
package shorebridge

import (
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shaper"
	"net/http"
	"strings"
	"sync"
	"time"
)

const consolePath = "/ghostseeker.v1.ConsoleService/"

// LinkControl exposes the current link rate and its automatic distribution.
// *shaper.Shaper satisfies it; nil means the link is controlled externally.
type LinkControl interface {
	Bps() int64
	Bytes() int64
	Distribution() *shaper.NormalDistribution
}

// LinkSetter is the optional demo override of the link rate.
type LinkSetter interface {
	SetFixed(bps int64)
	SetAuto()
	Fixed() bool
}

type Bridge struct {
	// linkOverride registers POST /api/shore/link (demo only; the link is automatic otherwise).
	linkOverride bool
	token        string
	shore, edge  string
	link         LinkControl
	client       *http.Client
	// Compiler, when set, turns a free-text directive into rules (local LLM
	// with the rules parser as fallback) and gives the operator a readback;
	// nil keeps the simple Interpret path.
	Compiler *command.Compiler

	mu         sync.Mutex
	snap       []byte
	lastGood   *Snapshot
	lastPolicy *pb.Policy
	subs       map[chan []byte]struct{}
	records    map[string]contactReport // RECORD item id -> its contact report (immutable, fetched once)
	operator   string
	edgeNodeID string

	poke       chan struct{}               // asks Run for an immediate refresh (after an operator action)
	nextExpiry time.Time                   // earliest timed rule/EMCON/policy deadline seen by the poller
	onSnapshot func(*Snapshot, *pb.Policy) // Console hook, called with every fresh snapshot and the policy it was built from
}

func New(shoreURL, edgeURL string, link LinkControl) *Bridge {
	return &Bridge{
		shore:    strings.TrimRight(shoreURL, "/"),
		edge:     strings.TrimRight(edgeURL, "/"),
		link:     link,
		client:   &http.Client{Timeout: 10 * time.Second},
		subs:     map[chan []byte]struct{}{},
		records:  map[string]contactReport{},
		poke:     make(chan struct{}, 1),
		operator: DefaultOperator,
	}
}

// Poke asks the poller to refresh now instead of at the next tick.
func (b *Bridge) Poke() {
	select {
	case b.poke <- struct{}{}:
	default:
	}
}

func (b *Bridge) issuer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.operator
}

func (b *Bridge) echelon() pb.Echelon {
	return pb.Echelon_ECHELON_SHIP
}

// DefaultOperator is the issuer recorded on policies this console edits when
// no -operator-id is configured.
const DefaultOperator = "operator-ui"

// WithOperator sets the identity recorded as IssuedBy / rule Author / EMCON
// SetBy on policies this console submits. It is server configuration (the
// authenticated console instance), never taken from the browser request.
func (b *Bridge) WithOperator(id string) *Bridge {
	if id = strings.TrimSpace(id); id != "" {
		b.mu.Lock()
		b.operator = id
		b.mu.Unlock()
	}
	return b
}

// WithTransport configures console RPCs and shore proxies before serving.
func (b *Bridge) WithTransport(transport http.RoundTripper) *Bridge {
	b.client.Transport = transport
	return b
}
