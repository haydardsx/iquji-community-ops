package application

import (
	"context"
	"errors"
	"net/http"
)

func recoverSwarmRebuild(ctx context.Context, input ServerRuntimeExecutionInput, remote swarmRebuildRemote, m swarmRebuildManifest, cp *swarmRebuildCheckpoint) error {
	if _, err := readSwarmRebuildBackup(ctx, remote, *cp); err != nil {
		return err
	}
	if err := verifySwarmRebuildData(ctx, remote, *cp); err != nil {
		return err
	}
	status, err := remote.Status(ctx)
	if err != nil {
		return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	originalID := stringValue(rebuildObject(m.Cluster)["ID"])
	if status.Active && status.ControlAvailable && status.ClusterID == originalID {
		// 退出之前的中断只恢复网关，不重建仍然存在的原 Swarm。
		if status.NodeID != stringValue(rebuildObject(m.Node)["ID"]) {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if err := waitSwarmRebuildDetachedGateway(ctx, remote, m); err != nil {
			return err
		}
		current, err := readSwarmRebuildManifest(ctx, remote.Call, input, m.Local)
		if err != nil || !swarmRebuildBaselineEqual(m, current) {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if len(current.Gateway) > 0 && (len(m.Gateway) == 0 || swarmValueHash(rebuildObject(current.Gateway)["Spec"]) != swarmValueHash(rebuildObject(m.Gateway)["Spec"])) {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if len(current.Gateway) == 0 && len(m.Gateway) > 0 {
			mapping := map[string]string{}
			for _, raw := range m.Networks {
				network := rebuildObject(raw)
				id := stringValue(network["Id"])
				mapping[id], mapping[stringValue(network["Name"])] = id, id
			}
			spec, err := swarmRebuildGatewaySpec(m, status.NodeID, mapping)
			if err != nil {
				return err
			}
			if _, err := remote.Call(ctx, http.MethodPost, "/services/create", spec); err != nil {
				return err
			}
		}
		cp.NewClusterID, cp.NewNodeID, cp.Phase = status.ClusterID, status.NodeID, "recovering"
		if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
			return err
		}
		if err := waitVerifySwarmRebuild(ctx, remote, m, *cp, m.Local); err != nil {
			return err
		}
		return commitSwarmRebuild(ctx, input, remote, cp, "recovered")
	}
	if status.Active {
		if !status.ControlAvailable {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		raw, err := remote.Call(ctx, http.MethodGet, "/swarm", nil)
		if err != nil {
			return err
		}
		cluster := rebuildObject(raw)
		owner := stringValue(objectMapFromAny(objectMapFromAny(cluster["Spec"])["Labels"])[swarmRebuildOwnerLabel])
		if status.ClusterID != cp.NewClusterID && owner != cp.BackupID {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if err := waitSwarmRebuildDetachedGateway(ctx, remote, m); err != nil {
			return err
		}
		current, err := readSwarmRebuildManifest(ctx, remote.Call, input, m.Local)
		if err != nil {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if !swarmRebuildOwnedNetworksSafe(m, current) {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if len(current.Gateway) > 0 {
			mapping := map[string]string{}
			for _, oldRaw := range m.Networks {
				old := rebuildObject(oldRaw)
				for _, newRaw := range current.Networks {
					network := rebuildObject(newRaw)
					if network["Name"] == old["Name"] {
						id := stringValue(network["Id"])
						mapping[stringValue(old["Id"])], mapping[stringValue(old["Name"])] = id, id
					}
				}
			}
			expected, err := swarmRebuildGatewaySpec(m, status.NodeID, mapping)
			if err != nil || swarmValueHash(expected) != swarmValueHash(objectMapFromAny(rebuildObject(current.Gateway)["Spec"])) {
				return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
			}
		}
		cp.Phase = "recovering"
		if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
			return err
		}
		if err := stopSwarmRebuildGateway(ctx, remote, current); err != nil {
			return err
		}
		if _, err := remote.Call(ctx, http.MethodPost, "/swarm/leave?force=1", nil); err != nil {
			return err
		}
	} else if status.Status != "inactive" {
		return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	if cp.DataHash == "" {
		return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	if err := waitSwarmRebuildOverlaysRemoved(ctx, remote); err != nil {
		return err
	}
	status, err = initSwarmRebuild(ctx, remote, m, m.Local)
	if err != nil {
		return err
	}
	cp.NewClusterID, cp.NewNodeID, cp.Phase = status.ClusterID, status.NodeID, "recovering"
	if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
		return err
	}
	if err := restoreSwarmRebuildData(ctx, remote, *cp); err != nil {
		return err
	}
	if _, err := restoreSwarmRebuildResources(ctx, remote, m, status.NodeID); err != nil {
		return err
	}
	if err := waitVerifySwarmRebuild(ctx, remote, m, *cp, m.Local); err != nil {
		return err
	}
	return commitSwarmRebuild(ctx, input, remote, cp, "recovered")
}
