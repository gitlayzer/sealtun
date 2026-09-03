package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/session"
	"github.com/labring/sealtun/pkg/tunnel"
	"github.com/spf13/cobra"
)

type managedTunnel struct {
	cancel      context.CancelFunc
	done        chan struct{}
	fingerprint string
}

var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Run the local Sealtun background agent",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), signalCleanupSignals()...)
		defer stop()

		releaseRuntime, err := daemonstate.AcquireRuntimeLock()
		if err != nil {
			return fmt.Errorf("another sealtun daemon appears to be running")
		}
		defer releaseRuntime()

		if err := daemonstate.SaveState(os.Getpid()); err != nil {
			return fmt.Errorf("save daemon state: %w", err)
		}
		stopHeartbeat := startDaemonHeartbeat(ctx, runDaemonHeartbeat)
		defer func() {
			stopHeartbeat()
			_ = daemonstate.DeleteStateForPID(os.Getpid())
		}()

		managed := map[string]*managedTunnel{}
		var mu sync.Mutex
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		reconcile := func() error {
			sessions, err := session.List()
			if err != nil {
				return err
			}

			desired := map[string]session.TunnelSession{}
			for _, sess := range sessions {
				if sess.Mode != "daemon" {
					continue
				}
				if sessionExpired(sess, time.Now()) {
					latest, removed, err := cleanupExpiredDaemonSession(ctx, sess.TunnelID)
					if err != nil {
						fmt.Printf("[!] expired tunnel %s cleanup failed: %v\n", sess.TunnelID, err)
						continue
					}
					if removed {
						fmt.Printf("[+] expired tunnel %s cleaned up\n", sess.TunnelID)
						continue
					}
					if latest == nil {
						continue
					}
					sess = *latest
					if sess.Mode != "daemon" {
						continue
					}
				}
				if sess.ConnectionState == session.ConnectionStateStopped {
					continue
				}
				desired[sess.TunnelID] = sess
			}

			reconcileDaemonWorkers(ctx, &mu, managed, desired, runDaemonTunnel)

			return nil
		}

		if err := reconcile(); err != nil {
			return fmt.Errorf("initial daemon reconcile: %w", err)
		}

		for {
			select {
			case <-ctx.Done():
				mu.Lock()
				for _, mt := range managed {
					mt.cancel()
				}
				mu.Unlock()
				return nil
			case <-ticker.C:
				if err := reconcile(); err != nil {
					fmt.Printf("[!] daemon reconcile failed: %v\n", err)
				}
			}
		}
	},
}

func cleanupExpiredDaemonSession(ctx context.Context, tunnelID string) (latest *session.TunnelSession, removed bool, err error) {
	err = withTunnelOperationLockContext(ctx, tunnelID, func() error {
		current, getErr := session.Get(tunnelID)
		if os.IsNotExist(getErr) {
			return nil
		}
		if getErr != nil {
			return getErr
		}
		if current.Mode != "daemon" || !sessionExpired(*current, time.Now()) {
			latest = current
			return nil
		}

		cleanupCtx, cancel := context.WithTimeout(ctx, tunnelCleanupTimeout)
		defer cancel()
		if cleanupErr := cleanupSessionResources(cleanupCtx, *current); cleanupErr != nil {
			return cleanupErr
		}
		if deleteErr := session.Delete(tunnelID); deleteErr != nil && !os.IsNotExist(deleteErr) {
			return fmt.Errorf("delete local session: %w", deleteErr)
		}
		removed = true
		return nil
	})
	return latest, removed, err
}

func reconcileDaemonWorkers(
	ctx context.Context,
	mu *sync.Mutex,
	managed map[string]*managedTunnel,
	desired map[string]session.TunnelSession,
	run func(context.Context, session.TunnelSession),
) {
	mu.Lock()
	defer mu.Unlock()

	for tunnelID, mt := range managed {
		select {
		case <-mt.done:
			delete(managed, tunnelID)
		default:
		}
	}

	for tunnelID, mt := range managed {
		desiredSession, ok := desired[tunnelID]
		if !ok || mt.fingerprint != daemonTunnelFingerprint(desiredSession) {
			// Keep the worker registered until it exits. This prevents a replacement
			// connection from racing the canceled worker's final session update.
			mt.cancel()
		}
	}

	for tunnelID, sess := range desired {
		if _, ok := managed[tunnelID]; ok {
			continue
		}

		tunnelCtx, cancel := context.WithCancel(ctx)
		mt := &managedTunnel{
			cancel:      cancel,
			done:        make(chan struct{}),
			fingerprint: daemonTunnelFingerprint(sess),
		}
		managed[tunnelID] = mt

		go func(sess session.TunnelSession, mt *managedTunnel) {
			defer func() {
				close(mt.done)
				mu.Lock()
				if managed[sess.TunnelID] == mt {
					delete(managed, sess.TunnelID)
				}
				mu.Unlock()
			}()
			run(tunnelCtx, sess)
		}(sess, mt)
	}
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}

func daemonTunnelFingerprint(sess session.TunnelSession) string {
	basicAuthEnabled := "false"
	basicAuthUsername := ""
	basicAuthHash := ""
	if sess.BasicAuth != nil && sess.BasicAuth.Enabled {
		basicAuthEnabled = "true"
		basicAuthUsername = sess.BasicAuth.Username
		basicAuthHash = basicAuthPasswordHash(sess.BasicAuth)
	}
	return strings.Join([]string{
		sess.TunnelID,
		sessionControlHost(sess),
		sess.LocalPort,
		sess.TargetURL,
		fmt.Sprint(targetTLSInsecureSkipVerifyEnabled(sess.TargetTLS)),
		sess.Protocol,
		sess.Secret,
		basicAuthEnabled,
		basicAuthUsername,
		basicAuthHash,
		daemonAccessPolicyFingerprint(sess.AccessPolicy),
		sess.TTL,
		sess.ExpiresAt,
	}, "\x00")
}

func daemonAccessPolicyFingerprint(policy *session.AccessPolicy) string {
	if policy == nil {
		return ""
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return ""
	}
	return string(data)
}

func runDaemonHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := daemonstate.TouchStateForPID(os.Getpid()); err != nil {
				fmt.Printf("[!] daemon heartbeat failed: %v\n", err)
			}
		}
	}
}

func startDaemonHeartbeat(parent context.Context, run func(context.Context)) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

func runDaemonTunnel(ctx context.Context, sess session.TunnelSession) {
	reconnectTimer := time.NewTimer(time.Hour)
	if !reconnectTimer.Stop() {
		<-reconnectTimer.C
	}
	defer reconnectTimer.Stop()

	for {
		current, err := session.Get(sess.TunnelID)
		if err != nil {
			return
		}
		if current.ConnectionState == session.ConnectionStateStopped {
			return
		}
		if current.Secret == "" {
			current.Mode = "daemon"
			current.PID = 0
			current.ConnectionState = session.ConnectionStateStopped
			current.LastError = "session secret is unavailable; login or recreate the tunnel"
			if err := session.Update(*current); err != nil && !os.IsNotExist(err) {
				fmt.Printf("[!] failed to stop session %s with missing secret: %v\n", current.TunnelID, err)
			}
			return
		}

		// Write the connecting state via CAS so a concurrent `sealtun stop`
		// (which writes Stopped under the tunnel lock) is never overwritten by
		// a stale read-modify-write from this worker.
		_, err = session.UpdateAtomic(sess.TunnelID, func(latest *session.TunnelSession) (bool, error) {
			if latest.ConnectionState == session.ConnectionStateStopped {
				return false, nil
			}
			latest.Mode = "daemon"
			latest.PID = os.Getpid()
			latest.ConnectionState = session.ConnectionStateConnecting
			latest.LastError = ""
			return true, nil
		})
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			fmt.Printf("[!] failed to refresh session %s: %v\n", sess.TunnelID, err)
		}

		controlHost, hostErr := normalizePublicHostname(sessionControlHost(*current))
		if hostErr != nil {
			err = fmt.Errorf("invalid tunnel control host: %w", hostErr)
		} else {
			wsURL := fmt.Sprintf("wss://%s/_sealtun/ws", controlHost)
			err = tunnel.DialServerAndServeTargetWithOptions(ctx, wsURL, current.Secret, current.LocalPort, current.TargetURL, current.Protocol, targetOptionsForSession(*current), func() {
				_, saveErr := session.UpdateAtomic(sess.TunnelID, func(latest *session.TunnelSession) (bool, error) {
					if shouldPreserveStoppedSession(latest) {
						return false, nil
					}
					latest.Mode = "daemon"
					latest.PID = os.Getpid()
					latest.ConnectionState = session.ConnectionStateConnected
					latest.LastError = ""
					latest.LastConnectedAt = time.Now().Format(time.RFC3339)
					return true, nil
				})
				if saveErr != nil && !os.IsNotExist(saveErr) {
					fmt.Printf("[!] failed to mark tunnel %s connected: %v\n", sess.TunnelID, saveErr)
				}
			})
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Printf("[!] tunnel %s disconnected: %v\n", current.TunnelID, err)
			if _, saveErr := session.UpdateAtomic(sess.TunnelID, func(latest *session.TunnelSession) (bool, error) {
				if shouldPreserveStoppedSession(latest) {
					return false, nil
				}
				latest.Mode = "daemon"
				latest.PID = os.Getpid()
				latest.ConnectionState = session.ConnectionStateError
				latest.LastError = err.Error()
				return true, nil
			}); saveErr != nil && !os.IsNotExist(saveErr) {
				fmt.Printf("[!] failed to persist tunnel %s error state: %v\n", sess.TunnelID, saveErr)
			}
		} else {
			if _, saveErr := session.UpdateAtomic(sess.TunnelID, func(latest *session.TunnelSession) (bool, error) {
				if shouldPreserveStoppedSession(latest) {
					return false, nil
				}
				latest.Mode = "daemon"
				latest.PID = os.Getpid()
				latest.ConnectionState = session.ConnectionStateError
				latest.LastError = "tunnel connection closed; reconnecting"
				return true, nil
			}); saveErr != nil && !os.IsNotExist(saveErr) {
				fmt.Printf("[!] failed to persist tunnel %s closed state: %v\n", sess.TunnelID, saveErr)
			}
		}

		reconnectTimer.Reset(2 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-reconnectTimer.C:
		}
	}
}
