package authn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
)

func generateKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, []byte, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 keys: %v", err)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal pkcs8: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	pubBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("failed to marshal pkix: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	return pub, priv, privPEM, pubPEM
}

func TestPEMParsing(t *testing.T) {
	pub, priv, privPEM, pubPEM := generateKeys(t)

	parsedPriv, err := authn.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("unexpected error parsing private key: %v", err)
	}
	if !parsedPriv.Equal(priv) {
		t.Fatal("parsed private key does not match original")
	}

	parsedPub, err := authn.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("unexpected error parsing public key: %v", err)
	}
	if !parsedPub.Equal(pub) {
		t.Fatal("parsed public key does not match original")
	}

	if _, err := authn.ParsePrivateKeyPEM([]byte("invalid pem")); err == nil {
		t.Fatal("expected error on invalid pem")
	}
	if _, err := authn.ParsePublicKeyPEM([]byte("invalid pem")); err == nil {
		t.Fatal("expected error on invalid pem")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _, _ := generateKeys(t)
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, 15*time.Minute)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	token, expiresAt, err := signer.Sign("user-123", []string{"customer", "admin"})
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if expiresAt.Before(time.Now()) {
		t.Fatalf("expiresAt should be in the future")
	}

	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Fatalf("expected subject user-123, got %s", claims.Subject)
	}
	if !claims.IsAdmin() {
		t.Fatal("expected IsAdmin to be true")
	}
	if !claims.HasRole("customer") {
		t.Fatal("expected HasRole(customer) to be true")
	}
	if claims.HasRole("unknown") {
		t.Fatal("expected HasRole(unknown) to be false")
	}
}

func TestExpiredToken(t *testing.T) {
	pub, priv, _, _ := generateKeys(t)
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, -1*time.Minute)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	token, _, err := signer.Sign("user-123", []string{"customer"})
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	_, err = verifier.Verify(token)
	if err == nil {
		t.Fatal("expected verification error for expired token")
	}
}

func TestWrongAudience(t *testing.T) {
	pub, priv, _, _ := generateKeys(t)
	signer := authn.NewSigner(priv, authn.Issuer, "wrong-audience", 15*time.Minute)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	token, _, err := signer.Sign("user-123", []string{"customer"})
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	_, err = verifier.Verify(token)
	if err == nil {
		t.Fatal("expected verification error for wrong audience")
	}
}

func TestAlgNone(t *testing.T) {
	pub, _, _, _ := generateKeys(t)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	// Create an unsigned token with alg=none
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "user-123",
		"iss": authn.Issuer,
		"aud": authn.Audience,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to create none token: %v", err)
	}

	_, err = verifier.Verify(tokenStr)
	if err == nil {
		t.Fatal("expected error rejecting alg=none")
	}
}

func TestWrongKey(t *testing.T) {
	_, priv1, _, _ := generateKeys(t)
	pub2, _, _, _ := generateKeys(t)

	signer := authn.NewSigner(priv1, authn.Issuer, authn.Audience, 15*time.Minute)
	verifier := authn.NewVerifier(pub2, authn.Issuer, authn.Audience)

	token, _, err := signer.Sign("user-123", []string{"customer"})
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	_, err = verifier.Verify(token)
	if err == nil {
		t.Fatal("expected verification error with wrong public key")
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	if _, ok := authn.FromContext(ctx); ok {
		t.Fatal("expected FromContext to return false for empty context")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected MustFromContext to panic")
		}
	}()
	authn.MustFromContext(ctx)
}

func TestUnaryInterceptor(t *testing.T) {
	pub, priv, _, _ := generateKeys(t)
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, 15*time.Minute)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	publicMethods := map[string]bool{
		"/user.v1.AuthService/Login":    true,
		"/user.v1.AuthService/Register": true,
	}

	interceptor := authn.UnaryInterceptor(verifier, publicMethods)

	dummyHandler := func(ctx context.Context, req any) (any, error) {
		claims, ok := authn.FromContext(ctx)
		if ok {
			return claims.Subject, nil
		}
		return "public-ok", nil
	}

	t.Run("public method bypass", func(t *testing.T) {
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.AuthService/Login"}
		res, err := interceptor(context.Background(), nil, info, dummyHandler)
		if err != nil || res != "public-ok" {
			t.Fatalf("expected public method to pass without auth, got res=%v, err=%v", res, err)
		}
	})

	t.Run("default deny - missing metadata", func(t *testing.T) {
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.UserService/GetProfile"}
		_, err := interceptor(context.Background(), nil, info, dummyHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", err)
		}
	})

	t.Run("missing authorization header", func(t *testing.T) {
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.UserService/GetProfile"}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("other-header", "foo"))
		_, err := interceptor(ctx, nil, info, dummyHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", err)
		}
	})

	t.Run("invalid authorization format", func(t *testing.T) {
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.UserService/GetProfile"}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Basic 12345"))
		_, err := interceptor(ctx, nil, info, dummyHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", err)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.UserService/GetProfile"}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer invalid-token"))
		_, err := interceptor(ctx, nil, info, dummyHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", err)
		}
	})

	t.Run("valid token injects claims", func(t *testing.T) {
		token, _, err := signer.Sign("user-456", []string{"customer"})
		if err != nil {
			t.Fatalf("sign failed: %v", err)
		}
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.UserService/GetProfile"}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
		res, err := interceptor(ctx, nil, info, dummyHandler)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != "user-456" {
			t.Fatalf("expected user-456, got %v", res)
		}
	})

	t.Run("nil verifier denies protected method", func(t *testing.T) {
		nilInterceptor := authn.UnaryInterceptor(nil, publicMethods)
		info := &grpc.UnaryServerInfo{FullMethod: "/user.v1.UserService/GetProfile"}
		_, err := nilInterceptor(context.Background(), nil, info, dummyHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", err)
		}
	})
}
