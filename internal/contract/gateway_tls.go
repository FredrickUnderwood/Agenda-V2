package contract

// UpdateTLSConfigRequest is the payload the control plane pushes to the gateway
// admin endpoint (PUT /-/tls) to configure edge-TLS certificate issuance. The
// values originate from the control plane's Settings (namespace "gateway.tls.")
// where the secret fields are stored encrypted at rest; they travel to the
// gateway over the service-token-authenticated admin API and are held only in
// the gateway's memory (never persisted on the gateway side).
//
// The gateway's bootstrap decides whether it is the TLS edge at all
// (GATEWAY_TLS_ENABLED) and which port/storage to use; this request only
// carries the operator-rotatable credentials + the extra static domains.
type UpdateTLSConfigRequest struct {
	Email          string   `json:"email"`
	CADir          string   `json:"ca_dir"`
	EABKeyID       string   `json:"eab_kid"`
	EABHMACKey     string   `json:"eab_hmac"`
	DNSProvider    string   `json:"dns_provider"`
	AliyunAKID     string   `json:"aliyun_ak_id"`
	AliyunAKSecret string   `json:"aliyun_ak_secret"`
	StaticDomains  []string `json:"static_domains,omitempty"`
}
