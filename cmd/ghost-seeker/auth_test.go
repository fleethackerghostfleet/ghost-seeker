package main

import (
	"strings"
	"testing"
)

func TestValidateAuthMode(t *testing.T) {
	fields := []string{"tls-cert", "tls-key", "piv-ca-bundle", "piv-crl-dir", "piv-users"}
	for i, name := range fields {
		t.Run(name, func(t *testing.T) {
			values := []string{"cert", "key", "ca", "crls", "users"}
			values[i] = ""
			err := validateAuthMode("piv", values[0], values[1], values[2], values[3], values[4])
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing %s: %v", name, err)
			}
		})
	}
	if err := validateAuthMode("token", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMode("piv", "cert", "key", "ca", "crls", "users"); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMode("invalid", "", "", "", "", ""); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestValidateReviewLinkAuthMode(t *testing.T) {
	if err := validateReviewLinkAuthMode("token", "review-link.key", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateReviewLinkAuthMode("token", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateReviewLinkAuthMode("piv", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateReviewLinkAuthMode("piv", "review-link.key", ""); err == nil || !strings.Contains(err.Error(), "incompatible with -auth-mode piv") {
		t.Fatalf("PIV + review link error = %v", err)
	}
	if err := validateReviewLinkAuthMode("token", "review-link.key", "http://shore:8080"); err == nil || !strings.Contains(err.Error(), "supported only in simulation/dataset mode, not with -shore") {
		t.Fatalf("shore + review link error = %v", err)
	}
	if err := validateReviewLinkAuthMode("token", "", "http://shore:8080"); err != nil {
		t.Fatalf("shore without review link = %v", err)
	}
}
