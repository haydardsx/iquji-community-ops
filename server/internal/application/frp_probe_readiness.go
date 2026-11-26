package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func frpProbeProof(token, runID, mapping, nonce string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = fmt.Fprintf(mac, "iquops-frp-probe\n%s\n%s\n%s", runID, mapping, nonce)
	return nonce + "." + hex.EncodeToString(mac.Sum(nil))
}

func frpProbeCheckResponse(proof, token, runID, mapping string) string {
	nonce, _, ok := strings.Cut(proof, ".")
	timestamp, _, _ := strings.Cut(nonce, "_")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now().Unix()
	if !ok || err != nil || seconds < now-120 || seconds > now+10 ||
		!hmac.Equal([]byte(proof), []byte(frpProbeProof(token, runID, mapping, nonce))) {
		return ""
	}
	return frpProbeProof(token, runID, mapping, nonce+"-ok")
}

func verifyPublicFrpProbe(ctx context.Context, publicURL string, input FrpTunnelExecutionInput, mapping string) error {
	client := httpClientForReadinessProbe(publicURL)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return retryFrpEndpoint(ctx, 10, 700*time.Millisecond, func() error {
		nonce := strconv.FormatInt(time.Now().Unix(), 10) + "_" + domain.NewID("check")
		proof := frpProbeProof(input.Token, input.Run.ID, mapping, nonce)
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, publicURL, nil)
		if err != nil {
			return err
		}
		request.Header.Set(frpProbeCheckHeader, proof)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		expected := frpProbeProof(input.Token, input.Run.ID, mapping, nonce+"-ok")
		if response.StatusCode == http.StatusNoContent && hmac.Equal([]byte(response.Header.Get(frpProbeCheckHeader)), []byte(expected)) {
			return nil
		}
		return fmt.Errorf("FRP_PROBE_RESPONSE_NOT_VERIFIED: HTTP %d", response.StatusCode)
	})
}
