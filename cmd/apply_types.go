package cmd

import (
	"regexp"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

type applyFile struct {
	Version string        `json:"version" yaml:"version"`
	Tunnels []applyTunnel `json:"tunnels" yaml:"tunnels"`
}

type applyTunnel struct {
	Name          string             `json:"name" yaml:"name"`
	Target        string             `json:"target,omitempty" yaml:"target,omitempty"`
	LocalPort     int                `json:"localPort" yaml:"localPort"`
	Port          int                `json:"port,omitempty" yaml:"port,omitempty"`
	Protocol      string             `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Domain        string             `json:"domain,omitempty" yaml:"domain,omitempty"`
	TTL           string             `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	WaitDomain    bool               `json:"waitDomain,omitempty" yaml:"waitDomain,omitempty"`
	ReadyTimeout  string             `json:"readyTimeout,omitempty" yaml:"readyTimeout,omitempty"`
	DomainTimeout string             `json:"domainTimeout,omitempty" yaml:"domainTimeout,omitempty"`
	TargetTLS     *applyTargetTLS    `json:"targetTls,omitempty" yaml:"targetTls,omitempty"`
	Resources     *applyResources    `json:"resources,omitempty" yaml:"resources,omitempty"`
	BasicAuth     *applyBasicAuth    `json:"basicAuth,omitempty" yaml:"basicAuth,omitempty"`
	AccessPolicy  *applyAccessPolicy `json:"accessPolicy,omitempty" yaml:"accessPolicy,omitempty"`
}

type applyResources struct {
	Requests *applyResourceValues `json:"requests,omitempty" yaml:"requests,omitempty"`
	Limits   *applyResourceValues `json:"limits,omitempty" yaml:"limits,omitempty"`
}

type applyResourceValues struct {
	CPU    string `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
}

type applyTargetTLS struct {
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
}

type applyBasicAuth struct {
	Credential  string `json:"credential,omitempty" yaml:"credential,omitempty"`
	Username    string `json:"username" yaml:"username"`
	Password    string `json:"password,omitempty" yaml:"password,omitempty"`
	PasswordEnv string `json:"passwordEnv,omitempty" yaml:"passwordEnv,omitempty"`
}

type diffResult struct {
	Name                               string                  `json:"name"`
	TunnelID                           string                  `json:"tunnelId"`
	Action                             string                  `json:"action"`
	Changes                            []string                `json:"changes,omitempty"`
	Warnings                           []string                `json:"warnings,omitempty"`
	DesiredPort                        string                  `json:"desiredPort,omitempty"`
	CurrentPort                        string                  `json:"currentPort,omitempty"`
	DesiredTarget                      string                  `json:"desiredTarget,omitempty"`
	CurrentTarget                      string                  `json:"currentTarget,omitempty"`
	DesiredHost                        string                  `json:"desiredHost,omitempty"`
	CurrentHost                        string                  `json:"currentHost,omitempty"`
	ExpiresAt                          string                  `json:"expiresAt,omitempty"`
	TargetTLSInsecureSkipVerify        bool                    `json:"targetTlsInsecureSkipVerify,omitempty"`
	CurrentTargetTLSInsecureSkipVerify bool                    `json:"currentTargetTlsInsecureSkipVerify,omitempty"`
	DesiredResources                   *session.ResourceConfig `json:"desiredResources,omitempty"`
	CurrentResources                   *session.ResourceConfig `json:"currentResources,omitempty"`
	AccessPolicy                       bool                    `json:"accessPolicy"`
	BasicAuth                          bool                    `json:"basicAuth"`
}

type applyResult struct {
	Name                        string                  `json:"name"`
	TunnelID                    string                  `json:"tunnelId"`
	Protocol                    string                  `json:"protocol"`
	Host                        string                  `json:"host"`
	SealosHost                  string                  `json:"sealosHost,omitempty"`
	CustomDomain                string                  `json:"customDomain,omitempty"`
	PublicPort                  int32                   `json:"publicPort,omitempty"`
	LocalPort                   string                  `json:"localPort"`
	TargetURL                   string                  `json:"targetUrl,omitempty"`
	TargetTLSInsecureSkipVerify bool                    `json:"targetTlsInsecureSkipVerify,omitempty"`
	Resources                   *session.ResourceConfig `json:"resources,omitempty"`
	BasicAuth                   bool                    `json:"basicAuth"`
	BasicAuthUser               string                  `json:"basicAuthUser,omitempty"`
	AccessPolicy                bool                    `json:"accessPolicy"`
	ExpiresAt                   string                  `json:"expiresAt,omitempty"`
	TemporaryURLs               []string                `json:"temporaryUrls,omitempty"`
	Status                      string                  `json:"status"`
	Warnings                    []string                `json:"warnings,omitempty"`
	NewTunnel                   bool                    `json:"-"`
	Previous                    *session.TunnelSession  `json:"-"`
}

type normalizedApplyTunnel struct {
	Name          string
	TunnelID      string
	LocalPort     string
	TargetURL     string
	Protocol      string
	CustomDomain  string
	BasicAuth     *session.BasicAuthConfig
	BasicAuthPass string
	TargetTLS     *session.TargetTLSConfig
	Resources     *session.ResourceConfig
	AccessPolicy  *session.AccessPolicy
	TTL           string
	ExpiresAt     string
	WaitDomain    bool
	ReadyTimeout  time.Duration
	DomainTimeout time.Duration
}

var applyNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,53}[a-z0-9])?$`)
