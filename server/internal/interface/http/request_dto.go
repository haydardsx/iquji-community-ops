package httpapi

import (
	"encoding/json"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type releasePlanRequest struct {
	Ref     string `json:"ref"`
	RefType string `json:"refType"`
}

type secretAssetCreateRequest struct {
	Name         string `json:"name"`
	AssetType    string `json:"assetType"`
	Purpose      string `json:"purpose,omitempty"`
	Description  string `json:"description,omitempty"`
	SecretValue  string `json:"secretValue"`
	BindServerID string `json:"bindServerId,omitempty"`
}

type secretAssetUpdateRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
}

type secretAssetRotateRequest struct {
	SecretValue string `json:"secretValue"`
}

type emptyServerRuntimeActionRequest struct{}

func requestObject(input any) (domain.Object, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	result := domain.Object{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}
