package unitauth

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestEdgeAndShoreTemporalValidation(t *testing.T) {
	i, err := New(true, testKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		name     string
		iat, nbf *jwt.NumericDate
		valid    bool
	}{
		{"missing optional dates", nil, nil, true},
		{"past issued at", jwt.NewNumericDate(now.Add(-time.Hour)), nil, true},
		{"future issued at", jwt.NewNumericDate(now.Add(time.Hour)), nil, false},
		{"future not before", nil, jwt.NewNumericDate(now.Add(time.Hour)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := claims{Role: "unit", RegisteredClaims: jwt.RegisteredClaims{Subject: "edge-1", ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Hour)), IssuedAt: tc.iat, NotBefore: tc.nbf}}
			value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testKey))
			if err != nil {
				t.Fatal(err)
			}
			header := make(http.Header)
			header.Set("Authorization", "Bearer "+value)
			node, err := i.authenticate(header)
			if (err == nil) != tc.valid || (tc.valid && node != "edge-1") {
				t.Fatalf("shore: node=%q err=%v", node, err)
			}
			if got := ConfiguredTokenActive(value); got != tc.valid {
				t.Fatalf("edge active=%v want=%v", got, tc.valid)
			}
		})
	}
}

func TestEdgeAndShoreRejectNoneAlgorithm(t *testing.T) {
	c := claims{Role: "unit", RegisteredClaims: jwt.RegisteredClaims{Subject: "edge-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	value, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	i, err := New(true, testKey)
	if err != nil {
		t.Fatal(err)
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+value)
	if _, err := i.authenticate(header); err == nil {
		t.Fatal("shore accepted unsigned token")
	}
	if ConfiguredTokenActive(value) {
		t.Fatal("edge accepted unsigned token")
	}
}
