package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/publicauth"
	"github.com/labring/sealtun/pkg/session"
	"github.com/labring/sealtun/pkg/tunnel"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var applyFilePath string
var applyJSON bool
var applyDryRun bool
var applyDryRunFormat string

const applyFileMaxBytes = 1 << 20

var applyCmd = &cobra.Command{
	Use:          "apply -f sealtun.yaml",
	Short:        "Apply declarative Sealtun tunnel configuration",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(applyFilePath) == "" {
			return fmt.Errorf("missing -f/--file")
		}
		if applyDryRunFormat == "diff" {
			if !applyDryRun {
				return fmt.Errorf("--format diff requires --dry-run")
			}
			results, err := runDiff(applyFilePath)
			if err != nil {
				return err
			}
			if applyJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}
			printDiffResults(cmd, results)
			return nil
		}
		if applyDryRunFormat != "" && applyDryRunFormat != "plan" {
			return fmt.Errorf("unsupported --format %q; use plan or diff", applyDryRunFormat)
		}
		results, err := runApply(cmd.Context(), applyFilePath, applyDryRun)
		if err != nil {
			return err
		}
		if applyJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}
		printApplyResults(cmd, results, applyDryRun)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(applyCmd)
	applyCmd.Flags().StringVarP(&applyFilePath, "file", "f", "", "Path to sealtun.yaml")
	applyCmd.Flags().BoolVar(&applyJSON, "json", false, "Output apply results as JSON")
	applyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "Validate and show planned tunnels without changing local or cloud state")
	applyCmd.Flags().StringVar(&applyDryRunFormat, "format", "", "Dry-run output format: plan (default) or diff")
}

// inlineSecretWarnings warns when a YAML tunnel carries plaintext secrets
// inline instead of *Env references, matching the CLI's plaintext-password
// warning for novices who copy examples into shared config files.
func inlineSecretWarnings(item applyTunnel) []string {
	var warnings []string
	if item.BasicAuth != nil {
		if strings.TrimSpace(item.BasicAuth.Credential) != "" || strings.TrimSpace(item.BasicAuth.Password) != "" {
			warnings = append(warnings, "basicAuth contains an inline plaintext password; prefer passwordEnv so the secret is not stored in the YAML file")
		}
	}
	if item.AccessPolicy != nil {
		if strings.TrimSpace(item.AccessPolicy.BearerToken) != "" {
			warnings = append(warnings, "accessPolicy contains an inline plaintext bearer token; prefer bearerTokenEnv")
		}
		for _, link := range item.AccessPolicy.TemporaryLinks {
			if strings.TrimSpace(link.Token) != "" {
				warnings = append(warnings, fmt.Sprintf("temporary link %q contains an inline plaintext token; prefer tokenEnv", link.Name))
			}
		}
	}
	return warnings
}

func runApply(ctx context.Context, path string, dryRun bool) ([]applyResult, error) {
	config, err := loadApplyFile(path)
	if err != nil {
		return nil, err
	}
	return runApplyConfig(ctx, config, dryRun)
}

func runApplyConfig(ctx context.Context, config *applyFile, dryRun bool) ([]applyResult, error) {
	if len(config.Tunnels) == 0 {
		return nil, fmt.Errorf("apply file has no tunnels")
	}
	if err := validateApplyTunnelNames(config.Tunnels); err != nil {
		return nil, err
	}
	if dryRun {
		results := make([]applyResult, 0, len(config.Tunnels))
		for _, item := range config.Tunnels {
			normalized, err := normalizeApplyTunnel(item)
			if err != nil {
				return results, err
			}
			results = append(results, applyResult{
				Name:                        normalized.Name,
				TunnelID:                    normalized.TunnelID,
				Protocol:                    normalized.Protocol,
				LocalPort:                   normalized.LocalPort,
				TargetURL:                   normalized.TargetURL,
				TargetTLSInsecureSkipVerify: targetTLSInsecureSkipVerifyEnabled(normalized.TargetTLS),
				Resources:                   normalized.Resources,
				BasicAuth:                   normalized.BasicAuth != nil && normalized.BasicAuth.Enabled,
				BasicAuthUser:               basicAuthUsername(normalized.BasicAuth),
				AccessPolicy:                normalized.AccessPolicy != nil,
				ExpiresAt:                   normalized.ExpiresAt,
				Status:                      "planned",
				Warnings:                    inlineSecretWarnings(item),
			})
		}
		return results, nil
	}

	authData, err := auth.LoadAuthData()
	if err != nil {
		return nil, fmt.Errorf("not logged in. Please run 'sealtun login' first: %w", err)
	}
	root, err := auth.GetSealosDir()
	if err != nil {
		return nil, err
	}
	kubeconfigPath := filepath.Join(root, "kubeconfig")
	kubeconfig, err := auth.ActiveKubeconfig()
	if err != nil {
		return nil, fmt.Errorf("failed to read kubeconfig: %w", err)
	}
	client, err := k8s.NewClient(kubeconfigPath, authData)
	if err != nil {
		return nil, fmt.Errorf("failed to init k8s client: %w", err)
	}

	tunnelIDs := make([]string, 0, len(config.Tunnels))
	for _, item := range config.Tunnels {
		tunnelID, err := applyTunnelID(item.Name)
		if err != nil {
			return nil, err
		}
		tunnelIDs = append(tunnelIDs, tunnelID)
	}

	results := make([]applyResult, 0, len(config.Tunnels))
	err = withTunnelOperationLocks(tunnelIDs, func() error {
		for _, item := range config.Tunnels {
			result, applyErr := applyOneTunnel(ctx, item, authData, client, kubeconfig, false)
			if applyErr != nil {
				return applyErrorWithRollback(applyErr, rollbackApplyResults(client, results))
			}
			results = append(results, result)
		}
		if err := ensureDaemonRunningFn(); err != nil {
			return applyErrorWithRollback(fmt.Errorf("failed to start local daemon: %w", err), rollbackApplyResults(client, results))
		}
		for _, result := range results {
			if err := waitForDaemonSession(result.TunnelID, daemonConnectTimeout); err != nil {
				return applyErrorWithRollback(err, rollbackApplyResults(client, results))
			}
		}
		return nil
	})
	return results, err
}

func loadApplyFile(path string) (*applyFile, error) {
	file, err := os.Open(path) // #nosec G304 -- apply file path is provided by the user.
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, applyFileMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > applyFileMaxBytes {
		return nil, fmt.Errorf("apply file %s is too large; limit is %d bytes", path, applyFileMaxBytes)
	}
	return loadApplyData(path, data)
}

func loadApplyData(label string, data []byte) (*applyFile, error) {
	if len(data) > applyFileMaxBytes {
		return nil, fmt.Errorf("apply file %s is too large; limit is %d bytes", label, applyFileMaxBytes)
	}
	var config applyFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	if err := validateApplyPortLiterals(label, data); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", label, err)
		}
		return nil, fmt.Errorf("parse %s: multiple YAML documents are not supported", label)
	}
	if config.Version == "" {
		config.Version = "v1"
	}
	if config.Version != "v1" {
		return nil, fmt.Errorf("unsupported apply file version %q", config.Version)
	}
	return &config, nil
}

func validateApplyTunnelNames(items []applyTunnel) error {
	seen := map[string]struct{}{}
	for _, item := range items {
		tunnelID, err := applyTunnelID(item.Name)
		if err != nil {
			return err
		}
		if _, ok := seen[tunnelID]; ok {
			return fmt.Errorf("duplicate tunnel name %q in apply file", item.Name)
		}
		seen[tunnelID] = struct{}{}
		if item.AccessPolicy != nil {
			linkNames := map[string]struct{}{}
			for _, link := range item.AccessPolicy.TemporaryLinks {
				if _, ok := linkNames[link.Name]; ok {
					return fmt.Errorf("tunnel %s: duplicate temporary link name %q in apply file", item.Name, link.Name)
				}
				linkNames[link.Name] = struct{}{}
			}
		}
	}
	return nil
}

// validateApplyPortLiterals rejects plain-scalar port values with leading
// zeros. YAML 1.1 parses them as octal (03000 becomes 1536), which silently
// provisions the tunnel on the wrong port. Quoted or zero-prefixed hex forms
// are unaffected because they fail or parse predictably.
func validateApplyPortLiterals(label string, data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // the strict decoder reports syntax errors with better context
	}
	plainOctalPort := regexp.MustCompile(`^[+-]?0[0-9]+$`)
	var walk func(node *yaml.Node, inTunnels bool) error
	walk = func(node *yaml.Node, inTunnels bool) error {
		if node == nil {
			return nil
		}
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				isPortKey := key.Value == "localPort" || key.Value == "port"
				if inTunnels && isPortKey && value.Kind == yaml.ScalarNode && value.Style == 0 && plainOctalPort.MatchString(value.Value) {
					return fmt.Errorf("parse %s: line %d: %s %q has a leading zero and is parsed as octal by YAML; write the port without the leading zero (e.g. %s)", label, value.Line, key.Value, value.Value, strings.TrimLeft(strings.TrimLeft(value.Value, "+-"), "0"))
				}
				next := inTunnels || key.Value == "tunnels"
				if err := walk(value, next); err != nil {
					return err
				}
			}
			return nil
		}
		for _, child := range node.Content {
			if err := walk(child, inTunnels); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(&doc, false)
}

func applyOneTunnel(ctx context.Context, item applyTunnel, authData *auth.AuthData, client *k8s.Client, kubeconfig string, dryRun bool) (result applyResult, err error) {
	normalized, err := normalizeApplyTunnel(item)
	if err != nil {
		return applyResult{}, err
	}

	result = applyResult{
		Name:                        normalized.Name,
		TunnelID:                    normalized.TunnelID,
		Protocol:                    normalized.Protocol,
		LocalPort:                   normalized.LocalPort,
		TargetURL:                   normalized.TargetURL,
		TargetTLSInsecureSkipVerify: targetTLSInsecureSkipVerifyEnabled(normalized.TargetTLS),
		BasicAuth:                   normalized.BasicAuth != nil && normalized.BasicAuth.Enabled,
		BasicAuthUser:               basicAuthUsername(normalized.BasicAuth),
		AccessPolicy:                normalized.AccessPolicy != nil,
		Resources:                   normalized.Resources,
		ExpiresAt:                   normalized.ExpiresAt,
		Status:                      "planned",
		Warnings:                    inlineSecretWarnings(item),
	}
	secret := uuid.New().String()
	createdAt := ""
	alreadyExisted := false
	var existingSession *session.TunnelSession
	if !dryRun {
		existing, err := session.Get(normalized.TunnelID)
		if err == nil {
			alreadyExisted = true
			currentNamespace := ""
			if client != nil {
				currentNamespace = client.Namespace()
			}
			if err := validateExistingApplySessionScope(*existing, authData, currentNamespace); err != nil {
				return result, err
			}
			if client != nil {
				if err := refreshSessionFromRemoteLocked(ctx, existing); err != nil {
					return result, fmt.Errorf("tunnel %s: sync existing remote state: %w", normalized.TunnelID, err)
				}
			}
			if strings.TrimSpace(existing.Secret) == "" {
				return result, fmt.Errorf("tunnel %s already exists but its local secret is unavailable; stop or cleanup the old session before apply", existing.TunnelID)
			}
			existingSession = existing
			if existing.Secret != "" {
				secret = existing.Secret
			}
			reuseExistingExpiration(&normalized, existing)
			reuseExistingBasicAuthHash(&normalized, existing.BasicAuth)
			createdAt = existing.CreatedAt
		} else if !os.IsNotExist(err) {
			return result, fmt.Errorf("tunnel %s: load existing session: %w", normalized.TunnelID, err)
		}
	}

	result.NewTunnel = !alreadyExisted
	result.Previous = existingSession
	if dryRun {
		return result, nil
	}

	desiredCustomDomain := normalized.CustomDomain
	customDomainVerified := false
	sealosHost := ""
	if existingSession != nil {
		sealosHost = sessionSealosHostForDomain(*existingSession, "")
	}
	if sealosHost == "" && client != nil {
		sealosHost = client.SealosHost(normalized.TunnelID)
	}
	if desiredCustomDomain != "" {
		if verifyErr := requireDomainCNAME(ctx, desiredCustomDomain, sealosHost); verifyErr != nil {
			if alreadyExisted {
				return result, fmt.Errorf("tunnel %s: custom domain DNS must be verified before updating an existing tunnel: %w", normalized.TunnelID, verifyErr)
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("custom domain not attached: %v", verifyErr))
			result.Warnings = append(result.Warnings, fmt.Sprintf("configure CNAME %s -> %s, then run `sealtun domain add %s %s`", desiredCustomDomain, sealosHost, normalized.TunnelID, desiredCustomDomain))
			desiredCustomDomain = ""
		} else {
			customDomainVerified = true
		}
	}
	if client == nil {
		return result, fmt.Errorf("tunnel %s: kubernetes client is unavailable", normalized.TunnelID)
	}

	remoteChanged := false
	defer func() {
		if err == nil || !remoteChanged {
			return
		}
		if result.NewTunnel {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), tunnelCleanupTimeout)
			defer cancel()
			_ = client.CleanupTunnel(cleanupCtx, normalized.TunnelID)
			_ = session.Delete(normalized.TunnelID)
			return
		}
		if existingSession != nil {
			if rollbackErr := rollbackExistingApplyTunnel(client, *existingSession); rollbackErr != nil {
				err = fmt.Errorf("%w; rollback of existing tunnel failed: %v", err, rollbackErr)
			}
		}
	}()

	options := k8s.TunnelOptions{}
	options.BasicAuth = basicAuthToK8s(normalized.BasicAuth)
	options.AccessPolicy = accessPolicyToK8s(normalized.AccessPolicy)
	options.TargetURL = normalized.TargetURL
	options.Resources = resourcesToK8s(normalized.Resources)
	if customDomainVerified {
		options.CustomDomain = desiredCustomDomain
		options.SealosHost = sealosHost
	}
	hosts, err := client.EnsureTunnelWithOptions(ctx, normalized.TunnelID, secret, normalized.Protocol, normalized.LocalPort, options)
	if err != nil {
		if alreadyExisted && existingSession != nil {
			if rollbackErr := rollbackExistingApplyTunnel(client, *existingSession); rollbackErr != nil {
				return result, fmt.Errorf("tunnel %s: provision on Sealos: %w; rollback of existing tunnel failed: %v", normalized.TunnelID, err, rollbackErr)
			}
		}
		return result, fmt.Errorf("tunnel %s: provision on Sealos: %w", normalized.TunnelID, err)
	}
	remoteChanged = true

	if alreadyExisted && existingSession != nil && existingSession.CustomDomain != "" && desiredCustomDomain == "" {
		clearedHosts, clearErr := client.WithNamespace(client.Namespace()).ClearCustomDomain(ctx, normalized.TunnelID, hosts.SealosHost)
		if clearErr != nil {
			return result, fmt.Errorf("tunnel %s: clear custom domain: %w", normalized.TunnelID, clearErr)
		}
		hosts = clearedHosts
	}

	waitCtx, cancel := context.WithTimeout(ctx, normalized.ReadyTimeout)
	err = client.WaitForReady(waitCtx, normalized.TunnelID)
	cancel()
	if err != nil {
		return result, fmt.Errorf("tunnel %s: wait for ready: %w", normalized.TunnelID, err)
	}

	record := buildApplySessionRecord(normalized, authData, client.Namespace(), kubeconfig, secret, hosts, createdAt)
	if err := session.Save(record); err != nil {
		return result, fmt.Errorf("tunnel %s: save session: %w", normalized.TunnelID, err)
	}

	if hosts.CustomDomain != "" && normalized.WaitDomain {
		verify, waitErr := waitForDomainReady(ctx, session.TunnelSession{
			TunnelID:     normalized.TunnelID,
			Host:         hosts.PublicHost,
			SealosHost:   hosts.SealosHost,
			CustomDomain: hosts.CustomDomain,
			Namespace:    client.Namespace(),
			Kubeconfig:   kubeconfig,
			Region:       authData.Region,
		}, normalized.DomainTimeout)
		if verify != nil && !verify.Ready {
			result.Warnings = append(result.Warnings, "custom domain is not fully ready yet")
		}
		if waitErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("custom domain readiness wait failed: %v", waitErr))
		}
	}

	result.Host = hosts.PublicHost
	result.SealosHost = hosts.SealosHost
	result.CustomDomain = hosts.CustomDomain
	result.PublicPort = hosts.PublicPort
	result.TargetURL = normalized.TargetURL
	result.TargetTLSInsecureSkipVerify = targetTLSInsecureSkipVerifyEnabled(normalized.TargetTLS)
	result.BasicAuth = normalized.BasicAuth != nil && normalized.BasicAuth.Enabled
	result.BasicAuthUser = basicAuthUsername(normalized.BasicAuth)
	result.AccessPolicy = normalized.AccessPolicy != nil
	result.ExpiresAt = normalized.ExpiresAt
	result.TemporaryURLs = applyTemporaryAccessURLs(hosts.PublicHost, item.AccessPolicy)
	result.Status = "applied"
	remoteChanged = false
	return result, nil
}

func buildApplySessionRecord(normalized normalizedApplyTunnel, authData *auth.AuthData, namespace, kubeconfig, secret string, hosts k8s.TunnelHosts, createdAt string) session.TunnelSession {
	region := ""
	if authData != nil {
		region = authData.Region
	}
	return session.TunnelSession{
		TunnelID:        normalized.TunnelID,
		Region:          region,
		Namespace:       namespace,
		Kubeconfig:      kubeconfig,
		Protocol:        normalized.Protocol,
		Host:            hosts.PublicHost,
		SealosHost:      hosts.SealosHost,
		CustomDomain:    hosts.CustomDomain,
		PublicPort:      hosts.PublicPort,
		LocalPort:       normalized.LocalPort,
		TargetURL:       normalized.TargetURL,
		TargetTLS:       normalized.TargetTLS,
		Secret:          secret,
		BasicAuth:       normalized.BasicAuth,
		AccessPolicy:    normalized.AccessPolicy,
		ResourceConfig:  normalized.Resources,
		TTL:             normalized.TTL,
		ExpiresAt:       normalized.ExpiresAt,
		Mode:            "daemon",
		PID:             0,
		ConnectionState: session.ConnectionStatePending,
		CreatedAt:       createdAt,
		Resources:       []string{fmt.Sprintf("sealtun-%s", normalized.TunnelID)},
	}
}

func validateExistingApplySessionScope(existing session.TunnelSession, authData *auth.AuthData, currentNamespace string) error {
	currentRegion := ""
	if authData != nil {
		currentRegion = authData.Region
	}
	if existing.Region == "" || currentRegion == "" {
		return fmt.Errorf("tunnel %s already exists but region metadata is incomplete; run `sealtun inspect %s` and clean it up before apply", existing.TunnelID, existing.TunnelID)
	}
	if existing.Region != currentRegion {
		return fmt.Errorf("tunnel %s already belongs to region %s; current region is %s", existing.TunnelID, existing.Region, currentRegion)
	}
	if currentNamespace != "" {
		if existing.Namespace == "" {
			return fmt.Errorf("tunnel %s already exists but namespace metadata is incomplete; clean it up before apply", existing.TunnelID)
		}
		if existing.Namespace != currentNamespace {
			return fmt.Errorf("tunnel %s already belongs to namespace %s; current namespace is %s", existing.TunnelID, existing.Namespace, currentNamespace)
		}
	}
	return nil
}

func restoreExistingApplyTunnel(client *k8s.Client, previous session.TunnelSession) error {
	if client == nil {
		return nil
	}
	if strings.TrimSpace(previous.Secret) == "" || strings.TrimSpace(previous.LocalPort) == "" {
		return fmt.Errorf("previous session %s is missing secret or local port", previous.TunnelID)
	}
	protocol := previous.Protocol
	if protocol == "" {
		protocol = "https"
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), tunnelCleanupTimeout)
	defer cancel()
	_, err := client.WithNamespace(previous.Namespace).EnsureTunnelWithOptions(cleanupCtx, previous.TunnelID, previous.Secret, protocol, previous.LocalPort, k8s.TunnelOptions{
		CustomDomain: previous.CustomDomain,
		SealosHost:   previous.SealosHost,
		TargetURL:    previous.TargetURL,
		BasicAuth:    basicAuthToK8s(previous.BasicAuth),
		AccessPolicy: accessPolicyToK8s(previous.AccessPolicy),
		Resources:    resourcesToK8s(previous.ResourceConfig),
	})
	return err
}

func rollbackExistingApplyTunnel(client *k8s.Client, previous session.TunnelSession) error {
	var firstErr error
	if err := restoreExistingApplyTunnel(client, previous); err != nil {
		firstErr = err
	}
	if err := session.Save(previous); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func rollbackApplyResults(client *k8s.Client, results []applyResult) error {
	var rollbackErrors []error
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if result.TunnelID == "" {
			continue
		}
		if result.NewTunnel {
			if client != nil {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), tunnelCleanupTimeout)
				if err := client.CleanupTunnel(cleanupCtx, result.TunnelID); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("cleanup tunnel %s: %w", result.TunnelID, err))
				}
				cancel()
			}
			if err := session.Delete(result.TunnelID); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("delete local session %s: %w", result.TunnelID, err))
			}
			continue
		}
		if result.Previous != nil {
			if err := rollbackExistingApplyTunnel(client, *result.Previous); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore tunnel %s: %w", result.TunnelID, err))
			}
		}
	}
	return errors.Join(rollbackErrors...)
}

func applyErrorWithRollback(applyErr, rollbackErr error) error {
	if rollbackErr == nil {
		return applyErr
	}
	return errors.Join(applyErr, fmt.Errorf("rollback failed: %w", rollbackErr))
}

func normalizeApplyTunnel(item applyTunnel) (normalizedApplyTunnel, error) {
	tunnelID, err := applyTunnelID(item.Name)
	if err != nil {
		return normalizedApplyTunnel{}, err
	}
	localPort, targetURL, err := normalizeApplyTarget(item)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: %w", tunnelID, err)
	}
	protocol := item.Protocol
	if protocol == "" {
		protocol = "https"
	}
	if err := validateProtocol(protocol); err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: %w", tunnelID, err)
	}
	protocol = tunnelprotocol.Normalize(protocol)
	customDomain, err := validateCustomDomain(item.Domain)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: %w", tunnelID, err)
	}
	effectiveReadyTimeout, err := parseApplyDuration(item.ReadyTimeout, readyTimeout)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s readyTimeout: %w", tunnelID, err)
	}
	effectiveDomainTimeout, err := parseApplyDuration(item.DomainTimeout, domainWaitTimeout)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s domainTimeout: %w", tunnelID, err)
	}
	basicAuth, basicAuthPass, err := resolveApplyBasicAuth(item.BasicAuth)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: %w", tunnelID, err)
	}
	now := nowUTC()
	accessPolicy, err := resolveApplyAccessPolicy(item.AccessPolicy, now, getenv)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s accessPolicy: %w", tunnelID, err)
	}
	targetTLS, err := resolveApplyTargetTLS(item.Target, item.TargetTLS)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: %w", tunnelID, err)
	}
	resourceConfig, err := resolveApplyResources(item.Resources)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s resources: %w", tunnelID, err)
	}
	if !tunnelprotocol.IsHTTP(protocol) {
		if strings.TrimSpace(item.Target) != "" {
			return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: target is only supported for https tunnels", tunnelID)
		}
		if targetTLS != nil {
			return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: targetTls is only supported for https target tunnels", tunnelID)
		}
		if customDomain != "" || item.WaitDomain {
			return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: domain and waitDomain are only supported for https tunnels", tunnelID)
		}
		if basicAuth != nil {
			return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: basicAuth is only supported for https tunnels", tunnelID)
		}
		if accessPolicy != nil {
			return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s: accessPolicy is only supported for https tunnels", tunnelID)
		}
	}
	ttl := strings.TrimSpace(item.TTL)
	expiresAt, err := resolveApplyTunnelExpiresAt(ttl, now)
	if err != nil {
		return normalizedApplyTunnel{}, fmt.Errorf("tunnel %s ttl: %w", tunnelID, err)
	}
	return normalizedApplyTunnel{
		Name:          item.Name,
		TunnelID:      tunnelID,
		LocalPort:     localPort,
		TargetURL:     targetURL,
		Protocol:      protocol,
		CustomDomain:  customDomain,
		BasicAuth:     basicAuth,
		BasicAuthPass: basicAuthPass,
		TargetTLS:     targetTLS,
		Resources:     resourceConfig,
		AccessPolicy:  accessPolicy,
		TTL:           ttl,
		ExpiresAt:     expiresAt,
		WaitDomain:    item.WaitDomain,
		ReadyTimeout:  effectiveReadyTimeout,
		DomainTimeout: effectiveDomainTimeout,
	}, nil
}

func normalizeApplyTarget(item applyTunnel) (string, string, error) {
	port := item.LocalPort
	if port == 0 {
		port = item.Port
	}
	localPort := ""
	if port != 0 {
		localPort = strconv.Itoa(port)
		if err := validateLocalPort(localPort); err != nil {
			return "", "", err
		}
	}
	target, err := tunnel.TargetFor(localPort, item.Target)
	if err != nil {
		return "", "", err
	}
	if localPort != "" && strings.TrimSpace(item.Target) != "" && localPort != target.Port {
		return "", "", fmt.Errorf("localPort %s does not match target port %s; omit localPort or use the same port", localPort, target.Port)
	}
	if localPort == "" {
		localPort = target.Port
	}
	return localPort, target.URL, nil
}

func resolveApplyTargetTLS(rawTarget string, config *applyTargetTLS) (*session.TargetTLSConfig, error) {
	if config == nil || !config.InsecureSkipVerify {
		return nil, nil
	}
	if strings.TrimSpace(rawTarget) == "" {
		return nil, fmt.Errorf("targetTls.insecureSkipVerify requires target with an https URL")
	}
	if err := validateTargetTLSOptions(rawTarget, true); err != nil {
		return nil, err
	}
	return sessionTargetTLSConfig(true), nil
}

func resolveApplyResources(config *applyResources) (*session.ResourceConfig, error) {
	if config == nil {
		return resourcesFromK8s(k8s.DefaultResourceConfig()), nil
	}
	input := session.ResourceConfig{}
	if config.Requests != nil {
		input.Requests = &session.ResourceValues{
			CPU:    config.Requests.CPU,
			Memory: config.Requests.Memory,
		}
	}
	if config.Limits != nil {
		input.Limits = &session.ResourceValues{
			CPU:    config.Limits.CPU,
			Memory: config.Limits.Memory,
		}
	}
	return normalizeResourceConfig(&input)
}

func resolveApplyTunnelExpiresAt(ttl string, now time.Time) (string, error) {
	if strings.TrimSpace(ttl) == "" {
		return "", nil
	}
	duration, err := time.ParseDuration(ttl)
	if err != nil {
		return "", err
	}
	if duration <= 0 {
		return "", fmt.Errorf("must be greater than 0")
	}
	return now.Add(duration).UTC().Format(time.RFC3339), nil
}

func resolveApplyBasicAuth(config *applyBasicAuth) (*session.BasicAuthConfig, string, error) {
	if config == nil {
		return nil, "", nil
	}
	input := basicAuthInput{
		Credential:  config.Credential,
		Username:    config.Username,
		Password:    config.Password,
		PasswordEnv: config.PasswordEnv,
	}
	username, password, ok, err := resolveBasicAuthCredentials(input, os.Getenv)
	if err != nil || !ok {
		return nil, "", err
	}
	basicAuth, err := newSessionBasicAuth(username, password)
	if err != nil {
		return nil, "", err
	}
	return basicAuth, password, nil
}

func reuseExistingBasicAuthHash(normalized *normalizedApplyTunnel, existing *session.BasicAuthConfig) {
	if normalized == nil || normalized.BasicAuth == nil || existing == nil || !existing.Enabled {
		return
	}
	existingHash := basicAuthPasswordHash(existing)
	if existingHash == "" || normalized.BasicAuthPass == "" || existing.Username != normalized.BasicAuth.Username {
		return
	}
	if !publicauth.Check(publicauth.BasicAuth{Username: existing.Username, PasswordHash: existingHash}, normalized.BasicAuth.Username, normalized.BasicAuthPass) {
		return
	}
	if existing.PasswordHash == "" {
		normalized.BasicAuth.PasswordSHA256 = ""
		return
	}
	normalized.BasicAuth.PasswordHash = existingHash
	normalized.BasicAuth.PasswordSHA256 = ""
}

func reuseExistingExpiration(normalized *normalizedApplyTunnel, existing *session.TunnelSession) {
	if normalized == nil || existing == nil {
		return
	}
	if normalized.TTL != "" && existing.TTL == normalized.TTL && !sessionExpired(*existing, nowUTC()) && existing.ExpiresAt != "" {
		normalized.ExpiresAt = existing.ExpiresAt
	}
	reuseExistingTemporaryTokenExpirations(normalized.AccessPolicy, existing.AccessPolicy)
}

func reuseExistingTemporaryTokenExpirations(desired, existing *session.AccessPolicy) {
	if desired == nil || existing == nil || len(desired.TemporaryTokens) == 0 || len(existing.TemporaryTokens) == 0 {
		return
	}
	existingByKey := map[string]session.TemporaryToken{}
	for _, token := range existing.TemporaryTokens {
		if token.TTL == "" || token.ExpiresAt == "" {
			continue
		}
		if expiresAt, err := time.Parse(time.RFC3339, token.ExpiresAt); err != nil || !nowUTC().Before(expiresAt) {
			continue
		}
		existingByKey[temporaryTokenIdentity(token)] = token
	}
	for i := range desired.TemporaryTokens {
		token := &desired.TemporaryTokens[i]
		if token.TTL == "" {
			continue
		}
		if existingToken, ok := existingByKey[temporaryTokenIdentity(*token)]; ok {
			token.ExpiresAt = existingToken.ExpiresAt
		}
	}
}

func temporaryTokenIdentity(token session.TemporaryToken) string {
	return strings.Join([]string{token.Name, token.TokenHash, token.TTL}, "\x00")
}

func basicAuthUsername(config *session.BasicAuthConfig) string {
	if config == nil || !config.Enabled {
		return ""
	}
	return config.Username
}

func applyTunnelID(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("tunnel name is required")
	}
	if name != strings.ToLower(name) || !applyNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid tunnel name %q: use lowercase DNS-compatible names, e.g. web or api-dev", name)
	}
	if strings.HasPrefix(name, "mesh-") {
		return "", fmt.Errorf("invalid tunnel name %q: the mesh- prefix is reserved for Sealtun Mesh resources", name)
	}
	return name, nil
}

func parseApplyDuration(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, fmt.Errorf("must be greater than 0")
	}
	return duration, nil
}

func printApplyResults(cmd *cobra.Command, results []applyResult, dryRun bool) {
	out := cmd.OutOrStdout()
	if dryRun {
		fmt.Fprintln(out, "Sealtun Apply Plan")
	} else {
		fmt.Fprintln(out, "Sealtun Apply Results")
	}
	for _, result := range results {
		endpoint := endpointDisplay(result.Protocol, result.Host, result.SealosHost, result.PublicPort)
		fmt.Fprintf(out, "  - %s (%s): %s %s", result.Name, result.TunnelID, result.Status, valueOr(result.TargetURL, defaultLocalTargetURL(result.LocalPort)))
		if result.Protocol == tunnelprotocol.SSH && endpoint.Command != "" {
			fmt.Fprintf(out, " -> %s", endpoint.Command)
		} else if result.Protocol == tunnelprotocol.TCP && endpoint.Port != 0 {
			fmt.Fprintf(out, " -> %s", endpointLabel(result.Protocol, result.Host, result.SealosHost, result.PublicPort))
		} else if endpoint.URL != "" {
			fmt.Fprintf(out, " -> %s", endpoint.URL)
		}
		fmt.Fprintln(out)
		if result.Protocol != "" {
			fmt.Fprintf(out, "    Protocol: %s\n", result.Protocol)
		}
		if result.TargetURL != "" {
			fmt.Fprintf(out, "    Target: %s\n", result.TargetURL)
		}
		if result.SealosHost != "" {
			fmt.Fprintf(out, "    Sealos host: %s\n", result.SealosHost)
		}
		if result.CustomDomain != "" {
			fmt.Fprintf(out, "    Custom domain: %s\n", result.CustomDomain)
		}
		if result.Protocol == tunnelprotocol.SSH {
			if endpoint.Host != "" {
				fmt.Fprintf(out, "    Public SSH host: %s\n", endpoint.Host)
			}
			if endpoint.Port != 0 {
				fmt.Fprintf(out, "    Public SSH port: %d\n", endpoint.Port)
			}
			if endpoint.Command != "" {
				fmt.Fprintf(out, "    SSH command: %s\n", endpoint.Command)
			}
		} else if result.Protocol == tunnelprotocol.TCP {
			if endpoint.Host != "" {
				fmt.Fprintf(out, "    Public TCP host: %s\n", endpoint.Host)
			}
			if endpoint.Port != 0 {
				fmt.Fprintf(out, "    Public TCP port: %d\n", endpoint.Port)
				fmt.Fprintf(out, "    Public TCP endpoint: %s\n", endpointLabel(result.Protocol, result.Host, result.SealosHost, result.PublicPort))
			}
		} else if endpoint.URL != "" {
			fmt.Fprintf(out, "    Public URL: %s\n", endpoint.URL)
		}
		if result.BasicAuth {
			fmt.Fprintf(out, "    Basic Auth: enabled")
			if result.BasicAuthUser != "" {
				fmt.Fprintf(out, " (user: %s)", result.BasicAuthUser)
			}
			fmt.Fprintln(out)
		}
		if result.AccessPolicy {
			fmt.Fprintln(out, "    Access policy: enabled")
		}
		if result.ExpiresAt != "" {
			fmt.Fprintf(out, "    Expires at: %s\n", result.ExpiresAt)
		}
		for _, link := range result.TemporaryURLs {
			fmt.Fprintf(out, "    Temporary access URL: %s\n", link)
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(out, "    Warning: %s\n", warning)
		}
	}
}

func applyTemporaryAccessURLs(host string, config *applyAccessPolicy) []string {
	if config == nil || host == "" {
		return nil
	}
	links := make([]string, 0, len(config.TemporaryLinks))
	for _, item := range config.TemporaryLinks {
		if item.Token == "" {
			continue
		}
		if link := temporaryAccessURL(host, item.Token); link != "" {
			links = append(links, link)
		}
	}
	return links
}

func runDiff(path string) ([]diffResult, error) {
	config, err := loadApplyFile(path)
	if err != nil {
		return nil, err
	}
	return runDiffConfig(config)
}

func runDiffConfig(config *applyFile) ([]diffResult, error) {
	return runDiffConfigWithSessionLookup(config, session.Get)
}

func runDiffConfigWithSessionLookup(config *applyFile, lookup func(string) (*session.TunnelSession, error)) ([]diffResult, error) {
	if len(config.Tunnels) == 0 {
		return nil, fmt.Errorf("apply file has no tunnels")
	}
	if err := validateApplyTunnelNames(config.Tunnels); err != nil {
		return nil, err
	}
	results := make([]diffResult, 0, len(config.Tunnels))
	for _, item := range config.Tunnels {
		normalized, err := normalizeApplyTunnel(item)
		if err != nil {
			return results, err
		}
		result := diffResult{
			Name:                        normalized.Name,
			TunnelID:                    normalized.TunnelID,
			DesiredPort:                 normalized.LocalPort,
			DesiredTarget:               normalized.TargetURL,
			DesiredHost:                 normalized.CustomDomain,
			ExpiresAt:                   normalized.ExpiresAt,
			TargetTLSInsecureSkipVerify: targetTLSInsecureSkipVerifyEnabled(normalized.TargetTLS),
			DesiredResources:            normalized.Resources,
			AccessPolicy:                normalized.AccessPolicy != nil,
			BasicAuth:                   normalized.BasicAuth != nil && normalized.BasicAuth.Enabled,
		}
		existing, err := lookup(normalized.TunnelID)
		if err == nil {
			reuseExistingExpiration(&normalized, existing)
			result.ExpiresAt = normalized.ExpiresAt
			result.AccessPolicy = normalized.AccessPolicy != nil
			result.CurrentPort = existing.LocalPort
			result.CurrentTarget = sessionTargetURL(*existing)
			result.CurrentHost = existing.CustomDomain
			result.CurrentTargetTLSInsecureSkipVerify = targetTLSInsecureSkipVerifyEnabled(existing.TargetTLS)
			result.CurrentResources = effectiveSessionResourceConfig(existing.ResourceConfig)
			result.Action = "no-op"
			if existing.LocalPort != normalized.LocalPort {
				result.Changes = append(result.Changes, fmt.Sprintf("localPort: %s -> %s", valueOr(existing.LocalPort, "-"), normalized.LocalPort))
			}
			if sessionTargetURL(*existing) != normalized.TargetURL {
				result.Changes = append(result.Changes, fmt.Sprintf("target: %s -> %s", valueOr(sessionTargetURL(*existing), "-"), normalized.TargetURL))
			}
			if targetTLSInsecureSkipVerifyEnabled(existing.TargetTLS) != targetTLSInsecureSkipVerifyEnabled(normalized.TargetTLS) {
				result.Changes = append(result.Changes, fmt.Sprintf("targetTls.insecureSkipVerify: %t -> %t", targetTLSInsecureSkipVerifyEnabled(existing.TargetTLS), targetTLSInsecureSkipVerifyEnabled(normalized.TargetTLS)))
			}
			if valueOr(existing.Protocol, "https") != normalized.Protocol {
				result.Changes = append(result.Changes, fmt.Sprintf("protocol: %s -> %s", valueOr(existing.Protocol, "-"), normalized.Protocol))
			}
			if existing.CustomDomain != normalized.CustomDomain {
				result.Changes = append(result.Changes, fmt.Sprintf("domain: %s -> %s", valueOr(existing.CustomDomain, "-"), valueOr(normalized.CustomDomain, "-")))
			}
			if basicAuthChanged(existing.BasicAuth, normalized.BasicAuth, normalized.BasicAuthPass) {
				result.Changes = append(result.Changes, "basicAuth")
			}
			if accessPolicyChanged(existing.AccessPolicy, normalized.AccessPolicy) {
				result.Changes = append(result.Changes, "accessPolicy")
			}
			if resourceConfigChanged(existing.ResourceConfig, normalized.Resources) {
				result.Changes = append(result.Changes, "resources")
			}
			if existing.ExpiresAt != normalized.ExpiresAt {
				result.Changes = append(result.Changes, fmt.Sprintf("ttl/expiresAt: %s -> %s", valueOr(existing.ExpiresAt, "-"), valueOr(normalized.ExpiresAt, "-")))
			}
			if len(result.Changes) > 0 {
				result.Action = "update"
			}
			results = append(results, result)
			continue
		}
		if diffTreatsMissingSessionAsCreate(err) {
			result.Action = "create"
			result.Changes = append(result.Changes, "create tunnel")
			results = append(results, result)
			continue
		}
		return results, fmt.Errorf("tunnel %s: load existing session: %w", normalized.TunnelID, err)
	}
	return results, nil
}

func diffTreatsMissingSessionAsCreate(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	return strings.Contains(err.Error(), "config directory") && strings.Contains(err.Error(), "no such file or directory")
}

func accessPolicyChanged(current, desired *session.AccessPolicy) bool {
	currentJSON, _ := json.Marshal(accessPolicyToRuntime(current))
	desiredJSON, _ := json.Marshal(accessPolicyToRuntime(desired))
	return string(currentJSON) != string(desiredJSON)
}

func basicAuthChanged(current, desired *session.BasicAuthConfig, desiredPassword string) bool {
	currentEnabled := current != nil && current.Enabled
	desiredEnabled := desired != nil && desired.Enabled
	if currentEnabled != desiredEnabled {
		return true
	}
	if !currentEnabled && !desiredEnabled {
		return false
	}
	if basicAuthUsername(current) != basicAuthUsername(desired) {
		return true
	}
	if desiredPassword == "" {
		return basicAuthPasswordHash(current) != basicAuthPasswordHash(desired)
	}
	return !publicauth.Check(publicauth.BasicAuth{
		Username:     current.Username,
		PasswordHash: basicAuthPasswordHash(current),
	}, desired.Username, desiredPassword)
}

func printDiffResults(cmd *cobra.Command, results []diffResult) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Diff")
	for _, result := range results {
		fmt.Fprintf(out, "  - %s (%s): %s", result.Name, result.TunnelID, result.Action)
		if result.DesiredTarget != "" {
			fmt.Fprintf(out, " %s", result.DesiredTarget)
		}
		fmt.Fprintln(out)
		for _, change := range result.Changes {
			fmt.Fprintf(out, "    ~ %s\n", change)
		}
		if result.AccessPolicy {
			fmt.Fprintln(out, "    Access policy: enabled")
		}
		if result.BasicAuth {
			fmt.Fprintln(out, "    Basic Auth: enabled")
		}
		if result.TargetTLSInsecureSkipVerify {
			fmt.Fprintln(out, "    Target TLS: insecure certificate verification disabled")
		}
		if result.ExpiresAt != "" {
			fmt.Fprintf(out, "    Expires at: %s\n", result.ExpiresAt)
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(out, "    Warning: %s\n", warning)
		}
	}
}
