package application

import (
	"strings"
	"testing"
)

func TestDockerPackageManagerStrategiesCoverSupportedManagers(t *testing.T) {
	strategies := dockerPackageManagerStrategies()
	names := dockerPackageManagerNames(strategies)
	for _, name := range []string{"yum", "dnf", "apt"} {
		if !strings.Contains(names, name) {
			t.Fatalf("strategy names %q missing %s", names, name)
		}
	}

	installDispatch := strings.Join(dockerInstallDispatchScript(strategies), "\n")
	if !strings.Contains(installDispatch, "install_yum_docker yum") {
		t.Fatal("yum install strategy missing")
	}
	if !strings.Contains(installDispatch, "install_yum_docker dnf") {
		t.Fatal("dnf install strategy missing")
	}
	if !strings.Contains(installDispatch, "install_apt_docker") {
		t.Fatal("apt install strategy missing")
	}

	removeDispatch := strings.Join(dockerRemoveDispatchScript(strategies), "\n")
	if !strings.Contains(removeDispatch, "yum remove -y $packages") {
		t.Fatal("yum remove strategy missing")
	}
	if !strings.Contains(removeDispatch, "dnf remove -y $packages") {
		t.Fatal("dnf remove strategy missing")
	}
	if !strings.Contains(removeDispatch, "apt-get purge -y $packages") {
		t.Fatal("apt remove strategy missing")
	}
}

func TestDockerInstallScriptUsesPackageManagerStrategyDispatch(t *testing.T) {
	script := dockerInstallScript()
	if !strings.Contains(script, "supported_package_managers='yum,dnf,apt'") {
		t.Fatal("install script should advertise strategy managers")
	}
	if !strings.Contains(script, "if command -v yum") || !strings.Contains(script, "elif command -v dnf") || !strings.Contains(script, "elif command -v apt-get") {
		t.Fatal("install script should dispatch supported package manager strategies")
	}
}
