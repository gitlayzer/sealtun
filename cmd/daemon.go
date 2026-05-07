package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/session"
	"github.com/labring/sealtun/pkg/tunnel"
	"github.com/spf13/cobra"
)

type managedTunnel struct {
	cancel context.CancelFunc
	done   chan struct{}
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
		heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
		defer stopHeartbeat()
		go runDaemonHeartbeat(heartbeatCtx)
		defer func() {
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
				if sess.ConnectionState == session.ConnectionStateStopped {
					continue
				}
				desired[sess.TunnelID] = sess
			}

			mu.Lock()
			for tunnelID, mt := range managed {
				select {
				case <-mt.done:
					delete(managed, tunnelID)
				default:
				}
			}

			for tunnelID, managedTunnel := range managed {
				if _, ok := desired[tunnelID]; !ok {
					managedTunnel.cancel()
					delete(managed, tunnelID)
				}
			}

			for tunnelID, sess := range desired {
				if _, ok := managed[tunnelID]; ok {
					continue
				}

				tunnelCtx, cancel := context.WithCancel(ctx)
				mt := &managedTunnel{cancel: cancel, done: make(chan struct{})}
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
					runDaemonTunnel(tunnelCtx, sess)
				}(sess, mt)
			}
			mu.Unlock()

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

func init() {
	rootCmd.AddCommand(daemonCmd)
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

func runDaemonTunnel(ctx context.Context, sess session.TunnelSession) {
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

		current.Mode = "daemon"
		current.PID = os.Getpid()
		current.ConnectionState = session.ConnectionStateConnecting
		current.LastError = ""
		if err := session.Update(*current); err != nil {
			if os.IsNotExist(err) {
				return
			}
			fmt.Printf("[!] failed to refresh session %s: %v\n", current.TunnelID, err)
		}

		wsURL := fmt.Sprintf("wss://%s/_sealtun/ws", sessionControlHost(*current))
		err = tunnel.DialServerAndServeWithOnConnected(ctx, wsURL, current.Secret, current.LocalPort, func() {
			latest, getErr := session.Get(sess.TunnelID)
			if getErr != nil {
				return
			}
			latest.Mode = "daemon"
			latest.PID = os.Getpid()
			latest.ConnectionState = session.ConnectionStateConnected
			latest.LastError = ""
			latest.LastConnectedAt = time.Now().Format(time.RFC3339)
			if saveErr := session.Update(*latest); saveErr != nil && !os.IsNotExist(saveErr) {
				fmt.Printf("[!] failed to mark tunnel %s connected: %v\n", latest.TunnelID, saveErr)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Printf("[!] tunnel %s disconnected: %v\n", current.TunnelID, err)
			if latest, getErr := session.Get(sess.TunnelID); getErr == nil {
				latest.Mode = "daemon"
				latest.PID = os.Getpid()
				latest.ConnectionState = session.ConnectionStateError
				latest.LastError = err.Error()
				if saveErr := session.Update(*latest); saveErr != nil && !os.IsNotExist(saveErr) {
					fmt.Printf("[!] failed to persist tunnel %s error state: %v\n", latest.TunnelID, saveErr)
				}
			}
		} else {
			if latest, getErr := session.Get(sess.TunnelID); getErr == nil {
				latest.Mode = "daemon"
				latest.PID = os.Getpid()
				latest.ConnectionState = session.ConnectionStateError
				latest.LastError = "tunnel connection closed; reconnecting"
				if saveErr := session.Update(*latest); saveErr != nil && !os.IsNotExist(saveErr) {
					fmt.Printf("[!] failed to persist tunnel %s closed state: %v\n", latest.TunnelID, saveErr)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
