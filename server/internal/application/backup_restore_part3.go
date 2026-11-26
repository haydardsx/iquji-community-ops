package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func restoreSecretMaterials(payload domain.BackupPayload, secretMap map[string]string) (map[string]string, error) {
	materials := map[string]string{}
	for _, item := range payload.SecretMaterials {
		ref := mappedRestoreString(item.SecretRef, nil, secretMap)
		if ref == "" || item.Value == "" {
			return nil, errors.New("RESTORE_SECRET_MATERIAL_INVALID")
		}
		if existing := materials[ref]; existing != "" && existing != item.Value {
			return nil, errors.New("RESTORE_SECRET_MATERIAL_CONFLICT")
		}
		materials[ref] = item.Value
	}
	return materials, nil
}

func (r *Runtime) missingRestoreSecretRefs(ctx context.Context, payload domain.BackupPayload, secretMap map[string]string) ([]string, error) {
	provided := map[string]bool{}
	for _, material := range payload.SecretMaterials {
		provided[mappedRestoreString(material.SecretRef, nil, secretMap)] = true
	}
	missing := []string{}
	for _, sourceRef := range domain.BackupPayloadSecretRefs(payload) {
		ref := mappedRestoreString(sourceRef, nil, secretMap)
		if provided[ref] {
			continue
		}
		exists, err := r.Store.HasSecretMaterial(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, ref)
		}
	}
	sort.Strings(missing)
	return uniqueStrings(missing), nil
}

func markRestoredSecretStatus(state *domain.State, selected map[domain.BackupScope]bool, selectedIDs map[string]bool, missingRefs []string) {
	missing := map[string]bool{}
	for _, ref := range missingRefs {
		missing[ref] = true
	}
	mark := func(items []domain.Object, keys ...string) {
		for _, item := range items {
			if !selectedIDs[domain.StringValue(item["id"])] {
				continue
			}
			status := "available"
			for _, key := range keys {
				if ref := domain.StringValue(item[key]); ref != "" && missing[ref] {
					status = "missing"
				}
			}
			item["materialStatus"] = status
		}
	}
	if selected[domain.BackupScopeSecretAssets] {
		mark(state.SecretAssets, "secretRef", "materialRef")
	}
	if selected[domain.BackupScopeDomainsCertificates] {
		mark(state.Certificates, "crtSecretRef", "keySecretRef")
	}
}

func (r *Runtime) prepareRestoreFiles(opened *OpenedBackupArchive, payload domain.BackupPayload, idMap map[string]string, uploadID string) (*preparedRestoreFiles, error) {
	prepared := &preparedRestoreFiles{items: []preparedRestoreFile{}}
	if payload.Projects == nil || len(payload.Projects.UploadFiles) == 0 {
		return prepared, nil
	}
	root := filepath.Join(r.Config.StateDir, "restore-staging", ".assets-"+uploadID+"-"+domain.NewID("txn"))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	prepared.root = root
	for index, metadata := range payload.Projects.UploadFiles {
		destinationAssetID := mappedRestoreString(metadata.AssetID, idMap, nil)
		if !validBackupAssetID(destinationAssetID) {
			prepared.cleanup()
			return nil, errors.New("RESTORE_UPLOAD_ASSET_ID_INVALID")
		}
		reader, err := opened.OpenFile(metadata.ArchivePath)
		if err != nil {
			prepared.cleanup()
			return nil, err
		}
		stagedPath := filepath.Join(root, fmt.Sprintf("asset-%06d", index))
		staged, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = reader.Close()
			prepared.cleanup()
			return nil, err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(staged, hash), reader)
		closeErr := staged.Close()
		_ = reader.Close()
		if copyErr != nil {
			prepared.cleanup()
			return nil, copyErr
		}
		if closeErr != nil {
			prepared.cleanup()
			return nil, closeErr
		}
		if written != metadata.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(metadata.SHA256) {
			prepared.cleanup()
			return nil, fmt.Errorf("%w:%s", ErrBackupFileIntegrity, metadata.ArchivePath)
		}
		prepared.items = append(prepared.items, preparedRestoreFile{
			stagedPath:      stagedPath,
			destinationPath: filepath.Join(r.Config.StateDir, "uploads", destinationAssetID),
			rollbackPath:    filepath.Join(root, fmt.Sprintf("rollback-%06d", index)),
		})
	}
	return prepared, nil
}

func (prepared *preparedRestoreFiles) install() error {
	if prepared == nil || len(prepared.items) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(prepared.items[0].destinationPath), 0o700); err != nil {
		return err
	}
	for index := range prepared.items {
		item := &prepared.items[index]
		if _, err := os.Stat(item.destinationPath); err == nil {
			if err := os.Rename(item.destinationPath, item.rollbackPath); err != nil {
				prepared.rollback()
				return err
			}
			item.hadDestination = true
		} else if !errors.Is(err, os.ErrNotExist) {
			prepared.rollback()
			return err
		}
		if err := os.Rename(item.stagedPath, item.destinationPath); err != nil {
			prepared.rollback()
			return err
		}
		item.installed = true
	}
	return nil
}

func (prepared *preparedRestoreFiles) rollback() {
	if prepared == nil {
		return
	}
	for index := len(prepared.items) - 1; index >= 0; index-- {
		item := &prepared.items[index]
		if item.installed {
			_ = os.Remove(item.stagedPath)
			_ = os.Rename(item.destinationPath, item.stagedPath)
			item.installed = false
		}
		if item.hadDestination {
			_ = os.Rename(item.rollbackPath, item.destinationPath)
			item.hadDestination = false
		}
	}
}

func (prepared *preparedRestoreFiles) commit() {
	if prepared == nil {
		return
	}
	for index := range prepared.items {
		if prepared.items[index].hadDestination {
			_ = os.Remove(prepared.items[index].rollbackPath)
			prepared.items[index].hadDestination = false
		}
		prepared.items[index].installed = false
	}
}

func (prepared *preparedRestoreFiles) cleanup() {
	if prepared == nil || prepared.root == "" {
		return
	}
	_ = os.RemoveAll(prepared.root)
	prepared.root = ""
}
