package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestEncryptedApplicationBackupRestoresReferencesWithoutLiveEndpoints(t *testing.T) {
	source, target := newTestAPI(t), newTestAPI(t)
	ctx := context.Background()
	random := func() string {
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal("无法生成一次性测试材料")
		}
		return hex.EncodeToString(raw)
	}
	data, password := random(), random()
	asset := createTestSecretAsset(t, source, map[string]any{"assetType": "application_secret", "name": "示例凭据", "secretValue": data})
	assetID := domain.StringValue(asset["id"])
	if err := source.store.MutateState(ctx, func(s *domain.State) error {
		vault := domain.FindObject(s.SecretAssets, assetID)
		s.Projects = append(s.Projects, domain.Object{"id": "project_a", "slug": "producer"}, domain.Object{"id": "project_b", "slug": "consumer"})
		s.Applications = append(s.Applications, domain.Object{"id": "app_a", "name": "数据服务", "projectId": "project_a", "runtimeSnapshot": domain.Object{"availability": "available"}, "publishedBuildOutputs": domain.Object{"out_build": "old"}}, domain.Object{"id": "app_b", "name": "前端", "projectId": "project_b"})
		s.ApplicationDeploymentConfigs = append(s.ApplicationDeploymentConfigs, domain.Object{"id": "cfg_a", "applicationId": "app_a", "outputVariables": []domain.Object{
			{"id": "out_key", "key": "ACCESS", "visibility": "cross_project", "valueSource": "secret_ref", "secretRef": vault["secretRef"]},
			{"id": "out_url", "key": "URL", "visibility": "cross_project", "valueSource": "route_url", "routeId": "route_a"},
		}}, domain.Object{"id": "cfg_b", "applicationId": "app_b", "environmentVariables": []domain.Object{
			{"key": "PASSWORD", "stage": "runtime", "valueSource": "output_ref", "outputId": "out_key", "outputApplicationId": "app_a"},
			{"key": "BACKEND_URL", "stage": "build", "valueSource": "output_ref", "outputId": "out_url", "outputApplicationId": "app_a"},
		}})
		s.Routes = append(s.Routes, domain.Object{"id": "route_a", "applicationId": "app_a", "accessibleUrls": []string{"https://example.test/"}, "lastApplied": domain.Object{"accessibleUrls": []string{"https://example.test/"}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	requestJSON := func(api *testAPI, path string, body any) domain.Object {
		t.Helper()
		response := api.request(t, http.MethodPost, path, body)
		if response.StatusCode != 200 {
			v := decodeTestJSON[domain.Object](t, response)
			t.Fatalf("接口 %s 失败：%d %s", path, response.StatusCode, v["errorCode"])
		}
		return decodeTestJSON[domain.Object](t, response)
	}
	opts := domain.BackupOptions{RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects}, IncludeSensitiveData: true}
	preview := requestJSON(source, "/backup-restore/backups/preview", domain.BackupPreviewRequest{Options: opts})
	response := source.request(t, http.MethodPost, "/backup-restore/backups/download", domain.BackupDownloadRequest{Options: opts, PreviewDigest: domain.StringValue(preview["previewDigest"]), Passphrase: password})
	if response.StatusCode != 200 {
		response.Body.Close()
		t.Fatal("加密备份下载失败")
	}
	archive, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || len(archive) == 0 || bytes.Contains(archive, []byte(data)) {
		t.Fatal("加密归档不符合预期")
	}
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "backup.iquops")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("passphrase", password); err != nil {
		t.Fatal("设置口令失败")
	}
	if err = writer.Close(); err != nil {
		t.Fatal("结束归档上传失败")
	}
	response, err = http.Post(target.server.URL+"/backup-restore/restores/uploads", writer.FormDataContentType(), &upload)
	if err != nil {
		t.Fatal("上传到新实例失败")
	}
	staged := decodeTestJSON[domain.Object](t, response)
	restoreOpts := domain.RestoreOptions{RequestedScopes: opts.RequestedScopes, ConflictPolicy: domain.RestoreConflictAbortOnConflict}
	uploadID := domain.StringValue(staged["uploadId"])
	check := requestJSON(target, "/backup-restore/restores/preview", domain.RestorePreviewRequest{UploadID: uploadID, Options: restoreOpts})
	if len(domain.StringSliceFromAny(check["blockers"])) > 0 || len(domain.StringSliceFromAny(check["missingSecretRefs"])) > 0 {
		t.Fatal("恢复预检发现阻断或缺失材料")
	}
	created := requestJSON(target, "/backup-restore/restores/execute", domain.RestoreExecuteRequest{UploadID: uploadID, Options: restoreOpts, PreviewDigest: domain.StringValue(check["previewDigest"])})
	id := domain.StringValue(created["id"])
	requestJSON(target, "/operations/runs/"+id+"/confirm", nil)
	if _, err = target.runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal("恢复 Worker 执行失败")
	}
	finished, err := target.runtime.GetOperationRun(ctx, id)
	if err != nil || finished["status"] != "succeeded" {
		t.Fatal("恢复任务没有成功", finished["errorCode"])
	}
	s, err := target.store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vault := domain.FindObject(s.SecretAssets, assetID)
	material, err := target.store.GetSecretMaterial(ctx, domain.StringValue(vault["materialRef"]))
	if err != nil || material != data || vault["materialStatus"] != "available" {
		t.Fatal("应用凭据与材料版本未恢复")
	}
	producer := domain.FindObjectBy(s.ApplicationDeploymentConfigs, "applicationId", "app_a")
	consumer := domain.FindObjectBy(s.ApplicationDeploymentConfigs, "applicationId", "app_b")
	if len(domain.ObjectSliceFromAny(consumer["environmentVariables"])) != 2 {
		t.Fatal("消费者变量数量不符")
	}
	for _, v := range domain.ObjectSliceFromAny(consumer["environmentVariables"]) {
		o := domain.FindObject(domain.ObjectSliceFromAny(producer["outputVariables"]), domain.StringValue(v["outputId"]))
		if o == nil || v["outputApplicationId"] != "app_a" || o["visibility"] != "cross_project" || (v["key"] == "PASSWORD" && o["secretRef"] != vault["secretRef"]) {
			t.Fatal("跨项目变量引用没有保留")
		}
	}
	// 重启预检只解析运行期输入，归档中的跨项目凭据应能冻结到已恢复的修订材料。
	op := requestJSON(target, "/projects/applications/app_b/restart", domain.Object{})
	state, err := target.store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frozen []domain.Object
	for _, task := range state.Tasks {
		if task.ID == op["id"] {
			frozen = domain.ObjectSliceFromAny(task.Payload["applicationInputs"])
			break
		}
	}
	if len(frozen) != 1 || frozen[0]["valueSource"] != "secret_ref" || frozen[0]["materialRevisionRef"] != vault["materialRef"] {
		t.Fatal("恢复后跨项目消费者无法使用原凭据输出")
	}
	app, route := domain.FindObject(s.Applications, "app_a"), domain.FindObject(s.Routes, "route_a")
	if app["status"] != "unverified" || app["runtimeSnapshot"] != nil || app["publishedBuildOutputs"] != nil || route["lastApplied"] != nil || route["accessibleUrls"] != nil {
		t.Fatal("归档历史被错误地用作当前运行或端点证据")
	}
}
