package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The verification the application does for itself, checked without a container
// runtime. smoke.sh proves the property end to end against a real proxy; these
// cases prove the rule the proxy is being held to, including the one that is
// easy to get wrong -- a certificate the authority signed, issued to somebody
// else.

func TestAuthorizedClient(t *testing.T) {
	const expected = "authorization-sidecar.svc.example"

	tests := []struct {
		name    string
		names   []string
		wantErr error
	}{
		{
			name:  "the sidecar's own certificate",
			names: []string{expected},
		},
		{
			name:  "a certificate that names the sidecar among others",
			names: []string{"mesh.example", expected},
		},
		{
			// This is the case that matters. The certificate is valid, it chains
			// to the authority, and it belongs to a different workload. In a
			// cluster that workload is every other service sharing the mesh
			// authority, which is precisely the population that must not be able
			// to skip the decision point.
			name:    "a valid certificate issued to another workload",
			names:   []string{"batch-exporter.svc.example"},
			wantErr: ErrUnexpectedClient,
		},
		{
			name:    "a certificate with no subject alternative name at all",
			names:   nil,
			wantErr: ErrUnexpectedClient,
		},
		{
			name:    "a near miss",
			names:   []string{"authorization-sidecar.svc.example.attacker.test"},
			wantErr: ErrUnexpectedClient,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certificate := &x509.Certificate{DNSNames: test.names}

			err := authorizedClient(certificate, expected)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("authorizedClient = %v, want %v", err, test.wantErr)
			}
		})
	}
}

// TestAuthorizedClientRefusesTheCommonName keeps a habit out of the code. Before
// subject alternative names, the common name was the identity; it is not one
// now, and a service that still reads it accepts certificates that were never
// issued for this use.
func TestAuthorizedClientRefusesTheCommonName(t *testing.T) {
	const expected = "authorization-sidecar.svc.example"
	certificate := &x509.Certificate{
		Subject:  pkix.Name{CommonName: expected},
		DNSNames: nil,
	}

	if err := authorizedClient(certificate, expected); !errors.Is(err, ErrUnexpectedClient) {
		t.Fatalf("a common name was accepted as an identity: %v", err)
	}
}

func TestVerifyPeerRefusesAnEmptyChain(t *testing.T) {
	if err := verifyPeer("anything")(nil, nil); !errors.Is(err, ErrNoPeerCertificate) {
		t.Fatalf("verifyPeer with no chain = %v, want %v", err, ErrNoPeerCertificate)
	}
}

// TestHandshake is the whole rule end to end over a real TLS connection: the
// sidecar's certificate gets in, no certificate does not, and a certificate the
// same authority issued to something else does not either.
func TestHandshake(t *testing.T) {
	const expected = "authorization-sidecar.svc.example"

	authority, authorityKey := issueAuthority(t)
	pool := x509.NewCertPool()
	pool.AddCert(authority)

	serverCert := issueLeaf(t, authority, authorityKey, "upstream", x509.ExtKeyUsageServerAuth)
	sidecarCert := issueLeaf(t, authority, authorityKey, expected, x509.ExtKeyUsageClientAuth)
	otherCert := issueLeaf(t, authority, authorityKey, "batch-exporter.svc.example",
		x509.ExtKeyUsageClientAuth)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "reached the application")
	}))
	server.TLS = &tls.Config{
		MinVersion:            tls.VersionTLS12,
		Certificates:          []tls.Certificate{serverCert},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             pool,
		VerifyPeerCertificate: verifyPeer(expected),
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	tests := []struct {
		name        string
		client      []tls.Certificate
		wantReached bool
	}{
		{"the sidecar reaches the application", []tls.Certificate{sidecarCert}, true},
		{"a caller with no certificate is refused at the handshake", nil, false},
		{"a caller with the wrong certificate is refused at the handshake",
			[]tls.Certificate{otherCert}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS12,
				RootCAs:      pool,
				Certificates: test.client,
				ServerName:   "upstream",
			}}}

			response, err := client.Get(server.URL)
			if test.wantReached {
				if err != nil {
					t.Fatalf("the sidecar could not reach the application: %v", err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", response.StatusCode)
				}
				return
			}
			// The refusal is a failed handshake, not a status code. There is no
			// HTTP response to read, which is the point: the request never
			// became a request.
			if err == nil {
				response.Body.Close()
				t.Fatal("the connection was accepted; the application is reachable without the sidecar")
			}
		})
	}
}

// issueAuthority mints a throwaway certificate authority for the test.
func issueAuthority(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the authority key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test authority"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the authority certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the authority certificate: %v", err)
	}
	return certificate, key
}

// issueLeaf signs one certificate for a named workload.
func issueLeaf(t *testing.T, authority *x509.Certificate, authorityKey *ecdsa.PrivateKey,
	name string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the key for %s: %v", name, err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, authorityKey)
	if err != nil {
		t.Fatalf("signing the certificate for %s: %v", name, err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        mustParse(t, der),
	}
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing a test certificate: %v", err)
	}
	return certificate
}
