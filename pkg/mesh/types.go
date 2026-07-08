package mesh

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ConfigVersion = "v1"
	DefaultName   = "global"

	ProtocolHTTP = "http"
	ProtocolTCP  = "tcp"

	DefaultGatewayName = "sealtun-mesh-gateway"
	DefaultGatewayPort = int32(8080)
	importPortBase     = uint32(20000)
	importPortRange    = uint32(20000)
)

var dnsNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type Config struct {
	Version      string    `json:"version"`
	Name         string    `json:"name"`
	HomeRegion   string    `json:"homeRegion"`
	GatewayToken string    `json:"gatewayToken,omitempty"`
	Regions      []Region  `json:"regions,omitempty"`
	Services     []Service `json:"services,omitempty"`
	UpdatedAt    string    `json:"updatedAt,omitempty"`
}

type Region struct {
	Name        string `json:"name"`
	RegionURL   string `json:"regionUrl,omitempty"`
	Profile     string `json:"profile"`
	Namespace   string `json:"namespace,omitempty"`
	GatewayHost string `json:"gatewayHost,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type Service struct {
	Name      string   `json:"name"`
	Protocol  string   `json:"protocol"`
	From      string   `json:"from"`
	Namespace string   `json:"namespace"`
	Service   string   `json:"service"`
	Port      int32    `json:"port"`
	Imports   []string `json:"imports"`
	CreatedAt string   `json:"createdAt,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

type GatewayRoute struct {
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	ListenPort       int32  `json:"listenPort"`
	TargetRegion     string `json:"targetRegion"`
	TargetNamespace  string `json:"targetNamespace"`
	TargetService    string `json:"targetService"`
	TargetPort       int32  `json:"targetPort"`
	RemoteGatewayURL string `json:"remoteGatewayUrl"`
}

func NewConfig(name, homeRegion, token string) Config {
	name = NormalizeName(defaultString(name, DefaultName))
	return Config{
		Version:      ConfigVersion,
		Name:         name,
		HomeRegion:   NormalizeName(homeRegion),
		GatewayToken: token,
		UpdatedAt:    nowString(),
	}
}

func (c *Config) Normalize() error {
	if c.Version == "" {
		c.Version = ConfigVersion
	}
	if c.Version != ConfigVersion {
		return fmt.Errorf("unsupported mesh config version %q", c.Version)
	}
	c.Name = NormalizeName(defaultString(c.Name, DefaultName))
	if err := ValidateName("mesh name", c.Name); err != nil {
		return err
	}
	c.HomeRegion = NormalizeName(c.HomeRegion)
	if err := ValidateName("home region", c.HomeRegion); err != nil {
		return err
	}
	for i := range c.Regions {
		if err := c.Regions[i].Normalize(); err != nil {
			return err
		}
	}
	for i := range c.Services {
		if err := c.Services[i].Normalize(); err != nil {
			return err
		}
	}
	sort.Slice(c.Regions, func(i, j int) bool { return c.Regions[i].Name < c.Regions[j].Name })
	sort.Slice(c.Services, func(i, j int) bool { return c.Services[i].Name < c.Services[j].Name })
	c.UpdatedAt = nowString()
	return nil
}

func (r *Region) Normalize() error {
	r.Name = NormalizeName(r.Name)
	if err := ValidateName("region", r.Name); err != nil {
		return err
	}
	r.Profile = strings.TrimSpace(r.Profile)
	if r.Profile == "" {
		r.Profile = "mesh-" + r.Name
	}
	r.Namespace = strings.TrimSpace(r.Namespace)
	r.GatewayHost = NormalizeHostname(r.GatewayHost)
	r.UpdatedAt = nowString()
	return nil
}

func (s *Service) Normalize() error {
	s.Name = NormalizeName(s.Name)
	if err := ValidateName("service name", s.Name); err != nil {
		return err
	}
	s.Protocol = NormalizeProtocol(s.Protocol)
	if err := ValidateProtocol(s.Protocol); err != nil {
		return err
	}
	s.From = NormalizeName(s.From)
	if err := ValidateName("source region", s.From); err != nil {
		return err
	}
	s.Namespace = NormalizeName(defaultString(s.Namespace, "default"))
	if err := ValidateName("namespace", s.Namespace); err != nil {
		return err
	}
	s.Service = NormalizeName(s.Service)
	if err := ValidateName("kubernetes service", s.Service); err != nil {
		return err
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("invalid service port %d: must be between 1 and 65535", s.Port)
	}
	seen := map[string]bool{}
	imports := make([]string, 0, len(s.Imports))
	for _, value := range s.Imports {
		region := NormalizeName(value)
		if region == "" || seen[region] {
			continue
		}
		if err := ValidateName("import region", region); err != nil {
			return err
		}
		seen[region] = true
		imports = append(imports, region)
	}
	sort.Strings(imports)
	s.Imports = imports
	if s.CreatedAt == "" {
		s.CreatedAt = nowString()
	}
	s.UpdatedAt = nowString()
	return nil
}

func NormalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return strings.Trim(value, "-")
}

func NormalizeHostname(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func NormalizeProtocol(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "https":
		return ProtocolHTTP
	default:
		return value
	}
}

func ValidateName(label, value string) error {
	if !dnsNamePattern.MatchString(value) {
		return fmt.Errorf("invalid %s %q: use a DNS-compatible name", label, value)
	}
	return nil
}

func ValidateProtocol(value string) error {
	switch NormalizeProtocol(value) {
	case ProtocolHTTP, ProtocolTCP:
		return nil
	default:
		return fmt.Errorf("unsupported mesh protocol %q: supported protocols are http and tcp", value)
	}
}

func ImportServiceName(name string) string {
	return "mesh-" + NormalizeName(name)
}

func ImportPort(name string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(NormalizeName(name)))
	// #nosec G115 -- offset is bounded to 0..19999 and the final port is below 40000.
	return int32(importPortBase + h.Sum32()%importPortRange)
}

func (c Config) Region(name string) (Region, bool) {
	name = NormalizeName(name)
	for _, region := range c.Regions {
		if region.Name == name {
			return region, true
		}
	}
	return Region{}, false
}

func (c *Config) UpsertRegion(region Region) error {
	if err := region.Normalize(); err != nil {
		return err
	}
	for i := range c.Regions {
		if c.Regions[i].Name == region.Name {
			c.Regions[i] = region
			return c.Normalize()
		}
	}
	c.Regions = append(c.Regions, region)
	return c.Normalize()
}

func (c *Config) UpsertService(service Service) error {
	if err := service.Normalize(); err != nil {
		return err
	}
	for i := range c.Services {
		if c.Services[i].Name == service.Name {
			service.CreatedAt = c.Services[i].CreatedAt
			c.Services[i] = service
			return c.Normalize()
		}
	}
	c.Services = append(c.Services, service)
	return c.Normalize()
}

func (c *Config) RemoveService(name string) bool {
	name = NormalizeName(name)
	for i := range c.Services {
		if c.Services[i].Name == name {
			c.Services = append(c.Services[:i], c.Services[i+1:]...)
			_ = c.Normalize()
			return true
		}
	}
	return false
}

func (c Config) RoutesForRegion(regionName string) []GatewayRoute {
	regionName = NormalizeName(regionName)
	routes := []GatewayRoute{}
	for _, service := range c.Services {
		isSourceRegion := service.From == regionName
		isImportRegion := containsString(service.Imports, regionName)
		if !isSourceRegion && !isImportRegion {
			continue
		}
		targetRegion, ok := c.Region(service.From)
		remoteURL := ""
		if isImportRegion && !isSourceRegion && ok && targetRegion.GatewayHost != "" {
			remoteURL = "https://" + targetRegion.GatewayHost
		}
		routes = append(routes, GatewayRoute{
			Name:             service.Name,
			Protocol:         service.Protocol,
			ListenPort:       ImportPort(service.Name),
			TargetRegion:     service.From,
			TargetNamespace:  service.Namespace,
			TargetService:    service.Service,
			TargetPort:       service.Port,
			RemoteGatewayURL: remoteURL,
		})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return routes
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}
