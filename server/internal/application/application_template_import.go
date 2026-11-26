package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func templateContentsEqual(a, b domain.Object) bool {
	left, err := json.Marshal(a)
	right, otherErr := json.Marshal(b)
	return err == nil && otherErr == nil && bytes.Equal(left, right)
}

type TemplateImport struct {
	Document domain.Object `json:"document"`
	Policy   string        `json:"policy"`
	Revision string        `json:"revision"`
}

func (r *Runtime) ImportApplicationTemplates(ctx context.Context, input TemplateImport, preview bool) (domain.Object, error) {
	doc := input.Document
	if !objectOnlyContainsKeys(doc, "version", "items") || !integerInRange(doc["version"], 1, 1) || !applicationObjectArray(doc["items"]) {
		return nil, errors.New("APPLICATION_TEMPLATE_DOCUMENT_INVALID")
	}
	items := objectSlice(doc["items"])
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if err := validateApplicationTemplate(item); err != nil {
			return nil, err
		}
		id := stringValue(item["id"])
		if seen[id] {
			return nil, errors.New("APPLICATION_TEMPLATE_CONFLICT")
		}
		seen[id] = true
	}
	if !preview && input.Policy != "skip" && input.Policy != "overwrite" {
		return nil, errors.New("APPLICATION_TEMPLATE_DOCUMENT_INVALID")
	}
	var result domain.Object
	apply := func(state *domain.State) error {
		conflicts := []string{}
		indices := make(map[string]int, len(state.ApplicationTemplates))
		matches := []domain.Object{}
		for index, item := range state.ApplicationTemplates {
			id := stringValue(item["id"])
			indices[id] = index
			if seen[id] {
				conflicts = append(conflicts, id)
				matches = append(matches, item)
			}
		}
		slices.Sort(conflicts)
		// 预览绑定导入内容与会被覆盖的记录，提交前检查期间发生的并发修改。
		raw, err := json.Marshal([]any{doc, matches})
		if err != nil {
			return err
		}
		revision := domain.HashSecret(string(raw))
		result = domain.Object{"revision": revision, "conflicts": conflicts, "count": len(items)}
		if preview {
			return nil
		}
		if input.Revision != revision {
			return errors.New("APPLICATION_TEMPLATE_PREVIEW_CHANGED")
		}
		for _, item := range items {
			if index, exists := indices[stringValue(item["id"])]; exists {
				if input.Policy == "overwrite" {
					state.ApplicationTemplates[index] = item
				}
			} else {
				state.ApplicationTemplates = append(state.ApplicationTemplates, item)
			}
		}
		return nil
	}
	if preview {
		state, err := r.Store.LoadState(ctx)
		if err != nil {
			return nil, err
		}
		err = apply(&state)
		return result, err
	}
	err := r.Store.MutateState(ctx, apply)
	return result, err
}
