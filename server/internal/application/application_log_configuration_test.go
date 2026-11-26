package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestApplicationLogConfigurationIsNotAWriteInterface(t *testing.T) {
	if err := validateManagedDeploymentConfigInput(domain.Object{"runtimeLogConfig": domain.Object{"provider": "docker_container_logs"}}); err == nil || err.Error() != "APPLICATION_DEPLOYMENT_CONFIG_INVALID" {
		t.Fatalf("顶层配置仍接受手工日志协议: %v", err)
	}
	config := defaultDeploymentConfig("app_example", "project_example", []string{"server_example"})
	if _, exists := config["runtimeLogConfig"]; exists {
		t.Fatal("默认配置仍产生未执行的手工日志选项")
	}
}
