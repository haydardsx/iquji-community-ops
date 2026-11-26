package application

import (
	"errors"
	"regexp"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var gitCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func checkJenkinsCommit(plan domain.Object, sha string) error {
	source := objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["sourceSnapshot"])
	expected := firstNonEmptyString(stringValue(plan["commitSha"]), stringValue(source["commitSha"]))
	if source["refType"] == "commit" && expected == "" {
		expected = stringValue(source["ref"])
	}
	if !gitCommitSHA.MatchString(sha) || (expected != "" && expected != sha) {
		return errors.New("JENKINS_COMMIT_MISMATCH")
	}
	return nil
}

var gitBuildGroupTag = regexp.MustCompile(`^(0|[1-9][0-9]*)-(amd64|arm64)$`)

// 只登记与本次发布、目标服务器和槽位一致的镜像读回结果。
func verifiedGitArtifactFacts(raw any, plan domain.Object, runID string) ([]domain.Object, error) {
	invalid := errors.New("JENKINS_RELEASE_ARTIFACT_INVALID")
	snapshot := objectMapFromAny(plan["buildSnapshot"])
	swarm := objectMapFromAny(snapshot["swarmRuntime"])
	services := objectSlice(swarm["services"])
	facts := objectSlice(raw)
	if len(facts) == 0 || len(facts) != len(services) {
		return nil, invalid
	}
	sync := registrySyncConfig(snapshot)
	platforms, frozen := snapshot["slotPlatforms"]
	prefix := "iquops/" + strings.ToLower(stringValue(plan["applicationId"])) + ":" + strings.ToLower(runID) + "-"
	seen := map[string]bool{}
	for _, fact := range facts {
		slot := stringValue(fact["serviceSlotKey"])
		svc := findObjectBy(services, "slotKey", slot)
		platform := stringValue(fact["platform"])
		suffix, belongs := strings.CutPrefix(stringValue(fact["image"]), prefix)
		match := gitBuildGroupTag.FindStringSubmatch(suffix)
		if !objectOnlyContainsKeys(fact, "serviceSlotKey", "serverId", "platform", "image", "imageDigest", "configDigest", "manifestDigest", "registryImage", "registryDigest") || svc == nil || svc["serverId"] != fact["serverId"] || slot == "" || seen[slot] || !belongs || len(match) != 3 || platform != "linux/"+match[2] {
			return nil, invalid
		}
		for _, key := range []string{"imageDigest", "configDigest", "manifestDigest"} {
			if !contentDigest.MatchString(stringValue(fact[key])) {
				return nil, invalid
			}
		}
		if fact["imageDigest"] != fact["configDigest"] && fact["imageDigest"] != fact["manifestDigest"] {
			return nil, invalid
		}
		if sync["enabled"] == true {
			if frozen && objectMapFromAny(platforms)[slot] != platform {
				return nil, invalid
			}
			expected := stringValue(sync["imageRepository"]) + ":" + strings.ToLower(runID) + "-" + suffix
			if fact["registryImage"] != expected || !contentDigest.MatchString(stringValue(fact["registryDigest"])) {
				return nil, invalid
			}
		} else if fact["registryImage"] != nil || fact["registryDigest"] != nil {
			return nil, invalid
		}
		seen[slot] = true
	}
	return facts, nil
}
