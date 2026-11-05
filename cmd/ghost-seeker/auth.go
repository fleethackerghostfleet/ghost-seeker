package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

func validateAuthMode(mode, cert, key, ca, crls, users string) error {
	if mode != "token" && mode != "piv" {
		return fmt.Errorf("invalid auth-mode %q: use token or piv", mode)
	}
	if mode == "piv" {
		for _, field := range []struct{ name, value string }{{"tls-cert", cert}, {"tls-key", key}, {"piv-ca-bundle", ca}, {"piv-crl-dir", crls}, {"piv-users", users}} {
			if field.value == "" {
				return fmt.Errorf("PIV mode requires -%s", field.name)
			}
		}
	}
	return nil
}
func validateReviewLinkAuthMode(mode, keyFile string) error {
	if mode == "piv" && keyFile != "" {
		return fmt.Errorf("-review-link-key-file is incompatible with -auth-mode piv")
	}
	return nil
}
func envDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			log.Fatalf("invalid %s: expected duration", key)
		}
		return duration
	}
	return fallback
}
