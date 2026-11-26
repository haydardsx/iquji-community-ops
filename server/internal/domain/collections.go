package domain

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type PageResult struct {
	Items    []Object `json:"items"`
	Page     int      `json:"page"`
	PageSize int      `json:"pageSize"`
	Total    int      `json:"total"`
}

func CloneObject(input Object) Object {
	if input == nil {
		return nil
	}
	next := Object{}
	for key, value := range input {
		next[key] = value
	}
	return next
}

func CloneObjectSlice(input []Object) []Object {
	items := make([]Object, 0, len(input))
	for _, item := range input {
		items = append(items, CloneObject(item))
	}
	return items
}

func FindObject(items []Object, id string) Object {
	return FindObjectBy(items, "id", id)
}

func FindObjectBy(items []Object, key string, value string) Object {
	for _, item := range items {
		if StringValue(item[key]) == value {
			return item
		}
	}
	return nil
}

func FilterObjectsBy(items []Object, key string, value string) []Object {
	result := []Object{}
	for _, item := range items {
		if value == "" || StringValue(item[key]) == value {
			result = append(result, item)
		}
	}
	return result
}

func PatchObjectByID(items *[]Object, id string, patch Object) bool {
	for index := range *items {
		if StringValue((*items)[index]["id"]) != id {
			continue
		}
		for key, value := range patch {
			(*items)[index][key] = value
		}
		return true
	}
	return false
}

func UpsertObjectByKey(items *[]Object, key string, value string, patch Object) bool {
	for index := range *items {
		if StringValue((*items)[index][key]) != value {
			continue
		}
		for patchKey, patchValue := range patch {
			if patchKey == "id" && StringValue((*items)[index]["id"]) != "" {
				continue
			}
			(*items)[index][patchKey] = patchValue
		}
		return false
	}
	*items = append(*items, patch)
	return true
}

func DeleteObjectByID(items []Object, id string) []Object {
	result := make([]Object, 0, len(items))
	for _, item := range items {
		if StringValue(item["id"]) != id {
			result = append(result, item)
		}
	}
	return result
}

func PageObjects(items []Object) map[string]any {
	return map[string]any{"items": items, "page": 1, "pageSize": len(items), "total": len(items)}
}

func ObjectFromAny(value any) Object {
	switch typed := value.(type) {
	case Object:
		return typed
	case map[string]any:
		return Object(typed)
	default:
		return Object{}
	}
}

func ObjectSliceFromAny(value any) []Object {
	switch items := value.(type) {
	case []Object:
		return items
	case []map[string]any:
		result := make([]Object, 0, len(items))
		for _, item := range items {
			result = append(result, Object(item))
		}
		return result
	case []any:
		result := make([]Object, 0, len(items))
		for _, item := range items {
			switch typed := item.(type) {
			case Object:
				result = append(result, typed)
			case map[string]any:
				result = append(result, Object(typed))
			}
		}
		return result
	default:
		return []Object{}
	}
}

func StringSliceFromAny(value any) []string {
	switch items := value.(type) {
	case []string:
		return items
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if text := StringValue(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return []string{}
	}
}

func StringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func NumberValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		number, _ := strconv.Atoi(typed.String())
		return number
	default:
		return 0
	}
}

func NumberOrDefault(value any, fallback int) int {
	if number := NumberValue(value); number > 0 {
		return number
	}
	return fallback
}

func StringFromAny(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
