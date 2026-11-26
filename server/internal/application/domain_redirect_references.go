package application

import "github.com/haydardsx/iquji-community-ops/server/internal/domain"

type DomainRedirectReference struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	TargetID   string `json:"targetId"`
	TargetName string `json:"targetName"`
}

type DomainRedirectUsageError struct{ Rules []DomainRedirectReference }

func (*DomainRedirectUsageError) Error() string { return "DOMAIN_REDIRECT_TARGET_IN_USE" }

func domainRedirectReferences(state domain.State, id string) error {
	var refs []DomainRedirectReference
	for _, source := range state.Domains {
		sourceID := stringValue(source["id"])
		seen := map[string]bool{}
		for _, rule := range redirectBindings(source) {
			targetID := stringValue(rule["targetDomainId"])
			if targetID == "" || seen[targetID] || (sourceID != id && targetID != id) {
				continue
			}
			seen[targetID] = true
			target := findObject(state.Domains, targetID)
			refs = append(refs, DomainRedirectReference{
				SourceID: sourceID, SourceName: stringValue(source["name"]),
				TargetID: targetID, TargetName: firstNonEmptyString(stringValue(target["name"]), stringValue(rule["targetHost"])),
			})
		}
	}
	if len(refs) > 0 {
		return &DomainRedirectUsageError{Rules: refs}
	}
	return nil
}
