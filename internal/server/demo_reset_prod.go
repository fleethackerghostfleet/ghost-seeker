//go:build prod

package server

import "net/http"

// Production builds have no demo reset: no option, no route.
func (s *Server) registerDemoRoutes() {}

func (s *Server) handleDemoRoute(http.ResponseWriter, *http.Request) bool { return false }

func demoDraftFeatures(features map[string]any) map[string]any { return features }
