package application

import "strings"

type packageManagerStrategy struct {
	Name          string
	DetectScript  string
	PrepareScript []string
	InstallCall   string
	RemoveCall    string
}

func dockerPackageManagerStrategies() []packageManagerStrategy {
	return []packageManagerStrategy{
		{
			Name:          "yum",
			DetectScript:  "command -v yum >/dev/null 2>&1",
			PrepareScript: []string{"yum remove -y $conflicts >/dev/null 2>&1 || true", "yum install -y yum-utils device-mapper-persistent-data lvm2 || yum install -y yum-utils"},
			InstallCall:   "install_yum_docker yum",
			RemoveCall:    "yum remove -y $packages >/dev/null 2>&1 || true",
		},
		{
			Name:          "dnf",
			DetectScript:  "command -v dnf >/dev/null 2>&1",
			PrepareScript: []string{"dnf remove -y $conflicts >/dev/null 2>&1 || true", "dnf install -y dnf-plugins-core"},
			InstallCall:   "install_yum_docker dnf",
			RemoveCall:    "dnf remove -y $packages >/dev/null 2>&1 || true",
		},
		{
			Name:          "apt",
			DetectScript:  "command -v apt-get >/dev/null 2>&1",
			PrepareScript: []string{"apt-get remove -y $conflicts >/dev/null 2>&1 || true", "apt-get update", "DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl gnupg", "install -m 0755 -d /etc/apt/keyrings"},
			InstallCall:   "install_apt_docker",
			RemoveCall:    "DEBIAN_FRONTEND=noninteractive apt-get purge -y $packages >/dev/null 2>&1 || true",
		},
	}
}

func dockerInstallDispatchScript(strategies []packageManagerStrategy) []string {
	lines := []string{}
	for index, strategy := range strategies {
		prefix := "if"
		if index > 0 {
			prefix = "elif"
		}
		lines = append(lines, prefix+" "+strategy.DetectScript+"; then")
		for _, command := range strategy.PrepareScript {
			lines = append(lines, "  "+command)
		}
		lines = append(lines, "  "+strategy.InstallCall)
	}
	lines = append(lines,
		"else",
		"  echo PACKAGE_MANAGER_UNSUPPORTED",
		"  exit 2",
		"fi",
	)
	return lines
}

func dockerRemoveDispatchScript(strategies []packageManagerStrategy) []string {
	lines := []string{}
	for index, strategy := range strategies {
		prefix := "if"
		if index > 0 {
			prefix = "elif"
		}
		lines = append(lines, prefix+" "+strategy.DetectScript+"; then")
		lines = append(lines, "  "+strategy.RemoveCall)
	}
	lines = append(lines, "fi")
	return lines
}

func dockerPackageManagerNames(strategies []packageManagerStrategy) string {
	names := make([]string, 0, len(strategies))
	for _, strategy := range strategies {
		names = append(names, strategy.Name)
	}
	return strings.Join(names, ",")
}
