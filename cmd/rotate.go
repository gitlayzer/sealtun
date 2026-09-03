package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type rotateServerSecretPayload struct {
	TunnelID     string `json:"tunnelId"`
	ServerSecret string `json:"serverSecret"`
	Message      string `json:"message"`
}

var rotateServerSecret bool
var rotateJSON bool

var rotateCmd = &cobra.Command{
	Use:          "rotate [tunnel-id]",
	Short:        "Rotate tunnel secrets",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !rotateServerSecret {
			return fmt.Errorf("choose what to rotate, e.g. --server-secret")
		}
		payload, err := rotateTunnelServerSecret(cmd.Context(), args[0])
		if err != nil && payload == nil {
			return err
		}
		if rotateJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if encodeErr := enc.Encode(payload); encodeErr != nil {
				return encodeErr
			}
			return err
		}
		printRotateServerSecret(cmd, payload)
		return err
	},
}

func init() {
	rootCmd.AddCommand(rotateCmd)
	rotateCmd.Flags().BoolVar(&rotateServerSecret, "server-secret", false, "Rotate the tunnel server secret")
	rotateCmd.Flags().BoolVar(&rotateJSON, "json", false, "Output rotation result as JSON")
}

func rotateTunnelServerSecret(ctx context.Context, tunnelID string) (*rotateServerSecretPayload, error) {
	var payload *rotateServerSecretPayload
	err := withTunnelOperationLock(tunnelID, func() error {
		sess, err := findSessionSyncedLocked(ctx, tunnelID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(sess.Secret) == "" {
			return fmt.Errorf("tunnel %s has no local secret; recreate it before rotating", sess.TunnelID)
		}
		if sess.ConnectionState == session.ConnectionStateStopped {
			return fmt.Errorf("tunnel %s is stopped; run `sealtun start %s` before rotating the server secret", sess.TunnelID, sess.TunnelID)
		}
		if sessionExpired(*sess, nowUTC()) {
			return fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
		}
		newSecret := uuid.New().String()
		mutatedAt := nowUTC().Add(-time.Second)
		if err := updateTunnelServerSecret(ctx, sess, newSecret, mutatedAt); err != nil {
			if serverSecretCommitted(err) {
				payload = &rotateServerSecretPayload{
					TunnelID:     sess.TunnelID,
					ServerSecret: newSecret,
					Message:      "New server secret is shown once and saved locally, but rollout confirmation failed.",
				}
				return err
			}
			return err
		}
		payload = &rotateServerSecretPayload{
			TunnelID:     sess.TunnelID,
			ServerSecret: newSecret,
			Message:      "New server secret is shown once and saved locally.",
		}
		return nil
	})
	if err != nil && payload == nil {
		return nil, err
	}
	return payload, err
}

type committedServerSecretError struct {
	err error
}

func (e committedServerSecretError) Error() string {
	return e.err.Error()
}

func (e committedServerSecretError) Unwrap() error {
	return e.err
}

func serverSecretCommitted(err error) bool {
	var committed committedServerSecretError
	return errors.As(err, &committed)
}

var updateTunnelServerSecret = func(ctx context.Context, sess *session.TunnelSession, newSecret string, mutatedAt time.Time) error {
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return err
	}
	namespacedClient := client.WithNamespace(sess.Namespace)
	hosts, err := namespacedClient.EnsureTunnelWithOptions(ctx, sess.TunnelID, newSecret, sessionProtocol(*sess), sess.LocalPort, k8s.TunnelOptions{
		CustomDomain: sess.CustomDomain,
		SealosHost:   sessionSealosHostForDomain(*sess, namespacedClient.SealosHost(sess.TunnelID)),
		TargetURL:    sess.TargetURL,
		BasicAuth:    basicAuthToK8s(sess.BasicAuth),
		AccessPolicy: accessPolicyToK8s(sess.AccessPolicy),
		Resources:    resourcesToK8s(sess.ResourceConfig),
	})
	if err != nil {
		return fmt.Errorf("rotate remote server secret: %w", err)
	}
	sess.Secret = newSecret
	sess.Host = hosts.PublicHost
	sess.SealosHost = hosts.SealosHost
	sess.CustomDomain = hosts.CustomDomain
	sess.PublicPort = hosts.PublicPort
	if err := session.Update(*sess); err != nil {
		return committedServerSecretError{err: fmt.Errorf("server secret was rotated on the remote, but saving the local session failed: %w", err)}
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, readyTimeout)
	defer cancelReady()
	if err := namespacedClient.WaitForReady(readyCtx, sess.TunnelID); err != nil {
		return committedServerSecretError{err: fmt.Errorf("server secret was rotated, but rollout readiness was not confirmed: %w", err)}
	}
	if err := waitForDaemonSessionAfter(sess.TunnelID, daemonConnectTimeout, mutatedAt); err != nil {
		return committedServerSecretError{err: fmt.Errorf("server secret was rotated, but local daemon reconnection was not confirmed: %w", err)}
	}
	return nil
}

func printRotateServerSecret(cmd *cobra.Command, payload *rotateServerSecretPayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Server Secret Rotated")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  New server secret: %s\n", payload.ServerSecret)
	fmt.Fprintf(out, "  Note: %s\n", payload.Message)
}
