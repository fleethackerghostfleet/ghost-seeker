//go:build !prod

package opauth

// DemoAcceptAnyToken permits any non-empty login token for demos only.
// Certificate authentication does not use token login.
func (s *Sessions) DemoAcceptAnyToken(on bool) *Sessions {
	s.demoAcceptAnyToken = on
	return s
}

// TrustPeers extends dev auto-login to browsers that address a loopback host
// from any peer address. For containers only: a port published on the host's
// 127.0.0.1 reaches the process from the bridge gateway, never from loopback,
// so Loopback alone would send every demo browser to /login. Forwarded
// headers still disqualify a request. No-op outside dev mode.
func (s *Sessions) TrustPeers(on bool) *Sessions {
	s.anyPeer = on
	return s
}
