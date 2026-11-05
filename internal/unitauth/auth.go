// Package unitauth authenticates edge units on the shore sync transport.
package unitauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/golang-jwt/jwt/v5"
)

type claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Credential reads a mounted credential when provided, otherwise the flag/env value.
func Credential(value, file string) (string, error) {
	if file == "" {
		return strings.TrimSpace(value), nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read credential: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", errors.New("empty credential file")
	}
	return strings.TrimSpace(string(raw)), nil
}

func validateKey(key string) error {
	if len(key) < 32 {
		return errors.New("JWT signing key must be at least 32 bytes")
	}
	return nil
}

// Mint uses the reference implementation's HS256/sub/exp contract and an explicit unit role.
func Mint(node, key string, ttl time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if strings.TrimSpace(node) == "" || ttl < time.Second {
		return "", errors.New("node id and TTL of at least one second are required")
	}
	c := claims{Role: "unit", RegisteredClaims: jwt.RegisteredClaims{
		Subject: node, ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(key))
}

type Interceptor struct {
	required bool
	key      []byte
}

func New(required bool, key string) (*Interceptor, error) {
	if required {
		if err := validateKey(key); err != nil {
			return nil, err
		}
	}
	return &Interceptor{required: required, key: []byte(key)}, nil
}

func (i *Interceptor) authenticate(h http.Header) (string, error) {
	parts := strings.Fields(h.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("bearer token required"))
	}
	c := &claims{}
	_, err := jwt.ParseWithClaims(parts[1], c, func(*jwt.Token) (any, error) { return i.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || strings.TrimSpace(c.Subject) == "" || c.Role != "unit" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("invalid unit token"))
	}
	return c.Subject, nil
}

func matchNode(node string, msg any) error {
	req, ok := msg.(*pb.ExchangeRequest)
	if !ok || req.EdgeId != node {
		return connect.NewError(connect.CodePermissionDenied, errors.New("edge id does not match token subject"))
	}
	return nil
}

func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.required {
			node, err := i.authenticate(req.Header())
			if err != nil {
				return nil, err
			}
			if err := matchNode(node, req.Any()); err != nil {
				return nil, err
			}
		}
		return next(ctx, req)
	}
}

func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if !i.required {
			return next(ctx, conn)
		}
		node, err := i.authenticate(conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(ctx, &unitStream{StreamingHandlerConn: conn, node: node})
	}
}

type unitStream struct {
	connect.StreamingHandlerConn
	node string
}

func (s *unitStream) Receive(msg any) error {
	if err := s.StreamingHandlerConn.Receive(msg); err != nil {
		return err
	}
	return matchNode(s.node, msg)
}
