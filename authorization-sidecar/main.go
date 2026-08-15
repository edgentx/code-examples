package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

// The deployment tells the service how to be unreachable except through the
// sidecar. Every value is a path or a name supplied from outside; there is no
// certificate, key, or authority compiled into this binary.
const (
	certFileEnv     = "TLS_CERT_FILE"
	keyFileEnv      = "TLS_KEY_FILE"
	clientCAFileEnv = "TLS_CLIENT_CA_FILE"
	clientNameEnv   = "TLS_CLIENT_NAME"
)

func main() {
	address := os.Getenv("UPSTREAM_ADDR")
	if address == "" {
		address = ":8080"
	}

	server := &http.Server{
		Addr:              address,
		Handler:           newRouter(newDocumentStore()),
		ReadHeaderTimeout: 5 * time.Second,
	}

	certFile := os.Getenv(certFileEnv)
	keyFile := os.Getenv(keyFileEnv)
	clientCAFile := os.Getenv(clientCAFileEnv)

	// Plain HTTP is still supported, and the README uses it to show what the
	// application answers when nothing is in front of it: everything, to
	// anybody. It is not how the stack runs.
	if certFile == "" || keyFile == "" || clientCAFile == "" {
		log.Printf("upstream document service listening on %s over plain HTTP "+
			"(no authorization code inside, and no client verification)", address)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("upstream document service: %v", err)
		}
		return
	}

	clientName := os.Getenv(clientNameEnv)
	if clientName == "" {
		// Refusing to start is the right failure. Accepting any certificate the
		// authority signed would look like mutual TLS in every log line and
		// enforce nothing about who is calling.
		log.Fatalf("%s is required when client certificates are verified", clientNameEnv)
	}

	tlsConfig, err := mutualTLS(certFile, keyFile, clientCAFile, clientName)
	if err != nil {
		log.Fatalf("upstream document service: %v", err)
	}
	server.TLSConfig = tlsConfig

	log.Printf("upstream document service listening on %s, accepting connections only from %q",
		address, clientName)
	// The certificate and key are already in the configuration, so they are not
	// passed again here.
	if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		log.Fatalf("upstream document service: %v", err)
	}
}
