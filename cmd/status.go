package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

type fileStatus struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
}

type kubeconfigStatus struct {
	Path           string `json:"path"`
	Present        bool   `json:"present"`
	CurrentContext string `json:"currentContext,omitempty"`
	Cluster        string `json:"cluster,omitempty"`
	Namespace      string `json:"namespace,omitempty"`
	Error          string `json:"error,omitempty"`
}

type statusPayload struct {
	LoggedIn        bool             `json:"loggedIn"`
	Region          string           `json:"region,omitempty"`
	AuthMethod      string           `json:"authMethod,omitempty"`
	AuthenticatedAt string           `json:"authenticatedAt,omitempty"`
	WorkspaceID     string           `json:"workspaceId,omitempty"`
	WorkspaceName   string           `json:"workspaceName,omitempty"`
	ConfigDir       string           `json:"configDir"`
	AuthFile        fileStatus       `json:"authFile"`
	Kubeconfig      kubeconfigStatus `json:"kubeconfig"`
	Warnings        []string         `json:"warnings,omitempty"`
}

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current Sealtun login status",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := collectStatus()
		if err != nil {
			return err
		}

		if statusJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(status)
		}

		printHumanStatus(cmd, status)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output status as JSON")
}

func collectStatus() (*statusPayload, error) {
	dir, err := auth.GetSealosDir()
	if err != nil {
		return nil, err
	}

	authPath := filepath.Join(dir, "auth.json")
	kubeconfigPath := filepath.Join(dir, "kubeconfig")

	status := &statusPayload{
		ConfigDir: dir,
		AuthFile: fileStatus{
			Path:    authPath,
			Present: fileExists(authPath),
		},
		Kubeconfig: kubeconfigStatus{
			Path:    kubeconfigPath,
			Present: fileExists(kubeconfigPath),
		},
	}

	authData, err := auth.LoadAuthData()
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load auth status: %w", err)
		}
	} else {
		status.LoggedIn = true
		status.Region = authData.Region
		status.AuthMethod = authData.AuthMethod
		status.AuthenticatedAt = formatAuthTime(authData.AuthenticatedAt)
		if authData.CurrentWorkspace != nil {
			status.WorkspaceID = authData.CurrentWorkspace.ID
			status.WorkspaceName = authData.CurrentWorkspace.TeamName
		}
	}

	if status.Kubeconfig.Present {
		cfg, err := clientcmd.LoadFromFile(kubeconfigPath)
		if err != nil {
			status.Kubeconfig.Error = err.Error()
			status.Warnings = append(status.Warnings, "kubeconfig exists but could not be parsed")
		} else {
			status.Kubeconfig.CurrentContext = cfg.CurrentContext
			if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok {
				status.Kubeconfig.Cluster = ctx.Cluster
				if ctx.Namespace != "" {
					status.Kubeconfig.Namespace = ctx.Namespace
				} else {
					status.Kubeconfig.Namespace = "default"
				}
			}
		}
	}

	if status.LoggedIn && !status.Kubeconfig.Present {
		status.Warnings = append(status.Warnings, "logged in but kubeconfig is missing")
	}
	if !status.LoggedIn && status.Kubeconfig.Present {
		status.Warnings = append(status.Warnings, "kubeconfig exists without an active Sealtun login session")
	}

	return status, nil
}

func printHumanStatus(cmd *cobra.Command, status *statusPayload) {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Sealtun Status")
	fmt.Fprintf(out, "  Logged in: %s\n", yesNo(status.LoggedIn))
	fmt.Fprintf(out, "  Config dir: %s\n", status.ConfigDir)

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Session")
	if status.LoggedIn {
		fmt.Fprintf(out, "  Region: %s\n", valueOr(status.Region, "unknown"))
		fmt.Fprintf(out, "  Auth method: %s\n", valueOr(status.AuthMethod, "unknown"))
		fmt.Fprintf(out, "  Authenticated at: %s\n", valueOr(status.AuthenticatedAt, "unknown"))
		if status.WorkspaceID != "" || status.WorkspaceName != "" {
			fmt.Fprintf(out, "  Workspace: %s\n", workspaceLabel(status.WorkspaceID, status.WorkspaceName))
		} else {
			fmt.Fprintln(out, "  Workspace: unavailable")
		}
	} else {
		fmt.Fprintln(out, "  No active login session.")
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Kubernetes")
	fmt.Fprintf(out, "  Auth file: %s\n", presentLabel(status.AuthFile.Present, status.AuthFile.Path))
	fmt.Fprintf(out, "  Kubeconfig: %s\n", presentLabel(status.Kubeconfig.Present, status.Kubeconfig.Path))
	if status.Kubeconfig.Present {
		fmt.Fprintf(out, "  Current context: %s\n", valueOr(status.Kubeconfig.CurrentContext, "unknown"))
		fmt.Fprintf(out, "  Cluster: %s\n", valueOr(status.Kubeconfig.Cluster, "unknown"))
		fmt.Fprintf(out, "  Namespace: %s\n", valueOr(status.Kubeconfig.Namespace, "unknown"))
		if status.Kubeconfig.Error != "" {
			fmt.Fprintf(out, "  Kubeconfig error: %s\n", status.Kubeconfig.Error)
		}
	}

	if len(status.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range status.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
}

func formatAuthTime(value string) string {
	if value == "" {
		return ""
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}

	return t.Local().Format(time.RFC3339)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func presentLabel(present bool, path string) string {
	if present {
		return path
	}
	return "missing"
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func workspaceLabel(id, name string) string {
	if id == "" {
		return name
	}
	if name == "" {
		return id
	}
	return fmt.Sprintf("%s (%s)", id, name)
}
