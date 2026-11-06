//go:build prod

package main

import (
	"crypto/tls"
	"errors"
	"log"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/shorebridge"
)

// Production builds have no simulation, dataset replay, demo link control or
// demo auth shortcuts: the console only runs in live mode against shore.

var errLiveModeRequired = errors.New("ghost-seeker: production build (-tags prod): the simulation is compiled out; run in live mode with -shore <shore console URL> and an operator token (GS_OPERATOR_TOKEN / -operator-token-file) or -auth-mode piv")

type demoFlags struct{}

func registerDemoFlags() *demoFlags { return &demoFlags{} }

func simulationAvailable() error { return errLiveModeRequired }

func (*demoFlags) validate(string, string) error { return nil }

func (*demoFlags) tokenFile() string { return "none" }

func (*demoFlags) linkControl() bool { return false }

func (*demoFlags) configureSessions(s *opauth.Sessions) *opauth.Sessions { return s }

func (*demoFlags) logDevMode(bool, string) {}

func runSimulationMode(string, *demoFlags, int64, string, bool, *command.Compiler, string, *opauth.Sessions, *tls.Config) {
	log.Fatal(errLiveModeRequired)
}

func enableDemoLinkControl(b *shorebridge.Bridge, _ bool) *shorebridge.Bridge { return b }

const linkBpsUsage = "fixed live link shaper rate in bits/s (-1 = automatic)"
