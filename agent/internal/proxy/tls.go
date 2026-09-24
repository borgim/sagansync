package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/caddyserver/certmagic"
)

type TLSOptions struct {
	StorageDir string // where certificates and ACME accounts are kept
	Email      string // optional ACME account email
	CA         string // ACME directory URL; Let's Encrypt production when empty
	RootCAPath string // extra CA to trust when talking to the ACME server (tests)
}

// TLS obtains and serves certificates for hosts that sagand routes, and only
// for those, so unknown hostnames cannot trigger issuance.
type TLS struct {
	cache *certmagic.Cache
	magic *certmagic.Config
	acme  *certmagic.ACMEIssuer
}

func NewTLS(opts TLSOptions, allowed func(host string) bool) (*TLS, error) {
	issuer := certmagic.ACMEIssuer{CA: opts.CA, Email: opts.Email, Agreed: true}
	if issuer.CA == "" {
		issuer.CA = certmagic.LetsEncryptProductionCA
	}
	if opts.RootCAPath != "" {
		pem, err := os.ReadFile(opts.RootCAPath)
		if err != nil {
			return nil, fmt.Errorf("read ACME root CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", opts.RootCAPath)
		}
		issuer.TrustedRoots = pool
	}
	t := &TLS{}
	t.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return t.magic, nil },
	})
	t.magic = certmagic.New(t.cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: opts.StorageDir},
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: func(_ context.Context, name string) error {
				if allowed(name) {
					return nil
				}
				return fmt.Errorf("%s is not served by sagand", name)
			},
		},
	})
	t.acme = certmagic.NewACMEIssuer(t.magic, issuer)
	t.magic.Issuers = []certmagic.Issuer{t.acme}
	return t, nil
}

func (t *TLS) TLSConfig() *tls.Config {
	cfg := t.magic.TLSConfig()
	cfg.NextProtos = append([]string{"h2", "http/1.1"}, cfg.NextProtos...)
	return cfg
}

// HTTPHandler answers ACME HTTP-01 challenges and passes everything else on.
func (t *TLS) HTTPHandler(next http.Handler) http.Handler { return t.acme.HTTPChallengeHandler(next) }

// Ensure obtains (or renews) the certificate for host right away.
func (t *TLS) Ensure(ctx context.Context, host string) error {
	return t.magic.ManageSync(ctx, []string{host})
}

func (t *TLS) Close() { t.cache.Stop() }
