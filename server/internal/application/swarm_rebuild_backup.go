package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const swarmRebuildBackupRoot = "/opt/iquops/backups/swarm-rebuild"

type swarmRebuildCheckpoint struct {
	Version      int    `json:"version"`
	BackupID     string `json:"backupId"`
	ManifestHash string `json:"manifestHash"`
	Phase        string `json:"phase"`
	DataHash     string `json:"dataHash"`
	NewClusterID string `json:"newClusterId"`
	NewNodeID    string `json:"newNodeId"`
}

type swarmRebuildRemote struct {
	Call      swarmEngineRequest
	Status    func(context.Context) (remoteSwarmStatus, error)
	Local     func(context.Context) (swarmLocalConnection, error)
	Output    func(context.Context, string) (string, error)
	Write     func(context.Context, string, []byte) error
	Reconnect func(context.Context) (swarmRebuildRemote, func(), error)
}

func validateSwarmRebuildCheckpointObject(object domain.Object) error {
	allowed := map[string]bool{"version": true, "backupId": true, "manifestHash": true, "phase": true, "dataHash": true, "newClusterId": true, "newNodeId": true}
	for key := range object {
		if !allowed[key] {
			return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
		}
	}
	raw, err := json.Marshal(object)
	var cp swarmRebuildCheckpoint
	if err != nil || json.Unmarshal(raw, &cp) != nil || cp.Version != 1 || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(cp.BackupID) || !rebuildHashValid(cp.ManifestHash) || len(cp.NewClusterID) > 128 || len(cp.NewNodeID) > 128 {
		return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
	}
	switch cp.Phase {
	case "prepared", "backed_up", "leaving", "initialized", "restored", "verified", "recovering", "recovered":
	default:
		return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
	}
	if cp.DataHash != "" && cp.DataHash != "none" && !rebuildHashValid(cp.DataHash) {
		return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
	}
	if cp.Phase != "prepared" && cp.Phase != "recovering" && cp.Phase != "recovered" && cp.DataHash == "" {
		return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
	}
	return nil
}

func rebuildHashValid(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
func rebuildBytesHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
func rebuildBackupPath(id string) string { return swarmRebuildBackupRoot + "/" + id }

func saveSwarmRebuildCheckpoint(ctx context.Context, input ServerRuntimeExecutionInput, cp swarmRebuildCheckpoint) error {
	raw, _ := json.Marshal(cp)
	var object domain.Object
	_ = json.Unmarshal(raw, &object)
	if validateSwarmRebuildCheckpointObject(object) != nil || input.SaveSwarmRebuildCheckpoint == nil {
		return errors.New("SWARM_REBUILD_CHECKPOINT_FAILED")
	}
	if err := input.SaveSwarmRebuildCheckpoint(ctx, object); err != nil {
		return swarmOperationError(ctx, err, "SWARM_REBUILD_CHECKPOINT_FAILED")
	}
	return nil
}

func prepareSwarmRebuildBackup(ctx context.Context, remote swarmRebuildRemote, manifest swarmRebuildManifest) (swarmRebuildCheckpoint, error) {
	cp := swarmRebuildCheckpoint{Version: 1, BackupID: manifest.BackupID, Phase: "prepared"}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return cp, errors.New("SWARM_REBUILD_BACKUP_FAILED")
	}
	cp.ManifestHash = rebuildBytesHash(raw)
	dir := rebuildBackupPath(cp.BackupID)
	// 目录只创建一次，防止重试将降级现状覆盖为原始备份。
	command := "set -eu\numask 077\ntest \"$(id -u)\" = 0\n"
	for _, directory := range []string{"/opt/iquops", "/opt/iquops/backups", swarmRebuildBackupRoot} {
		command += "test ! -L " + shellQuote(directory) + "\nif [ -e " + shellQuote(directory) + " ]; then test \"$(stat -c %u " + shellQuote(directory) + ")\" = 0; fi\n"
	}
	command += "install -d -m 0700 " + shellQuote("/opt/iquops/backups") + " " + shellQuote(swarmRebuildBackupRoot) + "\nmkdir -m 0700 " + shellQuote(dir)
	if _, err = remote.Output(ctx, command); err != nil {
		// 若进程在保存检查点之前退出，只复用完全相同且校验通过的原始文件。
		if _, readErr := readSwarmRebuildBackup(ctx, remote, cp); readErr == nil {
			return cp, nil
		}
		return cp, errors.New("SWARM_REBUILD_BACKUP_FAILED")
	}
	if err = remote.Write(ctx, dir+"/manifest.json.tmp", raw); err != nil {
		return cp, errors.New("SWARM_REBUILD_BACKUP_FAILED")
	}
	if _, err = remote.Output(ctx, "set -eu\nchmod 0600 "+shellQuote(dir+"/manifest.json.tmp")+"\nmv "+shellQuote(dir+"/manifest.json.tmp")+" "+shellQuote(dir+"/manifest.json")); err != nil {
		return cp, errors.New("SWARM_REBUILD_BACKUP_FAILED")
	}
	if _, err = readSwarmRebuildBackup(ctx, remote, cp); err != nil {
		return cp, err
	}
	return cp, nil
}

func readSwarmRebuildBackup(ctx context.Context, remote swarmRebuildRemote, cp swarmRebuildCheckpoint) (swarmRebuildManifest, error) {
	var manifest swarmRebuildManifest
	dir := rebuildBackupPath(cp.BackupID)
	command := "set -eu\n"
	for _, directory := range []string{"/opt/iquops", "/opt/iquops/backups", swarmRebuildBackupRoot, dir} {
		command += "test ! -L " + shellQuote(directory) + "\ntest \"$(stat -c %u " + shellQuote(directory) + ")\" = 0\n"
	}
	command += "test \"$(stat -c %a " + shellQuote(swarmRebuildBackupRoot) + ")\" = 700\ntest \"$(stat -c %a " + shellQuote(dir) + ")\" = 700\ntest -f " + shellQuote(dir+"/manifest.json") + "\ntest ! -L " + shellQuote(dir+"/manifest.json") + "\ntest \"$(stat -c %u " + shellQuote(dir+"/manifest.json") + ")\" = 0\ntest \"$(stat -c %a " + shellQuote(dir+"/manifest.json") + ")\" = 600\ncat -- " + shellQuote(dir+"/manifest.json")
	output, err := remote.Output(ctx, command)
	if err != nil || rebuildBytesHash([]byte(output)) != cp.ManifestHash || json.Unmarshal([]byte(output), &manifest) != nil || manifest.Version != 1 || manifest.BackupID != cp.BackupID {
		return manifest, errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	return manifest, nil
}

func backupSwarmRebuildData(ctx context.Context, remote swarmRebuildRemote, cp swarmRebuildCheckpoint, hasGateway bool) (string, error) {
	if !hasGateway {
		return "none", nil
	}
	dir := rebuildBackupPath(cp.BackupID)
	archive := dir + "/traefik.tar"
	command := strings.Join([]string{"set -eu", "umask 077", "test -d " + shellQuote(traefikRemoteRoot), "test ! -L " + shellQuote(traefikRemoteRoot), "test -z \"$(find " + shellQuote(traefikRemoteRoot) + " ! -type f ! -type d -print -quit)\"", "test -z \"$(find " + shellQuote(traefikRemoteRoot) + " -type f \\( -name '*.staged' -o -name 'certificate-probe-*' \\) -print -quit)\"", "tar -C /opt/iquops -cpf " + shellQuote(archive+".tmp") + " traefik", "chmod 0600 " + shellQuote(archive+".tmp"), "tar -C /opt/iquops -df " + shellQuote(archive+".tmp") + " >/dev/null", "mv " + shellQuote(archive+".tmp") + " " + shellQuote(archive), "sha256sum " + shellQuote(archive) + " | cut -d ' ' -f 1"}, "\n")
	output, err := remote.Output(ctx, command)
	if err != nil || !rebuildHashValid(output) {
		return "", errors.New("SWARM_REBUILD_BACKUP_FAILED")
	}
	return output, nil
}

func verifySwarmRebuildData(ctx context.Context, remote swarmRebuildRemote, cp swarmRebuildCheckpoint) error {
	if cp.DataHash == "none" || cp.DataHash == "" {
		return nil
	}
	archive := rebuildBackupPath(cp.BackupID) + "/traefik.tar"
	command := "set -eu\ntest ! -L " + shellQuote(archive) + "\ntest \"$(stat -c %a " + shellQuote(archive) + ")\" = 600\ntest \"$(sha256sum " + shellQuote(archive) + " | cut -d ' ' -f 1)\" = " + shellQuote(cp.DataHash) + "\ntar -tf " + shellQuote(archive) + " >/dev/null"
	if _, err := remote.Output(ctx, command); err != nil {
		return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	return nil
}

func restoreSwarmRebuildData(ctx context.Context, remote swarmRebuildRemote, cp swarmRebuildCheckpoint) error {
	if err := verifySwarmRebuildData(ctx, remote, cp); err != nil {
		return err
	}
	if cp.DataHash == "none" || cp.DataHash == "" {
		return nil
	}
	archive := rebuildBackupPath(cp.BackupID) + "/traefik.tar"
	// 调用者保证网关已停止；只恢复受管数据根，不操作 Docker 数据目录或独立容器。
	if _, err := remote.Output(ctx, "set -eu\ntar -C /opt/iquops -xpf "+shellQuote(archive)+"\ntar -C /opt/iquops -df "+shellQuote(archive)+" >/dev/null"); err != nil {
		return errors.New("SWARM_REBUILD_BACKUP_FAILED")
	}
	return nil
}
