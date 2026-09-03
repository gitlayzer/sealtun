package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/spf13/cobra"
)

var logsTail int64
var logsSince time.Duration
var logsFollow bool
var logsRaw bool

var logsCmd = &cobra.Command{
	Use:          "logs [tunnel-id]",
	Short:        "Show remote tunnel pod logs",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateLogOptions(logsTail, logsSince); err != nil {
			return err
		}
		sess, err := findSession(args[0])
		if err != nil {
			return err
		}
		client, err := k8sClientForSession(*sess)
		if err != nil {
			return err
		}

		ctx := cmd.Context()
		cancel := func() {}
		if !logsFollow {
			ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		}
		defer cancel()

		opts := k8s.TunnelLogOptions{
			TailLines: logsTail,
			Follow:    logsFollow,
		}
		if logsSince > 0 {
			opts.SinceSeconds = int64(logsSince.Seconds())
		}
		out := cmd.OutOrStdout()
		if !logsRaw {
			// Remote pod logs can carry attacker-controlled bytes (request
			// paths, user agents, app output). Strip terminal escape and
			// control sequences so viewing logs cannot hijack the terminal;
			// --raw restores the unfiltered stream.
			out = newControlCharFilterWriter(out)
		}
		if err := client.WithNamespace(sess.Namespace).StreamTunnelLogs(ctx, sess.TunnelID, out, opts); err != nil {
			return fmt.Errorf("stream logs for tunnel %s: %w", sess.TunnelID, err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().Int64Var(&logsTail, "tail", 100, "Number of recent log lines to show")
	logsCmd.Flags().DurationVar(&logsSince, "since", 0, "Only return logs newer than this duration, e.g. 10m")
	logsCmd.Flags().BoolVar(&logsFollow, "follow", false, "Follow log output")
	logsCmd.Flags().BoolVar(&logsRaw, "raw", false, "Output logs unfiltered, including terminal escape sequences")
}

func validateLogOptions(tail int64, since time.Duration) error {
	if tail < 0 {
		return fmt.Errorf("--tail must be greater than or equal to 0")
	}
	if since < 0 {
		return fmt.Errorf("--since must be greater than or equal to 0")
	}
	return nil
}

// controlCharFilterWriter drops ESC and other C0 control characters (except
// newline, carriage return, and tab) plus DEL from whatever passes through.
// This blocks ANSI/VT100 sequences, OSC clipboard writes, and cursor control
// from remote-controlled log content.
type controlCharFilterWriter struct {
	out io.Writer
}

func newControlCharFilterWriter(out io.Writer) io.Writer {
	return &controlCharFilterWriter{out: out}
}

func (w *controlCharFilterWriter) Write(p []byte) (int, error) {
	filtered := make([]byte, 0, len(p))
	for _, b := range p {
		switch {
		case b == '\n' || b == '\r' || b == '\t':
			filtered = append(filtered, b)
		case b == 0x1b || b < 0x20 || b == 0x7f:
			// drop escape and control characters
		default:
			filtered = append(filtered, b)
		}
	}
	if len(filtered) == 0 {
		return len(p), nil
	}
	if _, err := w.out.Write(filtered); err != nil {
		return 0, err
	}
	return len(p), nil
}
