package service

import (
	"context"
	"errors"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

// Setting keys (namespace "gateway.tls.") that hold the edge-TLS ACME/DNS
// credentials. Operators manage these in the Settings UI; the secret ones are
// stored encrypted at rest. They are pushed to the gateway by
// GatewayTLSSyncService so the gateway never needs them baked into its env.
const (
	SettingKeyGatewayTLSACMEEmail      = "gateway.tls.acme_email"
	SettingKeyGatewayTLSACMECADir      = "gateway.tls.acme_ca"          // optional; defaults to ZeroSSL
	SettingKeyGatewayTLSEABKeyID       = "gateway.tls.eab_kid"          // secret
	SettingKeyGatewayTLSEABHMACKey     = "gateway.tls.eab_hmac"         // secret
	SettingKeyGatewayTLSDNSProvider    = "gateway.tls.dns_provider"     // optional; defaults to alidns
	SettingKeyGatewayTLSAliyunAKID     = "gateway.tls.aliyun_ak_id"     // secret
	SettingKeyGatewayTLSAliyunAKSecret = "gateway.tls.aliyun_ak_secret" // secret
	SettingKeyGatewayTLSStaticDomains  = "gateway.tls.static_domains"   // optional; space/comma separated

	settingKeyGatewayTLSPrefix = "gateway.tls."

	defaultGatewayTLSCADir       = "https://acme.zerossl.com/v2/DV90"
	defaultGatewayTLSDNSProvider = "alidns"
)

// settingReader is the slice of SettingService the sync needs.
type settingReader interface {
	GetByPrefix(prefix string) map[string]string
}

// tlsConfigPusher pushes a TLS config bundle to a gateway (gatewayclient.Client).
type tlsConfigPusher interface {
	PutTLSConfig(ctx context.Context, req contract.UpdateTLSConfigRequest) error
}

// ErrGatewayTLSNotConfigured is returned by Sync when the required credential
// settings are absent, so callers can treat "not set up yet" as benign rather
// than an error to alert on.
var ErrGatewayTLSNotConfigured = errors.New("gateway tls settings not configured")

// GatewayTLSSyncService reads the gateway.tls.* settings and pushes them to the
// gateway. It is driven both once at startup and periodically (so a gateway
// that restarts and loses the in-memory config is re-primed, and so credential
// rotations in Settings propagate without a control-plane restart).
type GatewayTLSSyncService struct {
	settings settingReader
	pusher   tlsConfigPusher
}

func NewGatewayTLSSyncService(settings settingReader, pusher tlsConfigPusher) *GatewayTLSSyncService {
	return &GatewayTLSSyncService{settings: settings, pusher: pusher}
}

// Sync builds the request from current settings and pushes it. It returns
// ErrGatewayTLSNotConfigured (and pushes nothing) when the mandatory
// credentials are missing, so the operator hasn't-configured-yet case is quiet.
func (s *GatewayTLSSyncService) Sync(ctx context.Context) error {
	kv := s.settings.GetByPrefix(settingKeyGatewayTLSPrefix)

	email := strings.TrimSpace(kv[SettingKeyGatewayTLSACMEEmail])
	akID := strings.TrimSpace(kv[SettingKeyGatewayTLSAliyunAKID])
	akSecret := strings.TrimSpace(kv[SettingKeyGatewayTLSAliyunAKSecret])
	// The gateway will reject an incomplete config; skip the push until the
	// operator has entered at least the DNS credentials and ACME email.
	if email == "" || akID == "" || akSecret == "" {
		return ErrGatewayTLSNotConfigured
	}

	caDir := strings.TrimSpace(kv[SettingKeyGatewayTLSACMECADir])
	if caDir == "" {
		caDir = defaultGatewayTLSCADir
	}
	dnsProvider := strings.TrimSpace(kv[SettingKeyGatewayTLSDNSProvider])
	if dnsProvider == "" {
		dnsProvider = defaultGatewayTLSDNSProvider
	}

	req := contract.UpdateTLSConfigRequest{
		Email:          email,
		CADir:          caDir,
		EABKeyID:       strings.TrimSpace(kv[SettingKeyGatewayTLSEABKeyID]),
		EABHMACKey:     strings.TrimSpace(kv[SettingKeyGatewayTLSEABHMACKey]),
		DNSProvider:    dnsProvider,
		AliyunAKID:     akID,
		AliyunAKSecret: akSecret,
		StaticDomains:  splitDomains(kv[SettingKeyGatewayTLSStaticDomains]),
	}
	return s.pusher.PutTLSConfig(ctx, req)
}

// splitDomains splits on whitespace and/or commas so the Settings value can use
// either separator (matching the Caddyfile's space-separated multi-domain style).
func splitDomains(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ','
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
