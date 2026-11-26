package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type maintenanceGuardKey struct{}

// WithMaintenanceGuard 让正式维护在写事务内重新检查预检状态；底层测试可不启用。
func WithMaintenanceGuard(ctx context.Context, stateDigest string) context.Context {
	return context.WithValue(ctx, maintenanceGuardKey{}, stateDigest)
}

func checkMaintenanceGuard(ctx context.Context, conn *sql.Conn) error {
	expected, ok := ctx.Value(maintenanceGuardKey{}).(string)
	if !ok {
		return nil
	}
	state, err := loadStateConn(ctx, conn)
	if err != nil {
		return err
	}
	for _, task := range state.Tasks {
		if domain.IsTaskActive(task.Status) {
			return errors.New("DATA_MAINTENANCE_ACTIVE_TASK")
		}
	}
	for _, run := range state.JenkinsReleaseRuns {
		switch domain.StringValue(run["status"]) {
		case "pending", "triggered", "callback_received", "cleanup_required":
			return errors.New("DATA_MAINTENANCE_ACTIVE_JENKINS_RUN")
		}
	}
	for _, run := range state.FrpTunnelRuns {
		if run.Status.IsActive() {
			return errors.New("DATA_MAINTENANCE_ACTIVE_FRP_TUNNEL")
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(encoded)
	if hex.EncodeToString(hash[:]) != expected {
		return errors.New("DATA_MAINTENANCE_STATE_CHANGED")
	}
	return nil
}
