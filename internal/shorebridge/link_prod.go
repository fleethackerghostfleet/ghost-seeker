//go:build prod

package shorebridge

import "net/http"

// Production builds have no presenter link override (POST /api/shore/link).
func (b *Bridge) registerDemoRoutes(*http.ServeMux) {}
