package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	Issuer   = "microservices-app/users"
	Audience = "microservices-app"

	// Service tokens are signed with a different key pair, issuer and audience from user tokens,
	// so a token for one can never verify as the other.
	ServiceIssuer   = "microservices-app/service"
	ServiceAudience = "microservices-app-internal"
)

type Claims struct {
	Subject string
	Roles   []string
	// Service is true only when the token was verified by a service verifier.
	// It is never read from the token itself.
	Service bool
}

func (c Claims) IsAdmin() bool {
	return c.HasRole("admin")
}

func (c Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type jwtCustomClaims struct {
	Roles []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

type Signer struct {
	privateKey ed25519.PrivateKey
	issuer     string
	audience   string
	ttl        time.Duration
}

func NewSigner(privateKey ed25519.PrivateKey, issuer, audience string, ttl time.Duration) *Signer {
	return &Signer{
		privateKey: privateKey,
		issuer:     issuer,
		audience:   audience,
		ttl:        ttl,
	}
}

func (s *Signer) Sign(userID string, roles []string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(s.ttl)
	claims := jwtCustomClaims{
		Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("authn: sign token: %w", err)
	}
	return signed, expiresAt, nil
}

// TokenVerifier is satisfied by *Verifier and Verifiers.
type TokenVerifier interface {
	Verify(tokenStr string) (Claims, error)
}

type Verifier struct {
	publicKey ed25519.PublicKey
	issuer    string
	audience  string
	service   bool
}

// NewServiceSigner signs service-to-service tokens whose subject is the calling service's name.
func NewServiceSigner(privateKey ed25519.PrivateKey, ttl time.Duration) *Signer {
	return NewSigner(privateKey, ServiceIssuer, ServiceAudience, ttl)
}

// NewServiceVerifier verifies tokens from NewServiceSigner and marks their Claims.Service.
func NewServiceVerifier(publicKey ed25519.PublicKey) *Verifier {
	return &Verifier{publicKey: publicKey, issuer: ServiceIssuer, audience: ServiceAudience, service: true}
}

// Verifiers tries each verifier in order and returns the first success.
type Verifiers []*Verifier

func (vs Verifiers) Verify(tokenStr string) (Claims, error) {
	err := errors.New("authn: no verifiers")
	for _, v := range vs {
		claims, e := v.Verify(tokenStr)
		if e == nil {
			return claims, nil
		}
		err = e
	}
	return Claims{}, err
}

func NewVerifier(publicKey ed25519.PublicKey, issuer, audience string) *Verifier {
	return &Verifier{
		publicKey: publicKey,
		issuer:    issuer,
		audience:  audience,
	}
}

func (v *Verifier) Verify(tokenStr string) (Claims, error) {
	var customClaims jwtCustomClaims
	token, err := jwt.ParseWithClaims(tokenStr, &customClaims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return v.publicKey, nil
	}, jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}))

	if err != nil || !token.Valid {
		return Claims{}, fmt.Errorf("authn: invalid token: %w", err)
	}

	return Claims{
		Subject: customClaims.Subject,
		Roles:   customClaims.Roles,
		Service: v.service,
	}, nil
}

func ParsePrivateKeyPEM(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("authn: failed to decode PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authn: parse pkcs8 private key: %w", err)
	}
	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("authn: key is not an Ed25519 private key")
	}
	return edKey, nil
}

func ParsePublicKeyPEM(pemBytes []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("authn: failed to decode PEM block")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authn: parse pkix public key: %w", err)
	}
	edKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("authn: key is not an Ed25519 public key")
	}
	return edKey, nil
}

type contextKey struct{}

var claimsContextKey = contextKey{}

func NewContext(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsContextKey).(Claims)
	return c, ok
}

func MustFromContext(ctx context.Context) Claims {
	c, ok := FromContext(ctx)
	if !ok {
		panic("authn: claims not found in context")
	}
	return c
}

func UnaryInterceptor(v TokenVerifier, public map[string]bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if public != nil && public[info.FullMethod] {
			return handler(ctx, req)
		}
		if v == nil {
			return nil, status.Error(codes.Unauthenticated, "unauthenticated")
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		vals := md.Get("authorization")
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}
		authHeader := vals[0]
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization header format")
		}
		tokenStr := strings.TrimPrefix(authHeader, prefix)
		claims, err := v.Verify(tokenStr)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		ctx = NewContext(ctx, claims)
		return handler(ctx, req)
	}
}
