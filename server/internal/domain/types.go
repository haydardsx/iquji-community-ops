package domain

import "encoding/json"

type TaskAction string

const (
	ManagedJenkinsFolder      = "iquops"
	ManagedJenkinsJob         = "iquops-standard-swarm"
	ManagedJenkinsJobTemplate = "iquops-standard-swarm"

	TaskActionStartFrpTunnel            TaskAction = "start_frp_tunnel"
	TaskActionStopFrpTunnel             TaskAction = "stop_frp_tunnel"
	TaskActionDeleteFrpTunnel           TaskAction = "delete_frp_tunnel"
	TaskActionInstallDockerEngine       TaskAction = "install_docker_engine"
	TaskActionUninstallDockerEngine     TaskAction = "uninstall_docker_engine"
	TaskActionInstallDockerSwarm        TaskAction = "install_docker_swarm"
	TaskActionUninstallDockerSwarm      TaskAction = "uninstall_docker_swarm"
	TaskActionReconfigureDockerSwarm    TaskAction = "reconfigure_docker_swarm"
	TaskActionRebuildDockerSwarm        TaskAction = "rebuild_docker_swarm"
	TaskActionRecoverDockerSwarmRebuild TaskAction = "recover_docker_swarm_rebuild"
	TaskActionInstallTraefikGateway     TaskAction = "install_traefik_gateway"
	TaskActionUninstallTraefikGateway   TaskAction = "uninstall_traefik_gateway"
	TaskActionInstallFrps               TaskAction = "install_frps"
	TaskActionUninstallFrps             TaskAction = "uninstall_frps"
	TaskActionInstallJenkins            TaskAction = "install_jenkins"
	TaskActionUninstallJenkins          TaskAction = "uninstall_jenkins"
	TaskActionBindJenkinsAccess         TaskAction = "bind_jenkins_access"
	TaskActionUnbindJenkinsAccess       TaskAction = "unbind_jenkins_access"
	TaskActionInstallMonitoringAgent    TaskAction = "install_monitoring_agent"
	TaskActionUninstallMonitoringAgent  TaskAction = "uninstall_monitoring_agent"
	TaskActionUninstallApplication      TaskAction = "uninstall_application"
	TaskActionRequestJenkinsRelease     TaskAction = "request_jenkins_release"
	TaskActionDeployApplication         TaskAction = "deploy_application"
	TaskActionRestoreBackup             TaskAction = "restore_backup"
	TaskActionRestoreBackupOverwrite    TaskAction = "restore_backup_overwrite"
	TaskActionVerifyCertificate         TaskAction = "verify_certificate"
	TaskActionCreateSecretAsset         TaskAction = "create_secret_asset"
	TaskActionRotateSecretAsset         TaskAction = "rotate_secret_asset"
)

type Project struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	ServerIDs   []string `json:"serverIds,omitempty"`
	CreatedAt   string   `json:"createdAt,omitempty"`
	UpdatedAt   string   `json:"updatedAt,omitempty"`
}

type Application struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type Server struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	PublicHost      string          `json:"publicHost"`
	PrivateHost     string          `json:"privateHost,omitempty"`
	Port            int             `json:"port,omitempty"`
	SSHUser         string          `json:"sshUser,omitempty"`
	SecretAssetIDs  []string        `json:"secretAssetIds,omitempty"`
	PlatformProfile PlatformProfile `json:"platformProfile,omitempty"`
	CreatedAt       string          `json:"createdAt,omitempty"`
	UpdatedAt       string          `json:"updatedAt,omitempty"`
}

type PlatformProfile struct {
	OSID             string `json:"osId,omitempty"`
	OSVersion        string `json:"osVersion,omitempty"`
	Architecture     string `json:"architecture,omitempty"`
	PackageManager   string `json:"packageManager,omitempty"`
	ServiceManager   string `json:"serviceManager,omitempty"`
	DockerRepoMirror string `json:"dockerRepoMirror,omitempty"`
	OverrideSource   string `json:"overrideSource,omitempty"`
	DetectedAt       string `json:"detectedAt,omitempty"`
}

type SecretAsset struct {
	ID            string `json:"id"`
	AssetType     string `json:"assetType"`
	Purpose       string `json:"purpose,omitempty"`
	Name          string `json:"name"`
	SecretRef     string `json:"secretRef,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	MaskedSummary string `json:"maskedSummary,omitempty"`
	CreatedAt     string `json:"createdAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type Route struct {
	ID            string   `json:"id"`
	ApplicationID string   `json:"applicationId"`
	Status        string   `json:"status,omitempty"`
	Domains       []Object `json:"domains,omitempty"`
	Service       Object   `json:"service,omitempty"`
	Rules         []Object `json:"rules,omitempty"`
	CreatedAt     string   `json:"createdAt,omitempty"`
	UpdatedAt     string   `json:"updatedAt,omitempty"`
}

type ReleasePlan struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Status        string `json:"status,omitempty"`
	CreatedAt     string `json:"createdAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type MonitoringReport struct {
	ServerID        string   `json:"serverId"`
	Version         string   `json:"version"`
	ProtocolVersion int      `json:"protocolVersion"`
	SampledAt       string   `json:"sampledAt"`
	Capabilities    []string `json:"capabilities"`
	Samples         []Object `json:"samples"`
	SwarmSnapshot   Object   `json:"swarmSnapshot,omitempty"`
	LastError       string   `json:"lastError,omitempty"`
}

func DecodeObject[T any](item Object) (T, error) {
	var result T
	data, err := json.Marshal(item)
	if err != nil {
		return result, err
	}
	return result, json.Unmarshal(data, &result)
}

func EncodeObject(value any) (Object, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result Object
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}
