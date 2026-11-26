package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type backupArchiveStoreStub struct {
	state     domain.State
	materials map[string]string
}

func (store backupArchiveStoreStub) LoadState(context.Context) (domain.State, error) {
	return store.state, nil
}

func (store backupArchiveStoreStub) GetSecretMaterial(_ context.Context, secretRef string) (string, error) {
	value := store.materials[secretRef]
	if value == "" {
		return "", errors.New("SECRET_MATERIAL_NOT_FOUND")
	}
	return value, nil
}

func TestBackupArchiveServiceBuildsTypedSensitivePackage(t *testing.T) {
	state := domain.EmptyState()
	state.Projects = []domain.Object{{"id": "prj_1", "serverIds": []string{"srv_1"}}}
	state.Applications = []domain.Object{{
		"id": "app_1", "projectId": "prj_1",
		"sourceConfig": domain.Object{"provider": "git", "authSecretRef": "secret_git"},
	}}
	state.Servers = []domain.Object{{"id": "srv_1", "secretAssetIds": []string{"asset_ssh"}}}
	state.SecretAssets = []domain.Object{
		{"id": "asset_git", "secretRef": "secret_git"},
		{"id": "asset_ssh", "secretRef": "secret_ssh"},
	}
	service := NewBackupArchiveService(backupArchiveStoreStub{
		state: state,
		materials: map[string]string{
			"secret_git": "git-token-material",
			"secret_ssh": "ssh-private-key-material",
		},
	}, "0.1.0", "backup-v1")
	service.Now = func() string { return "2026-07-14T00:00:00Z" }

	packageData, err := service.BuildPackage(context.Background(), domain.BackupOptions{
		RequestedScopes:      []domain.BackupScope{domain.BackupScopeProjects},
		IncludeSensitiveData: true,
	})
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	expectedScopes := []domain.BackupScope{domain.BackupScopeProjects, domain.BackupScopeServers, domain.BackupScopeSecretAssets}
	if !reflect.DeepEqual(packageData.Manifest.EffectiveScopes, expectedScopes) {
		t.Fatal("unexpected effective scopes")
	}
	if len(packageData.Payload.SecretMaterials) != 2 || packageData.Payload.SecretMaterials[0].SecretRef != "secret_git" || packageData.Payload.SecretMaterials[1].SecretRef != "secret_ssh" {
		t.Fatal("credential references were not collected in the expected order")
	}
	if packageData.Manifest.PayloadSHA256 == "" || packageData.Manifest.RecordCounts["secretMaterials"] != 2 {
		t.Fatal("manifest integrity or counts missing")
	}
}

func TestBackupArchiveServiceFailsWhenSelectedSecretMaterialIsMissing(t *testing.T) {
	state := domain.EmptyState()
	state.SecretAssets = []domain.Object{{"id": "asset_1", "secretRef": "secret_missing"}}
	service := NewBackupArchiveService(backupArchiveStoreStub{state: state, materials: map[string]string{}}, "0.1.0", "backup-v1")
	_, err := service.BuildPackage(context.Background(), domain.BackupOptions{
		RequestedScopes:      []domain.BackupScope{domain.BackupScopeSecretAssets},
		IncludeSensitiveData: true,
	})
	if !errors.Is(err, ErrBackupSecretMaterialMissing) {
		t.Fatalf("expected missing secret material error, got %v", err)
	}
}

func TestWriteAndReadUnencryptedBackupArchive(t *testing.T) {
	packageData := backupArchiveTestPackage(false, nil, nil)
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "", nil); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if !bytes.HasPrefix(archive.Bytes(), backupArchiveMagic) || bytes.HasPrefix(archive.Bytes(), []byte("PK")) {
		t.Fatal("archive did not use the versioned IQuOps envelope")
	}
	restored, err := ReadBackupArchive(bytes.NewReader(archive.Bytes()), "")
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !reflect.DeepEqual(restored, packageData) {
		t.Fatal("archive round trip changed package")
	}
}

func TestSensitiveBackupArchiveEncryptsChunksAndValidatesFiles(t *testing.T) {
	fileData := bytes.Repeat([]byte("iquops-archive-chunk-test\n"), 90000)
	fileHash := sha256.Sum256(fileData)
	metadata := domain.BackupFileMetadata{
		ArchivePath: "files/uploads/upload_1",
		AssetID:     "upload_1",
		FileName:    "source.tar",
		SHA256:      hex.EncodeToString(fileHash[:]),
		SizeBytes:   int64(len(fileData)),
	}
	secret := "super-secret-private-key-material"
	packageData := backupArchiveTestPackage(true, []domain.BackupSecretMaterial{{SecretRef: "secret_1", Value: secret}}, []domain.BackupFileMetadata{metadata})
	var archive bytes.Buffer
	password := "correct-horse-battery-staple"
	if err := WriteBackupArchive(&archive, packageData, password, []BackupArchiveFile{{ArchivePath: metadata.ArchivePath, Reader: bytes.NewReader(fileData)}}); err != nil {
		t.Fatalf("write sensitive archive: %v", err)
	}
	if bytes.Contains(archive.Bytes(), []byte(secret)) || bytes.Contains(archive.Bytes(), fileData[:64]) {
		t.Fatal("sensitive archive exposed plaintext payload or file bytes")
	}

	opened, err := OpenBackupArchive(bytes.NewReader(archive.Bytes()), password)
	if err != nil {
		t.Fatalf("open sensitive archive: %v", err)
	}
	defer func() { _ = opened.Close() }()
	if got := opened.FileNames(); !reflect.DeepEqual(got, []string{metadata.ArchivePath}) {
		t.Fatal("unexpected file names")
	}
	file, err := opened.OpenFile(metadata.ArchivePath)
	if err != nil {
		t.Fatalf("open archived file: %v", err)
	}
	restoredFile, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || !bytes.Equal(restoredFile, fileData) {
		t.Fatal("archived file did not round trip")
	}
	if opened.Payload.SecretMaterials[0].Value != secret {
		t.Fatal("secret material did not round trip after authenticated decryption")
	}

	if _, err := ReadBackupArchive(bytes.NewReader(archive.Bytes()), "wrong-password-value"); !errors.Is(err, ErrBackupArchiveAuthentication) {
		t.Fatalf("expected authentication error for wrong password, got %v", err)
	}
	tampered := append([]byte(nil), archive.Bytes()...)
	headerSizeOffset := len(backupArchiveMagic)
	headerSize := int(binary.BigEndian.Uint32(tampered[headerSizeOffset : headerSizeOffset+4]))
	bodyOffset := headerSizeOffset + 4 + headerSize
	if bodyOffset+8 >= len(tampered) {
		t.Fatal("encrypted archive body unexpectedly short")
	}
	tampered[bodyOffset+8] ^= 0x40
	if _, err := ReadBackupArchive(bytes.NewReader(tampered), password); !errors.Is(err, ErrBackupArchiveAuthentication) {
		t.Fatalf("expected authentication error for tampered chunk, got %v", err)
	}
}

func TestBackupArchiveRejectsLegacyBundle(t *testing.T) {
	legacy := `{"bundleType":"project-bundle","entities":{"projects":[]}}`
	if _, err := ReadBackupArchive(strings.NewReader(legacy), ""); !errors.Is(err, ErrLegacyBundleUnsupported) {
		t.Fatalf("expected legacy bundle rejection, got %v", err)
	}
}

func TestBackupArchiveRejectsDuplicateBusinessIDs(t *testing.T) {
	packageData := backupArchiveTestPackage(false, nil, nil)
	packageData.Payload.Projects.Applications = append(packageData.Payload.Projects.Applications, domain.Object{
		"id": "prj_1", "projectId": "prj_1", "name": "Duplicate", "slug": "duplicate",
	})
	refreshBackupArchiveTestManifest(&packageData)
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "", nil); !errors.Is(err, ErrBackupArchiveIntegrity) {
		t.Fatalf("expected duplicate ID rejection, got %v", err)
	}
}

func TestBackupArchiveRejectsUploadAssetIDCollision(t *testing.T) {
	fileHash := sha256.Sum256([]byte("asset"))
	packageData := backupArchiveTestPackage(false, nil, []domain.BackupFileMetadata{{
		ArchivePath: "files/uploads/prj_1",
		AssetID:     "prj_1",
		FileName:    "asset.tar",
		SHA256:      hex.EncodeToString(fileHash[:]),
		SizeBytes:   5,
	}})
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "", []BackupArchiveFile{{ArchivePath: "files/uploads/prj_1", Reader: strings.NewReader("asset")}}); !errors.Is(err, ErrBackupArchiveIntegrity) {
		t.Fatalf("expected upload asset ID collision rejection, got %v", err)
	}
}

func TestBackupArchiveRejectsMissingDependencyScope(t *testing.T) {
	packageData := backupArchiveTestPackage(false, nil, nil)
	packageData.Payload.Projects.Applications = append(packageData.Payload.Projects.Applications, domain.Object{
		"id": "app_1", "projectId": "prj_1", "name": "API", "slug": "api",
		"sourceConfig": domain.Object{"provider": "git", "authSecretRef": "secret_git"},
	})
	refreshBackupArchiveTestManifest(&packageData)
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "", nil); !errors.Is(err, ErrBackupArchiveIntegrity) {
		t.Fatalf("expected missing dependency rejection, got %v", err)
	}
}

func TestBackupArchivePasswordMinimumCountsCharacters(t *testing.T) {
	packageData := backupArchiveTestPackage(true, []domain.BackupSecretMaterial{{SecretRef: "secret_1", Value: "secret"}}, nil)
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "密码密码密码", nil); !errors.Is(err, ErrBackupPasswordTooShort) {
		t.Fatalf("expected six-character password rejection, got %v", err)
	}
}

func backupArchiveTestPackage(sensitive bool, materials []domain.BackupSecretMaterial, files []domain.BackupFileMetadata) domain.BackupPackage {
	scopes := []domain.BackupScope{domain.BackupScopeProjects}
	applications := []domain.Object{}
	var secretAssets *domain.BackupSecretAssetsData
	if len(materials) > 0 {
		scopes = append(scopes, domain.BackupScopeSecretAssets)
		applications = append(applications, domain.Object{
			"id": "app_1", "projectId": "prj_1",
			"sourceConfig": domain.Object{"provider": "git", "authSecretRef": materials[0].SecretRef},
		})
		secretAssets = &domain.BackupSecretAssetsData{Assets: []domain.Object{{
			"id": "asset_1", "secretRef": materials[0].SecretRef, "maskedSummary": "********",
		}}}
	}
	payload := domain.BackupPayload{
		SchemaVersion: "backup-v1",
		Scopes:        scopes,
		Projects: &domain.BackupProjectsData{
			Projects:                     []domain.Object{{"id": "prj_1", "name": "Project"}},
			Applications:                 applications,
			ApplicationDeploymentConfigs: []domain.Object{},
			Routes:                       []domain.Object{},
			UploadFiles:                  append([]domain.BackupFileMetadata(nil), files...),
		},
		SecretAssets:    secretAssets,
		SecretMaterials: append([]domain.BackupSecretMaterial(nil), materials...),
	}
	payloadBytes, err := CanonicalBackupPayload(payload)
	if err != nil {
		panic(err)
	}
	payloadHash := sha256.Sum256(payloadBytes)
	logicalState := backupPayloadState(payload, scopes, false)
	_, dependencies, err := domain.ResolveBackupScopeClosure(logicalState, []domain.BackupScope{domain.BackupScopeProjects})
	if err != nil {
		panic(err)
	}
	return domain.BackupPackage{
		Manifest: domain.BackupManifest{
			ApplicationVersion:    "0.1.0",
			SchemaVersion:         "backup-v1",
			RequestedScopes:       []domain.BackupScope{domain.BackupScopeProjects},
			EffectiveScopes:       scopes,
			DependencyReasons:     dependencies,
			RecordCounts:          domain.BackupPayloadCounts(payload),
			IncludesSensitiveData: sensitive,
			IncludesLogsAndAudit:  false,
			PayloadSHA256:         hex.EncodeToString(payloadHash[:]),
			CreatedAt:             "2026-07-14T00:00:00Z",
		},
		Payload: payload,
	}
}

func refreshBackupArchiveTestManifest(packageData *domain.BackupPackage) {
	payloadBytes, err := CanonicalBackupPayload(packageData.Payload)
	if err != nil {
		panic(err)
	}
	payloadHash := sha256.Sum256(payloadBytes)
	packageData.Manifest.PayloadSHA256 = hex.EncodeToString(payloadHash[:])
	packageData.Manifest.RecordCounts = domain.BackupPayloadCounts(packageData.Payload)
}
