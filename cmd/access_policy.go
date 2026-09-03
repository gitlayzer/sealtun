package cmd

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/session"
)

type accessPolicyInput struct {
	BearerToken       string
	BearerTokenEnv    string
	IPAllowlist       []string
	IPDenylist        []string
	TemporaryToken    string
	TemporaryTokenEnv string
	TemporaryTTL      time.Duration
	TemporaryName     string
	RateLimit         string
	AuditEnabled      bool
}

type applyAccessPolicy struct {
	BearerToken    string               `json:"bearerToken,omitempty" yaml:"bearerToken,omitempty"`
	BearerTokenEnv string               `json:"bearerTokenEnv,omitempty" yaml:"bearerTokenEnv,omitempty"`
	IPAllowlist    []string             `json:"ipAllowlist,omitempty" yaml:"ipAllowlist,omitempty"`
	IPDenylist     []string             `json:"ipDenylist,omitempty" yaml:"ipDenylist,omitempty"`
	TemporaryLinks []applyTemporaryLink `json:"temporaryLinks,omitempty" yaml:"temporaryLinks,omitempty"`
	RateLimit      string               `json:"rateLimit,omitempty" yaml:"rateLimit,omitempty"`
	Audit          *applyAuditConfig    `json:"audit,omitempty" yaml:"audit,omitempty"`
}

type applyTemporaryLink struct {
	Name      string `json:"name,omitempty" yaml:"name,omitempty"`
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	TokenEnv  string `json:"tokenEnv,omitempty" yaml:"tokenEnv,omitempty"`
	TTL       string `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty" yaml:"expiresAt,omitempty"`
}

type applyAuditConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

func resolveAccessPolicy(input accessPolicyInput, now time.Time, lookupEnv func(string) string) (*session.AccessPolicy, error) {
	policy := &session.AccessPolicy{}
	if input.BearerToken != "" || input.BearerTokenEnv != "" {
		token, err := resolveSecretValue(input.BearerToken, input.BearerTokenEnv, "bearer token", lookupEnv)
		if err != nil {
			return nil, err
		}
		hash, err := accesspolicy.HashToken(token)
		if err != nil {
			return nil, fmt.Errorf("bearer token: %w", err)
		}
		policy.BearerTokenHashes = append(policy.BearerTokenHashes, hash)
	}
	policy.IPAllowlist = normalizeStringList(input.IPAllowlist)
	policy.IPDenylist = normalizeStringList(input.IPDenylist)
	// If the user passed an IP list flag but every entry normalized away
	// (e.g. "--ip-allowlist ,"), silently creating an unrestricted tunnel
	// would contradict their intent; treat it as invalid input instead.
	if len(input.IPAllowlist) > 0 && len(policy.IPAllowlist) == 0 {
		return nil, fmt.Errorf("ip allowlist contains no valid entries")
	}
	if len(input.IPDenylist) > 0 && len(policy.IPDenylist) == 0 {
		return nil, fmt.Errorf("ip denylist contains no valid entries")
	}
	policy.RateLimit = strings.TrimSpace(input.RateLimit)
	if input.AuditEnabled {
		policy.Audit = &session.AuditConfig{Enabled: true}
	}
	if input.TemporaryToken != "" || input.TemporaryTokenEnv != "" {
		if input.TemporaryTTL <= 0 {
			return nil, fmt.Errorf("temporary access token requires --temporary-access-ttl greater than 0")
		}
		token, err := resolveSecretValue(input.TemporaryToken, input.TemporaryTokenEnv, "temporary access token", lookupEnv)
		if err != nil {
			return nil, err
		}
		hash, err := accesspolicy.HashToken(token)
		if err != nil {
			return nil, fmt.Errorf("temporary access token: %w", err)
		}
		policy.TemporaryTokens = append(policy.TemporaryTokens, session.TemporaryToken{
			Name:      strings.TrimSpace(input.TemporaryName),
			TokenHash: hash,
			TTL:       input.TemporaryTTL.String(),
			ExpiresAt: now.Add(input.TemporaryTTL).UTC().Format(time.RFC3339),
		})
	}
	if err := accesspolicy.Validate(accessPolicyToRuntime(policy)); err != nil {
		return nil, err
	}
	if accesspolicy.Empty(accessPolicyToRuntime(policy)) {
		return nil, nil
	}
	return policy, nil
}

func resolveApplyAccessPolicy(config *applyAccessPolicy, now time.Time, lookupEnv func(string) string) (*session.AccessPolicy, error) {
	if config == nil {
		return nil, nil
	}
	policy := &session.AccessPolicy{
		IPAllowlist: normalizeStringList(config.IPAllowlist),
		IPDenylist:  normalizeStringList(config.IPDenylist),
		RateLimit:   strings.TrimSpace(config.RateLimit),
	}
	if config.Audit != nil && config.Audit.Enabled {
		policy.Audit = &session.AuditConfig{Enabled: true}
	}
	if config.BearerToken != "" || config.BearerTokenEnv != "" {
		token, err := resolveSecretValue(config.BearerToken, config.BearerTokenEnv, "bearer token", lookupEnv)
		if err != nil {
			return nil, err
		}
		hash, err := accesspolicy.HashToken(token)
		if err != nil {
			return nil, fmt.Errorf("bearer token: %w", err)
		}
		policy.BearerTokenHashes = append(policy.BearerTokenHashes, hash)
	}
	for _, item := range config.TemporaryLinks {
		token, err := resolveSecretValue(item.Token, item.TokenEnv, "temporary link token", lookupEnv)
		if err != nil {
			return nil, err
		}
		hash, err := accesspolicy.HashToken(token)
		if err != nil {
			return nil, fmt.Errorf("temporary link token: %w", err)
		}
		expiresAt, err := resolveTemporaryLinkExpiry(item, now)
		if err != nil {
			return nil, err
		}
		policy.TemporaryTokens = append(policy.TemporaryTokens, session.TemporaryToken{
			Name:      strings.TrimSpace(item.Name),
			TokenHash: hash,
			TTL:       strings.TrimSpace(item.TTL),
			ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		})
	}
	if err := accesspolicy.Validate(accessPolicyToRuntime(policy)); err != nil {
		return nil, err
	}
	if accesspolicy.Empty(accessPolicyToRuntime(policy)) {
		return nil, nil
	}
	return policy, nil
}

func resolveTemporaryLinkExpiry(item applyTemporaryLink, now time.Time) (time.Time, error) {
	if strings.TrimSpace(item.TTL) != "" && strings.TrimSpace(item.ExpiresAt) != "" {
		return time.Time{}, fmt.Errorf("temporary link cannot set both ttl and expiresAt")
	}
	if strings.TrimSpace(item.TTL) != "" {
		ttl, err := time.ParseDuration(item.TTL)
		if err != nil {
			return time.Time{}, fmt.Errorf("temporary link ttl: %w", err)
		}
		if ttl <= 0 {
			return time.Time{}, fmt.Errorf("temporary link ttl must be greater than 0")
		}
		return now.Add(ttl), nil
	}
	if strings.TrimSpace(item.ExpiresAt) != "" {
		expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(item.ExpiresAt))
		if err != nil {
			return time.Time{}, fmt.Errorf("temporary link expiresAt: %w", err)
		}
		if !expiresAt.After(now) {
			return time.Time{}, fmt.Errorf("temporary link expiresAt must be in the future")
		}
		return expiresAt, nil
	}
	return time.Time{}, fmt.Errorf("temporary link requires ttl or expiresAt")
}

func accessPolicyToK8s(policy *session.AccessPolicy) *accesspolicy.Policy {
	return accessPolicyToRuntime(policy)
}

func accessPolicyToRuntime(policy *session.AccessPolicy) *accesspolicy.Policy {
	if policy == nil {
		return nil
	}
	tokens := make([]accesspolicy.TemporaryToken, 0, len(policy.TemporaryTokens))
	for _, token := range policy.TemporaryTokens {
		tokens = append(tokens, accesspolicy.TemporaryToken{
			Name:      token.Name,
			TokenHash: token.TokenHash,
			TTL:       token.TTL,
			ExpiresAt: token.ExpiresAt,
		})
	}
	return &accesspolicy.Policy{
		BearerTokenHashes: append([]string(nil), policy.BearerTokenHashes...),
		IPAllowlist:       append([]string(nil), policy.IPAllowlist...),
		IPDenylist:        append([]string(nil), policy.IPDenylist...),
		TemporaryTokens:   tokens,
		RateLimit:         policy.RateLimit,
		Audit:             auditConfigToRuntime(policy.Audit),
	}
}

func auditConfigToRuntime(config *session.AuditConfig) *accesspolicy.AuditConfig {
	if config == nil {
		return nil
	}
	return &accesspolicy.AuditConfig{Enabled: config.Enabled}
}

func resolveSecretValue(value, envName, label string, lookupEnv func(string) string) (string, error) {
	if value != "" && envName != "" {
		return "", fmt.Errorf("%s cannot be combined with %sEnv", label, label)
	}
	if envName == "" {
		if value == "" {
			return "", fmt.Errorf("%s is required", label)
		}
		return value, nil
	}
	resolved := lookupEnv(envName)
	if resolved == "" {
		return "", fmt.Errorf("%s environment variable %s is empty or unset", label, envName)
	}
	return resolved, nil
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func temporaryAccessURL(host, token string) string {
	host = strings.TrimSpace(host)
	token = strings.TrimSpace(token)
	if host == "" || token == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/?%s=%s", host, accesspolicy.TemporaryTokenQueryParam, url.QueryEscape(token))
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

func getenv(name string) string {
	return os.Getenv(name)
}
