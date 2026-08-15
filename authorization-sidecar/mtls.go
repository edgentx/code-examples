package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"slices"
)

// The application's half of "the sidecar cannot be bypassed".
//
// Everything else in this example makes the sidecar the place a decision is
// made. None of it stops something on the same network from dialing the
// application directly and skipping the decision entirely -- and in the compose
// stack you can watch that happen. Mutual TLS is what closes it: the application
// completes a handshake only with a client that presents a certificate signed by
// the example authority AND issued to the sidecar, so the proxy is not merely
// the recommended path to the application, it is the only one.
//
// The two halves are separate on purpose. `RequireAndVerifyClientCert` answers
// "was this certificate issued by an authority we trust"; the SAN check answers
// "was it issued to the one caller we accept". A deployment that stops at the
// first has decided that every holder of any certificate its authority ever
// signed may reach the application, which in a cluster is every workload in it.

var (
	// ErrNoPeerCertificate is returned when a connection completes verification
	// with no client certificate at all, which should be unreachable under
	// RequireAndVerifyClientCert and is refused here rather than assumed.
	ErrNoPeerCertificate = errors.New("no verified client certificate")
	// ErrUnexpectedClient is returned when the certificate is valid and was
	// issued to somebody else.
	ErrUnexpectedClient = errors.New("client certificate was not issued to the sidecar")
)

// authorizedClient reports whether a verified peer certificate belongs to the
// caller this service accepts connections from. The name is checked against the
// subject alternative names, not the common name: the common name has not been
// an identity for this purpose since RFC 2818 was replaced, and treating it as
// one accepts certificates that were never issued for this use.
func authorizedClient(certificate *x509.Certificate, expected string) error {
	if certificate == nil {
		return ErrNoPeerCertificate
	}
	if !slices.Contains(certificate.DNSNames, expected) {
		return fmt.Errorf("%w: presented %v, expected %q",
			ErrUnexpectedClient, certificate.DNSNames, expected)
	}
	return nil
}

// verifyPeer is the tls.Config hook that applies authorizedClient to whatever
// chain the handshake verified. It runs after the standard verification, so by
// the time it is called the certificate is already known to chain to the
// configured authority and to be within its validity period.
func verifyPeer(expected string) func([][]byte, [][]*x509.Certificate) error {
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			return ErrNoPeerCertificate
		}
		// The leaf is the first certificate of a verified chain.
		return authorizedClient(verifiedChains[0][0], expected)
	}
}

// mutualTLS builds the server configuration from the certificate, the key, and
// the authority the deployment supplies.
func mutualTLS(certFile, keyFile, clientCAFile, expectedClient string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading the service certificate: %w", err)
	}

	pem, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("reading the client authority: %w", err)
	}
	// A fresh pool, never the system pool. Trusting the system roots here would
	// mean any publicly issued certificate could open a connection to an
	// internal service.
	authority := x509.NewCertPool()
	if !authority.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificate found in %s", clientCAFile)
	}

	return &tls.Config{
		MinVersion:            tls.VersionTLS12,
		Certificates:          []tls.Certificate{certificate},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             authority,
		VerifyPeerCertificate: verifyPeer(expectedClient),
	}, nil
}
