// Package edgetls terminates TLS on the gateway and auto-issues certificates
// via ACME, replacing the standalone agenda-caddy edge container by embedding
// Caddy's cert engine (CertMagic) directly in the gateway process.
//
// It is purpose-built for the constraints agenda-caddy documented: on Aliyun
// mainland nodes port 80 is hijacked by a "beian-block" page for un-filed
// domains, so HTTP-01/TLS-ALPN validation is unusable. We therefore issue only
// via DNS-01 against Aliyun DNS (libdns/alidns), using ZeroSSL as the CA
// (Let's Encrypt is effectively unreachable from mainland), which requires an
// EAB credential. Propagation checks are pinned to Aliyun public resolvers
// (223.5.5.5/223.6.6.6) because the container's embedded resolver (127.0.0.11)
// cannot see the authoritative TXT record and would hang indefinitely.
//
// Configuration is split in two:
//   - Options (bootstrap, from GATEWAY_TLS_* env): whether this gateway is the
//     TLS edge, the :443 listen addr, cert storage path, resolvers/timeouts.
//     These govern process/port lifecycle and rarely change.
//   - Config (runtime, pushed by the control plane from platform Settings):
//     the ACME account + Aliyun DNS credentials. These are secrets an operator
//     rotates from the Settings UI, so they arrive via Reconfigure and are
//     hot-swapped without restarting the gateway.
package edgetls

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"github.com/caddyserver/certmagic"
	"github.com/libdns/alidns"
	"github.com/mholt/acmez/v3/acme"
	"go.uber.org/zap"
)

// DNSProviderAlidns is the only DNS-01 provider wired today. Kept as a named
// constant so config validation and future providers have a single anchor.
const DNSProviderAlidns = "alidns"

// Options is the bootstrap configuration parsed from GATEWAY_TLS_* env. It
// carries nothing secret; credentials arrive at runtime via Reconfigure.
type Options struct {
	Enabled bool

	// HTTPSAddr is the listen address for the TLS data plane, e.g. ":443".
	HTTPSAddr string

	// Resolvers used for the DNS-01 propagation check. Must be able to see the
	// authoritative zone (see package doc for why the default resolver fails).
	Resolvers          []string
	PropagationTimeout time.Duration

	// StoragePath is where CertMagic persists ACME accounts and issued certs.
	// It MUST be a durable volume: losing it can trip CA rate limits on
	// re-issue. Mirrors agenda-caddy's caddy_data volume.
	StoragePath string

	// ReconcileInterval is how often the managed-domain set is recomputed from
	// the runtime Config's static domains ∪ live route hosts.
	ReconcileInterval time.Duration
}

func (o Options) validate() error {
	if o.HTTPSAddr == "" {
		return errors.New("edgetls: HTTPSAddr is required")
	}
	if o.StoragePath == "" {
		return errors.New("edgetls: StoragePath is required")
	}
	return nil
}

// Config is the runtime, operator-managed configuration pushed by the control
// plane (sourced from platform Settings). It holds the ACME account and the
// Aliyun DNS credentials used to solve DNS-01 challenges.
type Config struct {
	Email      string
	CADir      string // ACME directory URL, e.g. ZeroSSL's DV90 endpoint
	EABKeyID   string
	EABHMACKey string

	// DNS-01 provider. Only DNSProviderAlidns is supported today.
	DNSProvider    string
	AliyunAKID     string
	AliyunAKSecret string

	// StaticDomains are hostnames to always manage even if they have no dynamic
	// gateway route (e.g. a domain fronted by a plain reverse-proxy route).
	// Dynamic route hosts are added on top.
	StaticDomains []string
}

func (c Config) validate() error {
	if c.DNSProvider != DNSProviderAlidns {
		return fmt.Errorf("edgetls: unsupported DNS provider %q (only %q)", c.DNSProvider, DNSProviderAlidns)
	}
	if c.AliyunAKID == "" || c.AliyunAKSecret == "" {
		return errors.New("edgetls: aliyun access key id/secret are required for DNS-01")
	}
	if c.Email == "" {
		return errors.New("edgetls: ACME email is required")
	}
	if c.CADir == "" {
		return errors.New("edgetls: ACME CA directory URL is required")
	}
	// ZeroSSL (and any EAB-gated CA) needs both halves of the EAB credential.
	if (c.EABKeyID == "") != (c.EABHMACKey == "") {
		return errors.New("edgetls: EAB key id and hmac must be set together")
	}
	return nil
}

// fingerprint is a stable digest of the credential-bearing fields, used to skip
// rebuilding CertMagic when a periodic re-push carries unchanged credentials.
func (c Config) fingerprint() string {
	h := sha256.New()
	static := append([]string(nil), c.StaticDomains...)
	sort.Strings(static)
	for _, part := range []string{
		c.Email, c.CADir, c.EABKeyID, c.EABHMACKey,
		c.DNSProvider, c.AliyunAKID, c.AliyunAKSecret,
		strings.Join(static, ","),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DomainSource returns the current set of dynamic hostnames that should have a
// certificate (typically the gateway's live route hosts). It is polled on each
// reconcile tick.
type DomainSource func() []string

// Manager owns the CertMagic config and the background reconcile loop. It is
// created (with the :443 listener) as soon as edge TLS is enabled, but does not
// issue anything until credentials arrive via Reconfigure.
type Manager struct {
	opts    Options
	logger  *zap.Logger
	storage certmagic.Storage
	source  DomainSource

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu          sync.RWMutex
	magic       *certmagic.Config // never nil after New; serves certs from storage
	cache       *certmagic.Cache
	cfg         Config
	fingerprint string
	ready       bool     // true once valid credentials have been applied
	managed     []string // last domain set handed to CertMagic, sorted+deduped
}

// New builds a Manager from opts. It returns (nil, nil) when TLS is disabled so
// callers can treat "no manager" as "plain HTTP only". The returned Manager can
// already serve any certs present in storage; issuance waits for Reconfigure.
func New(opts Options) (*Manager, error) {
	if !opts.Enabled {
		return nil, nil
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	m := &Manager{
		opts:    opts,
		logger:  alog.L().Named("edgetls"),
		storage: &certmagic.FileStorage{Path: opts.StoragePath},
	}
	// Build an issuer-less config so GetCertificate can already serve certs that
	// exist on disk (e.g. after a restart) before credentials are re-pushed.
	m.magic, m.cache = m.newMagic(nil)
	return m, nil
}

// newMagic builds a fresh CertMagic Config (and its backing Cache) wired to the
// shared on-disk storage. issuer may be nil (serve-only, no issuance).
func (m *Manager) newMagic(issuer certmagic.Issuer) (*certmagic.Config, *certmagic.Cache) {
	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			return magic, nil
		},
	})
	magic = certmagic.New(cache, certmagic.Config{
		Storage: m.storage,
		Logger:  m.logger,
		// ACME Renewal Info (ARI) is a renewal-timing optimization served from
		// the CA's own host (for ZeroSSL: ari.trust-provider.com), which is not
		// reachable from mainland Aliyun nodes. Leaving it on makes the ARI GET
		// time out and stall/retry inside issuance; disabling it falls back to
		// plain time-based renewal (RenewalWindowRatio) with no downside here.
		DisableARI: true,
		// OCSP stapling fetches from the CA's OCSP responder (for ZeroSSL/Sectigo:
		// ocsp.sectigo.com), also unreachable from mainland Aliyun nodes — it just
		// times out and logs a warning per cert. Stapling is optional (clients
		// fall back to their own revocation checks), so disable it here too.
		OCSP: certmagic.OCSPConfig{DisableStapling: true},
		// Without OnDemand, a handshake only serves certs already pre-loaded into
		// the in-memory cache — it will NOT read a managed cert from storage on a
		// cache miss (certmagic handshake.go: loadDynamically requires OnDemand).
		// So a cert we issued and persisted to /data would fail to serve after a
		// restart ("no certificate available"). Enabling OnDemand flips on the
		// load-from-storage-on-handshake path; DecisionFunc gates it to our
		// managed domains so it can't be abused to obtain certs for arbitrary
		// SNI. Background ManageAsync still pre-issues, so on-demand obtaining is
		// only a fallback for the gap before the next reconcile.
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: func(_ context.Context, name string) error {
				if m.isManaged(name) {
					return nil
				}
				return fmt.Errorf("edgetls: host %q is not a managed gateway domain", name)
			},
		},
	})
	if issuer != nil {
		magic.Issuers = []certmagic.Issuer{issuer}
	}
	return magic, cache
}

// HTTPSAddr is the configured TLS listen address.
func (m *Manager) HTTPSAddr() string { return m.opts.HTTPSAddr }

// TLSConfig returns a *tls.Config whose GetCertificate dispatches to the current
// CertMagic config under a read lock, so credential hot-swaps (Reconfigure) take
// effect without rebuilding the *tls.Config the http.Server already holds.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Advertise the normal HTTPS ALPN protocols. We must NOT set this to only
		// acmez.ACMETLS1Protocol ("acme-tls/1"): that is exclusively for the
		// TLS-ALPN-01 challenge (which we disable — we solve via DNS-01), and a
		// server offering only it fails ALPN negotiation with every real client
		// (curl/browsers offer h2,http/1.1) → "no application protocol", i.e. a
		// broken handshake even though the certificate is valid and loaded.
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			m.mu.RLock()
			magic := m.magic
			m.mu.RUnlock()
			return magic.GetCertificate(hello)
		},
	}
}

// Reconfigure applies operator credentials (typically pushed by the control
// plane from Settings). It is idempotent: an unchanged Config is a no-op, so a
// periodic re-push is cheap. A changed Config rebuilds the ACME issuer and
// hot-swaps the CertMagic config, then kicks an immediate reconcile.
func (m *Manager) Reconfigure(cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	fp := cfg.fingerprint()

	m.mu.RLock()
	unchanged := m.ready && fp == m.fingerprint
	m.mu.RUnlock()
	if unchanged {
		return nil
	}

	provider := &alidns.Provider{
		CredentialInfo: alidns.CredentialInfo{
			AccessKeyID:     cfg.AliyunAKID,
			AccessKeySecret: cfg.AliyunAKSecret,
		},
	}
	solver := &certmagic.DNS01Solver{
		DNSManager: certmagic.DNSManager{
			DNSProvider:        provider,
			Resolvers:          m.opts.Resolvers,
			PropagationTimeout: m.opts.PropagationTimeout,
			Logger:             m.logger,
		},
	}
	magic, cache := m.newMagic(nil)
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		CA:     cfg.CADir,
		Email:  cfg.Email,
		Agreed: true,
		// Mainland port 80/443 inbound is unreliable/hijacked; force DNS-01.
		DisableHTTPChallenge:    true,
		DisableTLSALPNChallenge: true,
		DNS01Solver:             solver,
		Logger:                  m.logger,
	})
	if cfg.EABKeyID != "" {
		issuer.ExternalAccount = &acme.EAB{KeyID: cfg.EABKeyID, MACKey: cfg.EABHMACKey}
	}
	magic.Issuers = []certmagic.Issuer{issuer}

	m.mu.Lock()
	old := m.cache
	m.magic = magic
	m.cache = cache
	m.cfg = cfg
	m.fingerprint = fp
	m.ready = true
	m.managed = nil // force the next reconcile to (re)manage against the new issuer
	m.mu.Unlock()

	if old != nil {
		old.Stop() // stop the superseded cache's maintenance goroutines
	}
	alog.Info(m.ctxOrBackground(), "edgetls credentials applied", zap.String("ca", cfg.CADir))

	if m.ctx != nil {
		if err := m.reconcile(m.ctx); err != nil {
			m.logger.Warn("edgetls reconcile after reconfigure failed", zap.Error(err))
		}
	}
	return nil
}

// Ready reports whether credentials have been applied and issuance is possible.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// Start launches the reconcile loop. source may be nil (only static domains are
// managed). Issuance only actually happens once Reconfigure has supplied
// credentials; until then reconcile is a no-op.
func (m *Manager) Start(ctx context.Context, source DomainSource) {
	m.source = source
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.wg.Add(1)
	go m.loop()
}

// Stop halts the reconcile loop.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.opts.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.reconcile(m.ctx); err != nil {
				m.logger.Warn("edgetls reconcile failed", zap.Error(err))
			}
		}
	}
}

// reconcile computes the desired managed-domain set and, if it changed since
// last time, hands it to CertMagic (async issue/renew). It is a no-op until
// credentials have been applied.
//
// Note: CertMagic v0.25 has no Unmanage; a hostname dropped from the set keeps
// its cached cert (and renewals) until the process restarts. That is harmless
// for an edge that only ever adds domains as routes appear.
func (m *Manager) reconcile(ctx context.Context) error {
	m.mu.RLock()
	ready := m.ready
	magic := m.magic
	static := m.cfg.StaticDomains
	prev := m.managed
	m.mu.RUnlock()
	if !ready {
		return nil
	}

	desired := m.desiredDomains(static)
	if equalStringSet(prev, desired) {
		return nil
	}
	m.mu.Lock()
	m.managed = desired
	m.mu.Unlock()

	if len(desired) == 0 {
		return nil
	}
	alog.Info(ctx, "edgetls managing domains", zap.Strings("domains", desired))
	// ManageAsync is idempotent: certs still valid are left alone; only missing
	// or near-expiry ones are (re)issued, on background goroutines.
	return magic.ManageAsync(ctx, desired)
}

func (m *Manager) desiredDomains(static []string) []string {
	set := make(map[string]struct{})
	add := func(d string) {
		if d = normalizeDomain(d); d != "" && publiclyCertifiable(d) {
			set[d] = struct{}{}
		}
	}
	for _, d := range static {
		add(d)
	}
	if m.source != nil {
		for _, d := range m.source() {
			add(d)
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// publiclyCertifiable reports whether a hostname is worth asking a public ACME
// CA to certify. Route hosts include internal/test names — a wildcard "*"
// catch-all, IP literals, single-label names, and reserved TLDs like .local —
// which can never get a public cert; managing them just spams the CA with
// doomed orders (wasting rate limit) and floods logs with "does not qualify"
// errors. Skip them so only real public domains enter the managed set.
func publiclyCertifiable(host string) bool {
	if host == "" || host == "*" {
		return false
	}
	if strings.ContainsAny(host, "*") {
		return false // wildcard route patterns aren't concrete hostnames
	}
	if net.ParseIP(host) != nil {
		return false // IP literal
	}
	i := strings.LastIndexByte(host, '.')
	if i < 0 || i == len(host)-1 {
		return false // single-label (no dot) or trailing-dot-only
	}
	switch host[i+1:] {
	case "local", "localhost", "internal", "lan", "home", "test", "invalid", "example":
		return false // reserved / non-public TLDs
	}
	return true
}

// isManaged reports whether name is in the current desired-domain set (the list
// last handed to CertMagic). Used by the OnDemand DecisionFunc to gate which
// SNI values may load-from-storage / be obtained.
func (m *Manager) isManaged(name string) bool {
	name = normalizeDomain(name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.managed {
		if d == name {
			return true
		}
	}
	return false
}

func (m *Manager) ctxOrBackground() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func normalizeDomain(d string) string {
	return strings.ToLower(strings.TrimSpace(d))
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
