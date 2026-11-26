package application

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var contentDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var canonicalImage = regexp.MustCompile(`^([a-z0-9]|\[[0-9a-f:]+\])[a-z0-9./:_-]*@sha256:[0-9a-f]{64}$`)

var imagePlatform = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9_]+$`)

func registryRepository(value string) string {
	repo, _, _ := strings.Cut(value, "@")
	if last := strings.LastIndex(repo, ":"); last > strings.LastIndex(repo, "/") {
		repo = repo[:last]
	}
	first, _, nested := strings.Cut(repo, "/")
	if !nested {
		return "docker.io/library/" + repo
	}
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		repo = "docker.io/" + repo
	}
	if canonical, ok := strings.CutPrefix(repo, "index.docker.io/"); ok {
		repo = "docker.io/" + canonical
	}
	if strings.HasPrefix(repo, "docker.io/") && strings.Count(repo, "/") == 1 {
		repo = "docker.io/library/" + strings.TrimPrefix(repo, "docker.io/")
	}
	return repo
}

func matchingImageDigest(reference string, digests []string) string {
	_, expected, pinned := strings.Cut(reference, "@")
	repo := registryRepository(reference)
	for _, item := range digests {
		_, digest, _ := strings.Cut(item, "@")
		if canonicalImage.MatchString(item) && registryRepository(item) == repo && (!pinned || digest == expected) {
			return item
		}
	}
	return ""
}

func packageInstallCommand(provider, name, version string) string {
	switch provider {
	case "apt":
		return "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " + shellQuote(name+"="+version) + " && dpkg-query -W -f='${Version}' " + shellQuote(name) + " > /iquops-package-version && rm -rf /var/lib/apt/lists/*"
	case "dnf", "yum":
		return provider + " install -y " + shellQuote(name+"-"+version) + " && rpm -q --qf '%{VERSION}-%{RELEASE}' " + shellQuote(name) + " > /iquops-package-version && " + provider + " clean all"
	case "npm":
		return "npm install -g " + shellQuote(name+"@"+version) + " && node -p " + shellQuote("require(process.argv[1]).version") + " \"$(npm root -g)/" + name + "/package.json\" > /iquops-package-version"
	case "pypi":
		return "python3 -m pip install --no-cache-dir " + shellQuote(name+"=="+version) + " && python3 -c " + shellQuote("import importlib.metadata as m;print(m.version("+shellQuote(name)+"))") + " > /iquops-package-version"
	}
	return ""
}

func buildContainerPackage(base string, source domain.Object, values map[string]string) ([]byte, string, error) {
	command, ok := stringArguments(source["command"])
	if !ok {
		return nil, "", errors.New("APPLICATION_PACKAGE_COMMAND_REQUIRED")
	}
	install := packageInstallCommand(stringValue(source["provider"]), stringValue(source["packageName"]), stringValue(source["version"]))
	if install == "" {
		return nil, "", errors.New("APPLICATION_SOURCE_PROVIDER_INVALID")
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(file string, content []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: file, Mode: 0600, Size: int64(len(content))}); err != nil {
			return err
		}
		_, err := tw.Write(content)
		return err
	}
	var flags, mounts, exports []string
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if !applicationVariableName.MatchString(k) {
			return nil, "", errors.New("APPLICATION_VARIABLE_KEY_INVALID")
		}
		id := fmt.Sprintf("input%d", i)
		if err := add(id, []byte(values[k])); err != nil {
			return nil, "", err
		}
		flags = append(flags, "--secret id="+id+",src="+id)
		mounts = append(mounts, "--mount=type=secret,id="+id+",target=/run/secrets/"+k+",required=true")
		// 在命令替换前添加哨兵，保留凭据末尾换行。
		exports = append(exports, k+"=$(cat /run/secrets/"+k+"; printf x); export "+k+"=\"${"+k+"%x}\";")
	}
	end, _ := json.Marshal(command)
	recipe := "FROM " + base + "\nRUN " + strings.Join(mounts, " ") + " " + strings.Join(exports, " ") + " " + install + "\n"
	if source["shellEntrypoint"] == true {
		recipe += "ENTRYPOINT []\n"
	}
	recipe += "CMD " + string(end) + "\n"
	if err := add("context/Dockerfile", []byte(recipe)); err != nil {
		return nil, "", err
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), strings.Join(flags, " "), nil
}

func prepareContainerArtifact(ctx context.Context, p containerPeer, source domain.Object, releaseID, slot, base string, build map[string]string) (domain.Object, error) {
	// 固定仓库摘要后，其他节点只按同一清单获取自己的架构变体。
	original := base
	base, err := imagePullReference(base, source["registryMirrorUrl"])
	if err != nil {
		return nil, err
	}
	raw, err := p.call(ctx, "docker pull "+shellQuote(base)+" > /dev/null && docker image inspect "+shellQuote(base)+" --format '{{.Id}}|{{.Os}}/{{.Architecture}}|{{json .RepoDigests}}'", nil, "APPLICATION_IMAGE_PULL_FAILED")
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(raw, "|", 3)
	var digests []string
	if len(parts) != 3 || !contentDigest.MatchString(parts[0]) || !imagePlatform.MatchString(parts[1]) || json.Unmarshal([]byte(parts[2]), &digests) != nil {
		return nil, errors.New("APPLICATION_IMAGE_DIGEST_UNAVAILABLE")
	}
	canonical := matchingImageDigest(base, digests)
	if canonical == "" {
		return nil, errors.New("APPLICATION_IMAGE_DIGEST_MISMATCH")
	}
	res := domain.Object{"provider": source["provider"], "image": canonical, "baseImageDigest": canonical, "imageDigest": parts[0], "platform": parts[1]}
	if source["provider"] == "image" {
		if base != original {
			_, digest, _ := strings.Cut(canonical, "@")
			res["baseImageDigest"] = registryRepository(original) + "@" + digest
		}
		return res, nil
	}
	files, flags, err := buildContainerPackage(canonical, source, build)
	if err != nil {
		return nil, err
	}
	tag := "iquops-package:" + shortHash(releaseID+"-"+slot)
	cmd := "set -eu; umask 077; dir=$(mktemp -d); trap 'rm -rf \"$dir\"' EXIT; cd \"$dir\"; tar -xf -; DOCKER_BUILDKIT=1 docker build --no-cache " + flags + " -t " + shellQuote(tag) + " context >/dev/null 2>&1; docker image inspect " + shellQuote(tag) + " --format '{{.Id}}'; docker run --rm --network none --read-only --cap-drop ALL --entrypoint cat " + shellQuote(tag) + " /iquops-package-version"
	raw, err = p.call(ctx, cmd, files, "APPLICATION_PACKAGE_BUILD_FAILED")
	if err != nil {
		return nil, err
	}
	id, version, ok := strings.Cut(raw, "\n")
	if !ok || !contentDigest.MatchString(id) || !exactPackageVersion.MatchString(version) {
		return nil, errors.New("APPLICATION_PACKAGE_READBACK_FAILED")
	}
	res["image"], res["imageDigest"], res["packageName"], res["packageVersion"] = tag, id, source["packageName"], version
	return res, nil
}
