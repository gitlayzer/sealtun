package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/mesh"
	"github.com/spf13/cobra"
)

var meshName string
var meshHome string
var meshRegions string
var meshInsecure bool
var meshOutputJSON bool
var meshApply bool

var meshCmd = &cobra.Command{
	Use:   "mesh",
	Short: "Manage service-level Sealtun Mesh across Sealos regions",
	Long: `Manage service-level Sealtun Mesh across Sealos regions.

Mesh v1 imports explicitly published Kubernetes Services into other regions as
local ClusterIP Services. It does not provide transparent Pod IP or CNI routing.`,
}

var meshInitCmd = &cobra.Command{
	Use:          "init",
	Short:        "Initialize a local Mesh registry",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		regions, err := resolveMeshRegions(meshRegions)
		if err != nil {
			return err
		}
		home, err := resolveMeshRegion(meshHome)
		if err != nil {
			return err
		}
		token, err := randomHex(32)
		if err != nil {
			return err
		}
		config := mesh.NewConfig(meshName, home.Name, token)
		for _, region := range regions {
			if err := config.UpsertRegion(mesh.Region{
				Name:      region.Name,
				RegionURL: region.URL,
				Profile:   meshProfileName(region.Name),
			}); err != nil {
				return err
			}
		}
		if _, ok := config.Region(home.Name); !ok {
			if err := config.UpsertRegion(mesh.Region{Name: home.Name, RegionURL: home.URL, Profile: meshProfileName(home.Name)}); err != nil {
				return err
			}
		}
		store, err := mesh.DefaultStore()
		if err != nil {
			return err
		}
		if err := store.Save(config); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Initialized mesh %s with home region %s.\n", config.Name, config.HomeRegion)
		fmt.Fprintf(cmd.OutOrStdout(), "Saved mesh registry: %s\n", store.Path())
		return nil
	},
}

var meshLoginCmd = &cobra.Command{
	Use:          "login",
	Short:        "Log in to multiple Mesh regions as mesh-* profiles",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		regions, err := resolveMeshRegions(meshRegions)
		if err != nil {
			return err
		}
		for _, region := range regions {
			profile := meshProfileName(region.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "\n==> Login region %s as profile %s\n", region.Name, profile)
			if err := runLoginFlowWithProfile(region.Name, meshInsecure, profile); err != nil {
				return err
			}
		}
		return nil
	},
}

var meshAuthStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show Mesh region profile status",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		type item struct {
			Region    string `json:"region"`
			Profile   string `json:"profile"`
			Namespace string `json:"namespace,omitempty"`
			OK        bool   `json:"ok"`
			Error     string `json:"error,omitempty"`
		}
		items := []item{}
		for _, region := range config.Regions {
			entry := item{Region: region.Name, Profile: region.Profile, Namespace: region.Namespace}
			if profile, _, err := auth.LoadProfile(region.Profile); err != nil {
				entry.Error = err.Error()
			} else if profile.AuthData == nil {
				entry.Error = "profile has no auth data"
			} else {
				entry.OK = true
			}
			items = append(items, entry)
		}
		if meshOutputJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}
		for _, item := range items {
			status := "ok"
			if !item.OK {
				status = "missing"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", item.Region, item.Profile, status)
			if item.Error != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", item.Error)
			}
		}
		return nil
	},
}

var meshStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show Mesh registry status",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		if meshOutputJSON {
			redacted := *config
			redacted.GatewayToken = ""
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(redacted)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Mesh: %s\nHome: %s\nRegions: %d\nServices: %d\n", config.Name, config.HomeRegion, len(config.Regions), len(config.Services))
		for _, region := range config.Regions {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s profile=%s namespace=%s gateway=%s\n", region.Name, region.Profile, valueOr(region.Namespace, "-"), valueOr(region.GatewayHost, "-"))
		}
		return nil
	},
}

var meshUpCmd = &cobra.Command{
	Use:          "up",
	Short:        "Apply Mesh gateways and imported services",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		store, err := mesh.DefaultStore()
		if err != nil {
			return err
		}
		return applyMeshConfig(cmd.Context(), store, *config, cmd.OutOrStdout())
	},
}

var meshDownCmd = &cobra.Command{
	Use:          "down",
	Short:        "Remove Mesh gateway and import resources from configured regions",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		for _, region := range config.Regions {
			_, client, err := meshClientForRegion(region)
			if err != nil {
				return fmt.Errorf("region %s: %w", region.Name, err)
			}
			for _, service := range config.Services {
				if containsMeshRegion(service.Imports, region.Name) {
					if err := client.CleanupMeshImport(cmd.Context(), service.Name); err != nil {
						return fmt.Errorf("region %s cleanup import %s: %w", region.Name, service.Name, err)
					}
				}
			}
			if err := client.CleanupMesh(cmd.Context(), config.Name); err != nil {
				return fmt.Errorf("region %s cleanup gateway: %w", region.Name, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cleaned mesh resources in %s.\n", region.Name)
		}
		return nil
	},
}

var meshServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Publish and inspect Mesh services",
}

var meshServiceFrom string
var meshServiceTarget string
var meshServiceProtocol string
var meshServiceImports string

var meshServicePublishCmd = &cobra.Command{
	Use:          "publish [name]",
	Short:        "Publish a Kubernetes Service into other Mesh regions",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		from, err := resolveMeshRegion(meshServiceFrom)
		if err != nil {
			return err
		}
		if _, ok := config.Region(from.Name); !ok {
			return fmt.Errorf("source region %s is not in mesh config; run `sealtun mesh init --regions ...` first", from.Name)
		}
		namespace, serviceName, port, err := parseKubernetesServiceTarget(meshServiceTarget)
		if err != nil {
			return err
		}
		imports, err := resolveImportRegions(config, meshServiceImports, from.Name)
		if err != nil {
			return err
		}
		service := mesh.Service{
			Name:      args[0],
			Protocol:  meshServiceProtocol,
			From:      from.Name,
			Namespace: namespace,
			Service:   serviceName,
			Port:      int32(port),
			Imports:   imports,
		}
		if err := config.UpsertService(service); err != nil {
			return err
		}
		store, err := mesh.DefaultStore()
		if err != nil {
			return err
		}
		if err := store.Save(*config); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Published service %s from %s/%s:%d.\n", service.Name, service.Namespace, service.Service, service.Port)
		if meshApply {
			return applyMeshConfig(cmd.Context(), store, *config, cmd.OutOrStdout())
		}
		return nil
	},
}

var meshServiceListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List Mesh services",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		if meshOutputJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(config.Services)
		}
		if len(config.Services) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No Mesh services published.")
			return nil
		}
		for _, service := range config.Services {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s/%s:%d\timports=%s\n", service.Name, service.Protocol, service.Namespace, service.Service, service.Port, strings.Join(service.Imports, ","))
		}
		return nil
	},
}

var meshServiceUnpublishCmd = &cobra.Command{
	Use:          "unpublish [name]",
	Short:        "Remove a Mesh service from the local registry",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		removed, ok := findMeshService(*config, args[0])
		if !ok {
			return fmt.Errorf("mesh service %s not found", args[0])
		}
		if !config.RemoveService(args[0]) {
			return fmt.Errorf("mesh service %s not found", args[0])
		}
		store, err := mesh.DefaultStore()
		if err != nil {
			return err
		}
		if err := store.Save(*config); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed mesh service %s from registry.\n", mesh.NormalizeName(args[0]))
		if meshApply {
			for _, regionName := range removed.Imports {
				region, ok := config.Region(regionName)
				if !ok {
					continue
				}
				_, client, err := meshClientForRegion(region)
				if err != nil {
					return fmt.Errorf("region %s: %w", regionName, err)
				}
				if err := client.CleanupMeshImport(cmd.Context(), removed.Name); err != nil {
					return fmt.Errorf("region %s cleanup import %s: %w", regionName, removed.Name, err)
				}
			}
			return applyMeshConfig(cmd.Context(), store, *config, cmd.OutOrStdout())
		}
		return nil
	},
}

var meshCheckFrom string

var meshServiceCheckCmd = &cobra.Command{
	Use:          "check [name]",
	Short:        "Check Mesh service control-plane readiness",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := loadMeshConfig()
		if err != nil {
			return err
		}
		service, ok := findMeshService(*config, args[0])
		if !ok {
			return fmt.Errorf("mesh service %s not found", args[0])
		}
		fromRegions := service.Imports
		if strings.TrimSpace(meshCheckFrom) != "" {
			from, err := resolveMeshRegion(meshCheckFrom)
			if err != nil {
				return err
			}
			fromRegions = []string{from.Name}
		}
		targetRegion, ok := config.Region(service.From)
		if !ok {
			return fmt.Errorf("target region %s is not in mesh config", service.From)
		}
		_, targetClient, err := meshClientForRegion(targetRegion)
		if err != nil {
			return fmt.Errorf("target region %s: %w", targetRegion.Name, err)
		}
		remoteExists, readyPods, err := targetClient.KubernetesServiceReady(cmd.Context(), service.Namespace, service.Service)
		if err != nil {
			return err
		}
		for _, from := range fromRegions {
			sourceRegion, ok := config.Region(from)
			if !ok {
				return fmt.Errorf("source region %s is not in mesh config", from)
			}
			_, sourceClient, err := meshClientForRegion(sourceRegion)
			if err != nil {
				return fmt.Errorf("source region %s: %w", sourceRegion.Name, err)
			}
			check, err := sourceClient.MeshCheck(cmd.Context(), config.Name, service)
			if err != nil {
				return err
			}
			dns := fmt.Sprintf("%s.%s.svc.cluster.local:%d", mesh.ImportServiceName(service.Name), sourceClient.Namespace(), service.Port)
			fmt.Fprintf(cmd.OutOrStdout(), "From %s -> %s: %s\n", from, service.From, dns)
			fmt.Fprintf(cmd.OutOrStdout(), "  source gateway: deployment=%t service=%t host=%s\n", check.GatewayDeploymentReady, check.GatewayServiceExists, valueOr(check.GatewayIngressHost, "-"))
			fmt.Fprintf(cmd.OutOrStdout(), "  import service: %t\n", check.ImportServiceExists)
			fmt.Fprintf(cmd.OutOrStdout(), "  remote service: exists=%t readyPods=%d\n", remoteExists, readyPods)
			if len(check.Warnings) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "  warnings: %s\n", strings.Join(check.Warnings, "; "))
			}
		}
		return nil
	},
}

var meshGatewayListen string
var meshGatewayRoutesEnv string
var meshGatewayTokenEnv string
var meshGatewayRoutesFile string
var meshGatewayToken string

var meshGatewayCmd = &cobra.Command{
	Use:          "gateway",
	Short:        "Run a Mesh gateway data plane",
	Hidden:       true,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		token := strings.TrimSpace(meshGatewayToken)
		if token == "" && meshGatewayTokenEnv != "" {
			token = os.Getenv(meshGatewayTokenEnv)
		}
		routesJSON := ""
		if meshGatewayRoutesEnv != "" {
			routesJSON = os.Getenv(meshGatewayRoutesEnv)
		}
		if routesJSON == "" && meshGatewayRoutesFile != "" {
			data, err := os.ReadFile(meshGatewayRoutesFile) // #nosec G304 -- explicit operator-provided gateway route file.
			if err != nil {
				return err
			}
			routesJSON = string(data)
		}
		var routes []mesh.GatewayRoute
		if strings.TrimSpace(routesJSON) != "" {
			if err := json.Unmarshal([]byte(routesJSON), &routes); err != nil {
				return fmt.Errorf("parse mesh routes: %w", err)
			}
		}
		return mesh.RunGateway(cmd.Context(), mesh.GatewayOptions{Listen: meshGatewayListen, Token: token, Routes: routes})
	},
}

func init() {
	rootCmd.AddCommand(meshCmd)
	meshCmd.PersistentFlags().StringVar(&meshName, "name", mesh.DefaultName, "Mesh name")
	meshCmd.PersistentFlags().BoolVar(&meshOutputJSON, "json", false, "Output JSON")

	meshCmd.AddCommand(meshInitCmd)
	meshInitCmd.Flags().StringVar(&meshHome, "home", "gzg", "Home region")
	meshInitCmd.Flags().StringVar(&meshRegions, "regions", "gzg", "Comma-separated regions or all")

	meshCmd.AddCommand(meshLoginCmd)
	meshLoginCmd.Flags().StringVar(&meshRegions, "regions", "all", "Comma-separated regions or all")
	meshLoginCmd.Flags().BoolVar(&meshInsecure, "insecure", false, "Skip TLS verification")

	meshAuthCmd := &cobra.Command{Use: "auth", Short: "Manage Mesh authorization"}
	meshAuthCmd.AddCommand(meshAuthStatusCmd)
	meshCmd.AddCommand(meshAuthCmd)

	meshCmd.AddCommand(meshStatusCmd)
	meshCmd.AddCommand(meshUpCmd)
	meshCmd.AddCommand(meshDownCmd)
	meshCmd.AddCommand(meshGatewayCmd)

	meshCmd.AddCommand(meshServiceCmd)
	meshServiceCmd.AddCommand(meshServicePublishCmd)
	meshServicePublishCmd.Flags().StringVar(&meshServiceFrom, "from", "gzg", "Source region")
	meshServicePublishCmd.Flags().StringVar(&meshServiceTarget, "k8s-service", "", "Kubernetes Service as [namespace/]name:port")
	meshServicePublishCmd.Flags().StringVar(&meshServiceProtocol, "protocol", mesh.ProtocolHTTP, "Mesh protocol: http or tcp")
	meshServicePublishCmd.Flags().StringVar(&meshServiceImports, "import", "all", "Comma-separated import regions or all")
	meshServicePublishCmd.Flags().BoolVar(&meshApply, "apply", true, "Apply gateways and imports after updating registry")
	_ = meshServicePublishCmd.MarkFlagRequired("k8s-service")

	meshServiceCmd.AddCommand(meshServiceListCmd)
	meshServiceCmd.AddCommand(meshServiceUnpublishCmd)
	meshServiceUnpublishCmd.Flags().BoolVar(&meshApply, "apply", true, "Apply gateways and imports after updating registry")
	meshServiceCmd.AddCommand(meshServiceCheckCmd)
	meshServiceCheckCmd.Flags().StringVar(&meshCheckFrom, "from", "", "Check from one import region")

	meshGatewayCmd.Flags().StringVar(&meshGatewayListen, "listen", ":8080", "Gateway listen address")
	meshGatewayCmd.Flags().StringVar(&meshGatewayRoutesEnv, "routes-env", "", "Environment variable containing gateway routes JSON")
	meshGatewayCmd.Flags().StringVar(&meshGatewayTokenEnv, "token-env", "", "Environment variable containing gateway token")
	meshGatewayCmd.Flags().StringVar(&meshGatewayRoutesFile, "routes-file", "", "File containing gateway routes JSON")
	meshGatewayCmd.Flags().StringVar(&meshGatewayToken, "token", "", "Gateway token")
}

func loadMeshConfig() (*mesh.Config, error) {
	store, err := mesh.DefaultStore()
	if err != nil {
		return nil, err
	}
	config, err := store.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("mesh is not initialized; run `sealtun mesh init --regions all` first")
		}
		return nil, err
	}
	return config, nil
}

func applyMeshConfig(ctx context.Context, store *mesh.Store, config mesh.Config, out interface{ Write([]byte) (int, error) }) error {
	clients := map[string]*k8s.Client{}
	for i := range config.Regions {
		profile, client, err := meshClientForRegion(config.Regions[i])
		if err != nil {
			return fmt.Errorf("region %s: %w", config.Regions[i].Name, err)
		}
		_ = profile
		config.Regions[i].Namespace = client.Namespace()
		config.Regions[i].GatewayHost = client.MeshGatewayHost(config.Name)
		clients[config.Regions[i].Name] = client
	}
	if err := store.Save(config); err != nil {
		return err
	}
	for _, region := range config.Regions {
		client := clients[region.Name]
		status, err := client.EnsureMeshGateway(ctx, k8s.MeshGatewaySpec{
			MeshName: config.Name,
			Token:    config.GatewayToken,
			Routes:   config.RoutesForRegion(region.Name),
		})
		if err != nil {
			return fmt.Errorf("region %s gateway: %w", region.Name, err)
		}
		fmt.Fprintf(out, "Gateway %s ready in %s: https://%s\n", status.Name, region.Name, status.Host)
	}
	for _, service := range config.Services {
		for _, regionName := range service.Imports {
			client := clients[regionName]
			if client == nil {
				return fmt.Errorf("import region %s is not configured", regionName)
			}
			dns, err := client.EnsureMeshImport(ctx, k8s.MeshImportSpec{
				Name:       service.Name,
				MeshName:   config.Name,
				Protocol:   service.Protocol,
				Port:       service.Port,
				TargetPort: mesh.ImportPort(service.Name),
			})
			if err != nil {
				return fmt.Errorf("region %s import %s: %w", regionName, service.Name, err)
			}
			fmt.Fprintf(out, "Imported %s into %s: %s\n", service.Name, regionName, dns)
		}
	}
	return nil
}

func meshClientForRegion(region mesh.Region) (*auth.Profile, *k8s.Client, error) {
	profile, kubeconfig, err := auth.LoadProfile(region.Profile)
	if err != nil {
		return nil, nil, err
	}
	if profile.AuthData == nil {
		return nil, nil, fmt.Errorf("profile %s has no auth data", region.Profile)
	}
	client, err := k8s.NewClientFromKubeconfig(kubeconfig, profile.AuthData)
	if err != nil {
		return nil, nil, err
	}
	return profile, client, nil
}

func resolveMeshRegions(value string) ([]auth.RegionOption, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return auth.KnownRegions(), nil
	}
	parts := strings.Split(value, ",")
	regions := make([]auth.RegionOption, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		region, err := resolveMeshRegion(part)
		if err != nil {
			return nil, err
		}
		if seen[region.Name] {
			continue
		}
		seen[region.Name] = true
		regions = append(regions, region)
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Name < regions[j].Name })
	return regions, nil
}

func resolveMeshRegion(value string) (auth.RegionOption, error) {
	regionURL, err := auth.ResolveRegion(value)
	if err != nil {
		return auth.RegionOption{}, err
	}
	for _, region := range auth.KnownRegions() {
		if region.URL == regionURL {
			return region, nil
		}
	}
	return auth.RegionOption{}, fmt.Errorf("unknown region %q", value)
}

func meshProfileName(region string) string {
	return "mesh-" + mesh.NormalizeName(region)
}

func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func parseKubernetesServiceTarget(value string) (string, string, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", 0, fmt.Errorf("--k8s-service is required")
	}
	left, portText, ok := strings.Cut(value, ":")
	if !ok {
		return "", "", 0, fmt.Errorf("invalid --k8s-service %q: expected [namespace/]name:port", value)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", "", 0, fmt.Errorf("invalid service port %q", portText)
	}
	namespace := "default"
	service := left
	if strings.Contains(left, "/") {
		namespace, service, _ = strings.Cut(left, "/")
	}
	namespace = mesh.NormalizeName(namespace)
	service = mesh.NormalizeName(service)
	if err := mesh.ValidateName("namespace", namespace); err != nil {
		return "", "", 0, err
	}
	if err := mesh.ValidateName("service", service); err != nil {
		return "", "", 0, err
	}
	return namespace, service, port, nil
}

func resolveImportRegions(config *mesh.Config, value, from string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		regions := []string{}
		for _, region := range config.Regions {
			if region.Name != from {
				regions = append(regions, region.Name)
			}
		}
		if len(regions) == 0 {
			return nil, fmt.Errorf("mesh has no import regions other than %s", from)
		}
		sort.Strings(regions)
		return regions, nil
	}
	regions := []string{}
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		region, err := resolveMeshRegion(item)
		if err != nil {
			return nil, err
		}
		if region.Name == from || seen[region.Name] {
			continue
		}
		if _, ok := config.Region(region.Name); !ok {
			return nil, fmt.Errorf("region %s is not in mesh config; run `sealtun mesh init --regions ...` first", region.Name)
		}
		seen[region.Name] = true
		regions = append(regions, region.Name)
	}
	if len(regions) == 0 {
		return nil, fmt.Errorf("mesh import regions cannot be empty")
	}
	sort.Strings(regions)
	return regions, nil
}

func containsMeshRegion(regions []string, want string) bool {
	for _, region := range regions {
		if region == want {
			return true
		}
	}
	return false
}

func findMeshService(config mesh.Config, name string) (mesh.Service, bool) {
	name = mesh.NormalizeName(name)
	for _, service := range config.Services {
		if service.Name == name {
			return service, true
		}
	}
	return mesh.Service{}, false
}
