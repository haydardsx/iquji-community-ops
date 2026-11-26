package application

import (
	"regexp"
	"strconv"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var jenkinsBundleRevision = regexp.MustCompile(`^v([0-9]+)-jenkins-`)

func jenkinsRuntimeBundleRevision(server domain.Object) int {
	match := jenkinsBundleRevision.FindStringSubmatch(stringValue(serverRuntimeMetrics(server)["jenkinsBundleVersion"]))
	if len(match) != 2 {
		return 0
	}
	revision, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return revision
}
