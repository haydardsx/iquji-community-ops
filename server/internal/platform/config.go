package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultTraefikCertResolver = "iquops-le"

const ManagedJenkinsBundleVersion = "v15-jenkins-2.568.1"
const ManagedJenkinsImage = "iquops-managed-jenkins:" + ManagedJenkinsBundleVersion

type ProcessRole string

const (
	RoleAPI    ProcessRole = "api"
	RoleWorker ProcessRole = "worker"
)

type Config struct {
	Role                       ProcessRole
	Host                       string
	Port                       int
	StateDir                   string
	DatabasePath               string
	RepositoryRoot             string
	PublicBaseURL              string
	WorkerID                   string
	WorkerIntervalMS           int
	WorkerRunOnce              bool
	WorkerConcurrency          int
	SwarmNetwork               string
	TraefikService             string
	TraefikImage               string
	TraefikImagePullCandidates []string
	TraefikHTTPPort            int
	TraefikHTTPSPort           int
	TraefikCertResolver        string
	SwarmAdvertiseAddr         string
	JenkinsBundleDir           string
	JenkinsPluginsArchive      string
	JenkinsBundleVersion       string
	JenkinsImage               string
	JenkinsContainer           string
	JenkinsPort                int
	FrpVersion                 string
	FrpsImage                  string
	FrpsPath                   string
	FrpcPath                   string
	FrpcDNSServer              string
	FrpProbeHost               string
	FrpProbePort               int
	AgentBundleDir             string
}

func Resolve(role ProcessRole) Config {
	root := getenv("IQUOPS_REPOSITORY_ROOT", communitySourceRoot(mustGetwd()))
	stateDir := getenv("IQUOPS_STATE_DIR", filepath.Join(root, ".local", "state", "iquops"))
	databasePath := strings.TrimSpace(os.Getenv("IQUOPS_SQLITE_PATH"))
	if databasePath == "" {
		databasePath = filepath.Join(stateDir, "state.sqlite")
	}
	port := positiveInt(firstEnv("IQUOPS_API_PORT", "PORT"), 3001)
	host := getenvAny([]string{"IQUOPS_API_HOST", "HOST"}, "127.0.0.1")
	frpVersion := getenv("IQUOPS_FRP_VERSION", "v0.69.1")
	traefikImage := getenv("IQUOPS_TRAEFIK_IMAGE", "traefik:v3.7")
	jenkinsDir := getenv("IQUOPS_JENKINS_BUNDLE_DIR", defaultJenkinsBundleDir(root))
	return Config{
		Role:              role,
		Host:              host,
		Port:              port,
		StateDir:          stateDir,
		DatabasePath:      databasePath,
		RepositoryRoot:    root,
		PublicBaseURL:     strings.TrimSpace(os.Getenv("IQUOPS_PUBLIC_BASE_URL")),
		WorkerID:          getenv("IQUOPS_WORKER_ID", "worker-"+strconv.Itoa(os.Getpid())),
		WorkerIntervalMS:  positiveInt(os.Getenv("IQUOPS_WORKER_INTERVAL_MS"), 5000),
		WorkerRunOnce:     os.Getenv("IQUOPS_WORKER_ONCE") == "true",
		WorkerConcurrency: positiveInt(os.Getenv("IQUOPS_WORKER_CONCURRENCY"), 4),
		SwarmNetwork:      getenv("IQUOPS_SWARM_NETWORK", "iquops-public"),
		TraefikService:    getenv("IQUOPS_TRAEFIK_SERVICE", "iquops-traefik"),
		TraefikImage:      traefikImage,
		TraefikImagePullCandidates: traefikImagePullCandidates(
			os.Getenv("IQUOPS_TRAEFIK_IMAGE_PULL_CANDIDATES"),
			traefikImage,
		),
		TraefikHTTPPort:       positiveInt(os.Getenv("IQUOPS_TRAEFIK_HTTP_PORT"), 80),
		TraefikHTTPSPort:      positiveInt(os.Getenv("IQUOPS_TRAEFIK_HTTPS_PORT"), 443),
		TraefikCertResolver:   getenv("IQUOPS_TRAEFIK_CERT_RESOLVER", DefaultTraefikCertResolver),
		SwarmAdvertiseAddr:    strings.TrimSpace(os.Getenv("IQUOPS_SWARM_ADVERTISE_ADDR")),
		JenkinsBundleDir:      jenkinsDir,
		JenkinsPluginsArchive: getenv("IQUOPS_JENKINS_PLUGINS_ARCHIVE", defaultJenkinsPluginsArchive(root, jenkinsDir)),
		JenkinsBundleVersion: getenv(
			"IQUOPS_JENKINS_BUNDLE_VERSION",
			ManagedJenkinsBundleVersion,
		),
		JenkinsImage:     getenv("IQUOPS_JENKINS_IMAGE", ManagedJenkinsImage),
		JenkinsContainer: getenv("IQUOPS_JENKINS_CONTAINER", "iquops-jenkins"),
		JenkinsPort:      positiveInt(os.Getenv("IQUOPS_JENKINS_PORT"), 58080),
		FrpVersion:       frpVersion,
		FrpsImage:        getenv("IQUOPS_FRPS_IMAGE", "ghcr.io/fatedier/frps:"+frpVersion),
		FrpsPath:         strings.TrimSpace(os.Getenv("IQUOPS_FRPS_PATH")),
		FrpcPath:         strings.TrimSpace(os.Getenv("IQUOPS_FRPC_PATH")),
		FrpcDNSServer:    strings.TrimSpace(os.Getenv("IQUOPS_FRPC_DNS_SERVER")),
		FrpProbeHost:     getenv("IQUOPS_FRP_PROBE_HOST", "127.0.0.1"),
		FrpProbePort:     positiveInt(os.Getenv("IQUOPS_FRP_PROBE_PORT"), 17990),
		AgentBundleDir:   getenv("IQUOPS_AGENT_BUNDLE_DIR", filepath.Join(root, "agent", "dist")),
	}
}

// 源码命令支持从仓库根目录或 server 目录启动。
func communitySourceRoot(directory string) string {
	if _, err := os.Stat(filepath.Join(directory, "internal", "platform", "config.go")); err == nil {
		return filepath.Dir(directory)
	}
	return directory
}

func defaultJenkinsBundleDir(root string) string {
	source := filepath.Join(root, "server", "resources", "runtime", "managed", "jenkins")
	if _, err := os.Stat(source); err == nil {
		return source
	}
	return filepath.Join(root, "resources", "runtime", "managed", "jenkins")
}

const ManagedJenkinsPluginArchive = "jenkins-plugins.tar"

func defaultJenkinsPluginsArchive(root, bundleDir string) string {
	source := filepath.Join(root, "server", "resources", "runtime", "managed", "jenkins")
	if filepath.Clean(bundleDir) == source {
		return filepath.Join(root, "desktop", "runtime", ManagedJenkinsPluginArchive)
	}
	return filepath.Join(bundleDir, ManagedJenkinsPluginArchive)
}

func (c Config) ControlPlane(version string) map[string]any {
	result := map[string]any{
		"hostname":      getenv("HOSTNAME", "localhost"),
		"apiHost":       c.Host,
		"apiPort":       c.Port,
		"dataDirectory": c.StateDir,
		"version":       version,
	}
	if c.PublicBaseURL != "" {
		result["publicBaseUrl"] = c.PublicBaseURL
	}
	return result
}

func (c Config) RuntimeSummary() map[string]any {
	result := map[string]any{
		"databasePath":               c.DatabasePath,
		"memoryStore":                "process-local",
		"swarmNetwork":               c.SwarmNetwork,
		"traefikService":             c.TraefikService,
		"traefikImage":               c.TraefikImage,
		"traefikImagePullCandidates": c.TraefikImagePullCandidates,
		"traefikHttpPort":            c.TraefikHTTPPort,
		"traefikHttpsPort":           c.TraefikHTTPSPort,
		"frpVersion":                 c.FrpVersion,
		"frpsImage":                  c.FrpsImage,
		"frpProbeHost":               c.FrpProbeHost,
		"frpProbePort":               c.FrpProbePort,
	}
	if c.FrpcPath != "" {
		result["frpcPath"] = c.FrpcPath
	}
	if c.FrpcDNSServer != "" {
		result["frpcDnsServer"] = c.FrpcDNSServer
	}
	if c.FrpsPath != "" {
		result["frpsPath"] = c.FrpsPath
	}
	if c.AgentBundleDir != "" {
		result["agentBundleDir"] = c.AgentBundleDir
	}
	if c.SwarmAdvertiseAddr != "" {
		result["swarmAdvertiseAddr"] = c.SwarmAdvertiseAddr
	}
	return result
}

func traefikImagePullCandidates(raw string, primary string) []string {
	configured := splitConfigList(raw)
	if len(configured) > 0 {
		return uniqueNonEmptyStrings(configured)
	}
	if strings.TrimSpace(primary) == "" {
		primary = "traefik:v3.7"
	}
	candidates := []string{primary}
	if fallback := publicECRDockerLibraryImage(primary); fallback != "" {
		candidates = append(candidates, fallback)
	}
	if fallback := traefikGHCRImage(primary); fallback != "" {
		candidates = append(candidates, fallback)
	}
	return uniqueNonEmptyStrings(candidates)
}

func splitConfigList(raw string) []string {
	normalized := strings.NewReplacer("\n", ",", ";", ",").Replace(raw)
	parts := strings.Split(normalized, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func publicECRDockerLibraryImage(primary string) string {
	tag, ok := dockerLibraryTraefikTag(primary)
	if !ok {
		return ""
	}
	return "public.ecr.aws/docker/library/traefik:" + tag
}

func traefikGHCRImage(primary string) string {
	tag, ok := dockerLibraryTraefikTag(primary)
	if !ok {
		return ""
	}
	return "ghcr.io/traefik/traefik:" + strings.TrimPrefix(tag, "v")
}

func dockerLibraryTraefikTag(reference string) (string, bool) {
	image, tag := splitImageReference(strings.TrimSpace(reference))
	switch image {
	case "traefik", "library/traefik", "docker.io/traefik", "docker.io/library/traefik", "index.docker.io/library/traefik", "registry-1.docker.io/library/traefik":
		return tag, true
	default:
		return "", false
	}
}

func splitImageReference(reference string) (string, string) {
	if reference == "" {
		return "traefik", "v3.7"
	}
	tag := "latest"
	lastSlash := strings.LastIndex(reference, "/")
	lastColon := strings.LastIndex(reference, ":")
	if lastColon > lastSlash {
		tag = reference[lastColon+1:]
		reference = reference[:lastColon]
	}
	return reference, tag
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvAny(keys []string, fallback string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func positiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
