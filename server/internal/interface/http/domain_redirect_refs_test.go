package httpapi

import (
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"net/http"
	"testing"
)

func TestDomainEditingAndDeletionReturnDistinctRedirectReferences(t *testing.T) {
	api := newTestAPI(t)
	edge := domain.Object{"targetDomainId": "b"}
	if err := api.store.MutateState(t.Context(), func(state *domain.State) error {
		state.Domains = []domain.Object{
			{"id": "b", "name": "b.example.test"},
			{"id": "a", "name": "a.example.test", "redirect": edge, "redirectRuntime": domain.Object{"lastApplied": domain.Object{"redirect": edge}}},
			{"id": "c", "name": "c.example.test", "redirectRuntime": domain.Object{"pendingRemote": domain.Object{"redirect": edge}}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var body any
		if method == http.MethodPut {
			body = map[string]any{"name": "renamed.example.test"}
		}
		res := api.request(t, method, "/secrets/domains/b", body)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("%s: HTTP %d", method, res.StatusCode)
		}
		received := decodeTestJSON[struct {
			Code    string `json:"errorCode"`
			Details struct {
				Rules []struct{ SourceID, SourceName, TargetID, TargetName string } `json:"affectedRedirects"`
			}
		}](t, res)
		if received.Code != "DOMAIN_REDIRECT_TARGET_IN_USE" || len(received.Details.Rules) != 2 {
			t.Fatal("响应中的冲突码或受影响规则数不正确")
		}
		for i, rule := range received.Details.Rules {
			id := []string{"a", "c"}[i]
			if rule.SourceID != id || rule.SourceName != id+".example.test" || rule.TargetID != "b" || rule.TargetName != "b.example.test" {
				t.Fatal("受影响规则的源或目标不正确")
			}
		}
	}
}
