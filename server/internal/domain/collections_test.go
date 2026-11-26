package domain

import (
	"encoding/json"
	"testing"
)

func TestTypedObjectRoundTripPreservesJSONFields(t *testing.T) {
	server := Server{
		ID:             "srv_1",
		Name:           "Gateway",
		PublicHost:     "203.0.113.10",
		Port:           22,
		SSHUser:        "root",
		SecretAssetIDs: []string{"asset_ssh"},
		PlatformProfile: PlatformProfile{
			OSID:           "centos",
			PackageManager: "yum",
			ServiceManager: "systemd",
		},
	}

	object, err := EncodeObject(server)
	if err != nil {
		t.Fatalf("encode object: %v", err)
	}
	if object["publicHost"] != "203.0.113.10" {
		t.Fatalf("publicHost field not preserved: %#v", object)
	}
	if profile := ObjectFromAny(object["platformProfile"]); profile["packageManager"] != "yum" {
		t.Fatalf("platformProfile packageManager not preserved: %#v", profile)
	}

	decoded, err := DecodeObject[Server](object)
	if err != nil {
		t.Fatalf("decode object: %v", err)
	}
	if decoded.ID != server.ID || decoded.PlatformProfile.PackageManager != "yum" {
		t.Fatalf("decoded mismatch: %#v", decoded)
	}
}

func TestObjectCollectionHelpers(t *testing.T) {
	items := []Object{
		{"id": "one", "projectId": "p1", "name": "One"},
		{"id": "two", "projectId": "p1", "name": "Two"},
		{"id": "three", "projectId": "p2", "name": "Three"},
	}

	if found := FindObject(items, "two"); found["name"] != "Two" {
		t.Fatalf("find object mismatch: %#v", found)
	}
	filtered := FilterObjectsBy(items, "projectId", "p1")
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d", len(filtered))
	}

	if !PatchObjectByID(&items, "two", Object{"name": "Updated"}) {
		t.Fatal("expected patch to update")
	}
	if FindObject(items, "two")["name"] != "Updated" {
		t.Fatalf("patch did not apply: %#v", items)
	}

	inserted := UpsertObjectByKey(&items, "id", "four", Object{"id": "four", "name": "Four"})
	if !inserted || FindObject(items, "four") == nil {
		t.Fatalf("expected insert: inserted=%v items=%#v", inserted, items)
	}
	inserted = UpsertObjectByKey(&items, "id", "four", Object{"id": "replacement", "name": "Four Updated"})
	if inserted {
		t.Fatal("expected upsert to update existing item")
	}
	if item := FindObject(items, "four"); item["id"] != "four" || item["name"] != "Four Updated" {
		t.Fatalf("upsert should preserve existing id: %#v", item)
	}

	items = DeleteObjectByID(items, "one")
	if FindObject(items, "one") != nil || len(items) != 3 {
		t.Fatalf("delete mismatch: %#v", items)
	}
	page := PageObjects(items)
	if page["page"] != 1 || page["pageSize"] != 3 || page["total"] != 3 {
		t.Fatalf("page mismatch: %#v", page)
	}
}

func TestScalarHelpers(t *testing.T) {
	if got := StringSliceFromAny([]any{"a", 3, "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("string slice mismatch: %#v", got)
	}
	if got := NumberValue(json.Number("42")); got != 42 {
		t.Fatalf("json number = %d", got)
	}
	if got := NumberOrDefault(0, 7); got != 7 {
		t.Fatalf("fallback = %d", got)
	}
}
