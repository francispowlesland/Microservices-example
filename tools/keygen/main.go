// Command keygen prints an Ed25519 key pair as PEM.
//
//	keygen          user-token pair: JWT_PRIVATE_KEY (users only), JWT_PUBLIC_KEY (every verifier)
//	keygen service  service-token pair: SERVICE_PRIVATE_KEY (orders only), SERVICE_PUBLIC_KEY (inventory only)
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

func main() {
	prefix, privWho, pubWho := "JWT", "Users service only", "All services"
	if len(os.Args) > 1 && os.Args[1] == "service" {
		prefix, privWho, pubWho = "SERVICE", "Orders service only", "Inventory service only"
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate ed25519 key: %v\n", err)
		os.Exit(1)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal private key: %v\n", err)
		os.Exit(1)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	pubBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal public key: %v\n", err)
		os.Exit(1)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	fmt.Printf("=== %s_PRIVATE_KEY (%s) ===\n", prefix, privWho)
	fmt.Print(string(privPEM))
	fmt.Printf("\n=== %s_PUBLIC_KEY (%s) ===\n", prefix, pubWho)
	fmt.Print(string(pubPEM))
}
