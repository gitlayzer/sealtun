package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/spf13/cobra"
)

type discoverItem struct {
	Port         int     `json:"port"`
	Address      string  `json:"address"`
	PID          int     `json:"pid,omitempty"`
	ProcessName  string  `json:"processName,omitempty"`
	ProtocolHint string  `json:"protocolHint"`
	TemplateHint string  `json:"templateHint"`
	Confidence   float64 `json:"confidence"`
	Command      string  `json:"command,omitempty"`
}

type discoverOptions struct {
	JSON     bool
	Protocol string
	Limit    int
}

type portDiscoverer interface {
	ListListeningPorts(context.Context) ([]discoverItem, error)
}

var discoverOpts discoverOptions

var discoverCmd = &cobra.Command{
	Use:          "discover",
	Short:        "Discover local TCP listening ports for tunnel creation",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if discoverOpts.Limit < 1 || discoverOpts.Limit > 200 {
			return fmt.Errorf("limit must be between 1 and 200")
		}
		items, err := discoverLocalPorts(cmd.Context(), discoverOpts, systemPortDiscoverer{})
		if err != nil {
			return err
		}
		if discoverOpts.JSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}
		printDiscoverTable(cmd, items)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(discoverCmd)
	discoverCmd.Flags().BoolVar(&discoverOpts.JSON, "json", false, "Output discovered ports as JSON")
	discoverCmd.Flags().StringVar(&discoverOpts.Protocol, "protocol", "auto", "Filter protocol hint: auto, https, ssh, or tcp")
	discoverCmd.Flags().IntVar(&discoverOpts.Limit, "limit", 30, "Maximum number of ports to return")
}

func discoverLocalPorts(ctx context.Context, opts discoverOptions, provider portDiscoverer) ([]discoverItem, error) {
	limitValue := ""
	if opts.Limit != 0 {
		limitValue = strconv.Itoa(opts.Limit)
	}
	limit, err := parseDiscoverLimit(limitValue, 30)
	if err != nil {
		return nil, err
	}
	protocol := strings.ToLower(strings.TrimSpace(opts.Protocol))
	if protocol == "" {
		protocol = "auto"
	}
	if protocol != "auto" && protocol != tunnelprotocol.HTTPS && protocol != tunnelprotocol.SSH && protocol != tunnelprotocol.TCP {
		return nil, fmt.Errorf("--protocol must be auto, https, ssh, or tcp")
	}
	items, err := provider.ListListeningPorts(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return []discoverItem{}, nil
	}
	seen := map[int]bool{}
	filtered := make([]discoverItem, 0, len(items))
	for _, item := range items {
		if item.Port < 1 || item.Port > 65535 || seen[item.Port] {
			continue
		}
		seen[item.Port] = true
		item = applyPortHints(item)
		if protocol != "auto" && item.ProtocolHint != protocol {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Confidence == filtered[j].Confidence {
			return filtered[i].Port < filtered[j].Port
		}
		return filtered[i].Confidence > filtered[j].Confidence
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func parseDiscoverLimit(value string, fallback int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 200 {
		return 0, fmt.Errorf("limit must be between 1 and 200")
	}
	return limit, nil
}

func applyPortHints(item discoverItem) discoverItem {
	switch item.Port {
	case 22:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.SSH, "ssh", 0.98
	case 3306:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.TCP, "mysql", 0.95
	case 5432:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.TCP, "postgres", 0.95
	case 6379:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.TCP, "redis", 0.95
	case 27017:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.TCP, "mongodb", 0.95
	case 1883:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.TCP, "mqtt", 0.95
	default:
		item.ProtocolHint, item.TemplateHint, item.Confidence = tunnelprotocol.HTTPS, "https", 0.65
	}
	if item.Command == "" {
		item.Command = fmt.Sprintf("sealtun expose %d --protocol %s", item.Port, item.ProtocolHint)
	}
	return item
}

func printDiscoverTable(cmd *cobra.Command, items []discoverItem) {
	out := cmd.OutOrStdout()
	if len(items) == 0 {
		fmt.Fprintln(out, "No local TCP listening ports found.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PORT\tADDRESS\tPID\tPROCESS\tPROTOCOL\tTEMPLATE\tCONFIDENCE\tCOMMAND")
	for _, item := range items {
		pid := "-"
		if item.PID != 0 {
			pid = strconv.Itoa(item.PID)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%.2f\t%s\n", item.Port, valueOr(item.Address, "-"), pid, valueOr(item.ProcessName, "-"), item.ProtocolHint, item.TemplateHint, item.Confidence, item.Command)
	}
	_ = tw.Flush()
}

type systemPortDiscoverer struct{}

func (systemPortDiscoverer) ListListeningPorts(ctx context.Context) ([]discoverItem, error) {
	if runtime.GOOS == "linux" {
		if items, err := readProcListeningPorts(); err == nil && len(items) > 0 {
			return items, nil
		}
	}
	return discoverPortsWithLsof(ctx)
}

func readProcListeningPorts() ([]discoverItem, error) {
	items, err := readProcNetTCP("/proc/net/tcp")
	if err != nil {
		return nil, err
	}
	if ipv6, err := readProcNetTCP("/proc/net/tcp6"); err == nil {
		items = append(items, ipv6...)
	}
	return items, nil
}

func readProcNetTCP(path string) ([]discoverItem, error) {
	file, err := os.Open(path) // #nosec G304 -- callers pass fixed /proc/net/tcp paths for local port discovery.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	items := []discoverItem{}
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		host, port, ok := parseProcTCPAddress(fields[1])
		if !ok {
			continue
		}
		items = append(items, discoverItem{Port: port, Address: host})
	}
	return items, scanner.Err()
}

func parseProcTCPAddress(value string) (string, int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", 0, false
	}
	port64, err := strconv.ParseInt(parts[1], 16, 32)
	if err != nil || port64 < 1 || port64 > 65535 {
		return "", 0, false
	}
	host := "0.0.0.0"
	if len(parts[0]) == 8 {
		b := make([]byte, 4)
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseUint(parts[0][i*2:i*2+2], 16, 8)
			if err != nil {
				return "", 0, false
			}
			b[3-i] = byte(v)
		}
		host = net.IP(b).String()
	}
	return host, int(port64), true
}

func discoverPortsWithLsof(ctx context.Context) ([]discoverItem, error) {
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("discover local ports: lsof unavailable or failed: %w", err)
	}
	return parseLsofListeningPorts(string(output)), nil
}

func parseLsofListeningPorts(output string) []discoverItem {
	items := []discoverItem{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		pid, _ := strconv.Atoi(fields[1])
		nameField := fields[len(fields)-2]
		if strings.Contains(nameField, "->") {
			continue
		}
		host, port, ok := parseLsofName(nameField)
		if !ok {
			continue
		}
		items = append(items, discoverItem{
			Port:        port,
			Address:     host,
			PID:         pid,
			ProcessName: fields[0],
		})
	}
	return items
}

func parseLsofName(value string) (string, int, bool) {
	idx := strings.LastIndex(value, ":")
	if idx < 0 || idx == len(value)-1 {
		return "", 0, false
	}
	port, err := strconv.Atoi(value[idx+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	host := strings.Trim(value[:idx], "[]")
	if host == "" || host == "*" {
		host = "0.0.0.0"
	}
	return host, port, true
}
