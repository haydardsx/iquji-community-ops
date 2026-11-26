package application

import (
	"archive/zip"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func validateBackupPayloadStructure(payload domain.BackupPayload, effective []domain.BackupScope) error {
	selected := make(map[domain.BackupScope]bool, len(effective))
	for _, scope := range effective {
		selected[scope] = true
	}
	segments := []struct {
		scope   domain.BackupScope
		present bool
	}{
		{scope: domain.BackupScopeProjects, present: payload.Projects != nil},
		{scope: domain.BackupScopeServers, present: payload.Servers != nil},
		{scope: domain.BackupScopeDomainsCertificates, present: payload.DomainsCertificates != nil},
		{scope: domain.BackupScopeSecretAssets, present: payload.SecretAssets != nil},
		{scope: domain.BackupScopeFrpTunnels, present: payload.FrpTunnels != nil},
	}
	for _, segment := range segments {
		if segment.present != selected[segment.scope] {
			return fmt.Errorf("%w:scope-segment:%s", ErrBackupArchiveIntegrity, segment.scope)
		}
	}

	seen := map[string]string{}
	register := func(kind string, id string) error {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("%w:missing-id:%s", ErrBackupArchiveIntegrity, kind)
		}
		if previous := seen[id]; previous != "" {
			return fmt.Errorf("%w:duplicate-id:%s:%s:%s", ErrBackupArchiveIntegrity, id, previous, kind)
		}
		seen[id] = kind
		return nil
	}
	registerObjects := func(kind string, items []domain.Object) error {
		for _, item := range items {
			if err := register(kind, domain.StringValue(item["id"])); err != nil {
				return err
			}
		}
		return nil
	}
	if payload.Projects != nil {
		templates := map[string]bool{}
		for _, item := range payload.Projects.ApplicationTemplates {
			id := domain.StringValue(item["id"])
			if templates[id] || validateApplicationTemplate(item) != nil {
				return fmt.Errorf("%w:application-template", ErrBackupArchiveIntegrity)
			}
			templates[id] = true
		}
		for kind, items := range map[string][]domain.Object{
			"project": payload.Projects.Projects, "application": payload.Projects.Applications,
			"application_deployment_config": payload.Projects.ApplicationDeploymentConfigs, "route": payload.Projects.Routes,
		} {
			if err := registerObjects(kind, items); err != nil {
				return err
			}
		}
		for _, metadata := range payload.Projects.UploadFiles {
			if err := register("upload_asset", metadata.AssetID); err != nil {
				return err
			}
		}
	}
	if payload.Servers != nil {
		for _, server := range payload.Servers.Servers {
			if err := validateArchivedServerConnection(server); err != nil {
				return fmt.Errorf("%w:server-connection", ErrBackupArchiveIntegrity)
			}
		}
		if err := registerObjects("server", payload.Servers.Servers); err != nil {
			return err
		}
		if err := registerObjects("server_role", payload.Servers.ServerRoles); err != nil {
			return err
		}
	}
	if payload.DomainsCertificates != nil {
		state := domain.State{Domains: payload.DomainsCertificates.Domains}
		if payload.Servers != nil {
			state.Servers = payload.Servers.Servers
		}
		for _, asset := range state.Domains {
			if draft, ok := asset["redirect"]; ok {
				if err := validateRedirectDefinition(state, stringValue(asset["id"]), objectMapFromAny(draft)); err != nil {
					return fmt.Errorf("%w:domain-redirect:%s", ErrBackupArchiveIntegrity, err)
				}
			}
		}
		for kind, items := range map[string][]domain.Object{
			"domain": payload.DomainsCertificates.Domains, "certificate": payload.DomainsCertificates.Certificates,
			"resolver": payload.DomainsCertificates.Resolvers, "ingress": payload.DomainsCertificates.Ingresses,
		} {
			if err := registerObjects(kind, items); err != nil {
				return err
			}
		}
	}
	if payload.SecretAssets != nil {
		if err := registerObjects("secret_asset", payload.SecretAssets.Assets); err != nil {
			return err
		}
	}
	if payload.FrpTunnels != nil {
		for _, tunnel := range payload.FrpTunnels.Tunnels {
			if err := register("frp_tunnel", tunnel.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeBackupEnvelopeHeader(destination io.Writer, headerBytes []byte) error {
	if len(headerBytes) == 0 || len(headerBytes) > backupArchiveHeaderLimit {
		return fmt.Errorf("%w:header-size", ErrBackupArchiveInvalid)
	}
	if _, err := destination.Write(backupArchiveMagic); err != nil {
		return err
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(headerBytes)))
	if _, err := destination.Write(size[:]); err != nil {
		return err
	}
	_, err := destination.Write(headerBytes)
	return err
}

func writeBackupZipBytes(writer *zip.Writer, name string, data []byte, method uint16) error {
	header := &zip.FileHeader{Name: name, Method: method}
	header.SetMode(0o600)
	header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func writeBackupZipFile(writer *zip.Writer, metadata domain.BackupFileMetadata, source io.Reader) error {
	if source == nil {
		return fmt.Errorf("%w:%s", ErrBackupFileMissing, metadata.ArchivePath)
	}
	header := &zip.FileHeader{Name: metadata.ArchivePath, Method: zip.Store}
	header.SetMode(0o600)
	header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(entry, hash), source)
	if err != nil {
		return err
	}
	if written != metadata.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(metadata.SHA256) {
		return fmt.Errorf("%w:%s", ErrBackupFileIntegrity, metadata.ArchivePath)
	}
	return nil
}

func validateBackupArchiveFiles(metadata []domain.BackupFileMetadata, files []BackupArchiveFile) (map[string]io.Reader, error) {
	expected := make(map[string]domain.BackupFileMetadata, len(metadata))
	for _, item := range metadata {
		if err := validateBackupArchivePath(item.ArchivePath); err != nil || item.SizeBytes < 0 || len(item.SHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("%w:file-metadata", ErrBackupArchiveInvalid)
		}
		if _, exists := expected[item.ArchivePath]; exists {
			return nil, fmt.Errorf("%w:duplicate-file", ErrBackupArchiveInvalid)
		}
		expected[item.ArchivePath] = item
	}
	readers := make(map[string]io.Reader, len(files))
	for _, file := range files {
		if _, exists := expected[file.ArchivePath]; !exists {
			return nil, fmt.Errorf("%w:undeclared-file", ErrBackupArchiveInvalid)
		}
		if file.Reader == nil || readers[file.ArchivePath] != nil {
			return nil, fmt.Errorf("%w:duplicate-or-empty-file", ErrBackupArchiveInvalid)
		}
		readers[file.ArchivePath] = file.Reader
	}
	for name := range expected {
		if readers[name] == nil {
			return nil, fmt.Errorf("%w:%s", ErrBackupFileMissing, name)
		}
	}
	return readers, nil
}

func backupPackageFiles(packageData domain.BackupPackage) []domain.BackupFileMetadata {
	if packageData.Payload.Projects == nil {
		return []domain.BackupFileMetadata{}
	}
	return append([]domain.BackupFileMetadata(nil), packageData.Payload.Projects.UploadFiles...)
}

func validateBackupSecretMaterials(payload domain.BackupPayload, sensitive bool) error {
	payloadWithoutMaterials := payload
	payloadWithoutMaterials.SecretMaterials = nil
	expectedRefs := domain.BackupPayloadSecretRefs(payloadWithoutMaterials)
	materials := make(map[string]string, len(payload.SecretMaterials))
	for _, material := range payload.SecretMaterials {
		if strings.TrimSpace(material.SecretRef) == "" || material.Value == "" || materials[material.SecretRef] != "" {
			return fmt.Errorf("%w:secret-material", ErrBackupArchiveIntegrity)
		}
		materials[material.SecretRef] = material.Value
	}
	if !sensitive {
		if len(materials) != 0 {
			return fmt.Errorf("%w:unexpected-secret-material", ErrBackupArchiveIntegrity)
		}
		return nil
	}
	if len(materials) != len(expectedRefs) {
		return fmt.Errorf("%w:secret-material-count", ErrBackupArchiveIntegrity)
	}
	for _, secretRef := range expectedRefs {
		if materials[secretRef] == "" {
			return fmt.Errorf("%w:secret-material-ref", ErrBackupArchiveIntegrity)
		}
	}
	return nil
}

func validateBackupArchivePath(name string) error {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.Clean(name) != name || !strings.HasPrefix(name, "files/") || strings.HasSuffix(name, "/") {
		return fmt.Errorf("%w:file-path", ErrBackupArchiveInvalid)
	}
	return nil
}

func validateBackupEnvelope(header backupArchiveEnvelope) error {
	if header.Version != domain.BackupArchiveVersion {
		return fmt.Errorf("%w:envelope-version", ErrBackupArchiveInvalid)
	}
	if !header.Encrypted {
		if header.Cipher != "" || header.KDF != "" || header.Salt != "" || header.NoncePrefix != "" || header.ChunkSize != 0 {
			return fmt.Errorf("%w:unencrypted-header", ErrBackupArchiveInvalid)
		}
		return nil
	}
	if header.Cipher != "xchacha20-poly1305" || header.KDF != "argon2id" {
		return fmt.Errorf("%w:crypto-suite", ErrBackupArchiveInvalid)
	}
	if header.ArgonTime == 0 || header.ArgonTime > backupArchiveMaximumArgonTime || header.ArgonMemoryKiB < 8*1024 || header.ArgonMemoryKiB > backupArchiveMaximumMemoryKiB || header.ArgonThreads == 0 || header.ArgonThreads > backupArchiveMaximumParallelism {
		return fmt.Errorf("%w:kdf-parameters", ErrBackupArchiveInvalid)
	}
	if header.ChunkSize < backupArchiveMinimumChunkSize || header.ChunkSize > backupArchiveMaximumChunkSize {
		return fmt.Errorf("%w:chunk-size", ErrBackupArchiveInvalid)
	}
	salt, err := base64.RawStdEncoding.DecodeString(header.Salt)
	if err != nil || len(salt) != backupArchiveSaltSize {
		return fmt.Errorf("%w:salt", ErrBackupArchiveInvalid)
	}
	noncePrefix, err := base64.RawStdEncoding.DecodeString(header.NoncePrefix)
	if err != nil || len(noncePrefix) != backupArchiveNoncePrefixSize {
		return fmt.Errorf("%w:nonce", ErrBackupArchiveInvalid)
	}
	return nil
}

func parseOpenedBackupArchive(reader *zip.Reader, encrypted bool) (*OpenedBackupArchive, error) {
	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || entries[file.Name] != nil {
			return nil, fmt.Errorf("%w:duplicate-or-directory-entry", ErrBackupArchiveInvalid)
		}
		entries[file.Name] = file
	}
	manifestFile := entries["manifest.json"]
	payloadFile := entries["payload.json"]
	if manifestFile == nil || payloadFile == nil {
		return nil, fmt.Errorf("%w:required-entry", ErrBackupArchiveInvalid)
	}
	manifestBytes, err := readBackupZipEntry(manifestFile, backupArchiveManifestLimit)
	if err != nil {
		return nil, err
	}
	payloadBytes, err := readBackupZipEntry(payloadFile, backupArchivePayloadLimit)
	if err != nil {
		return nil, err
	}
	var packageData domain.BackupPackage
	if err := decodeStrictBackupJSON(manifestBytes, &packageData.Manifest); err != nil {
		return nil, fmt.Errorf("%w:manifest-json", ErrBackupArchiveInvalid)
	}
	if err := decodeStrictBackupJSON(payloadBytes, &packageData.Payload); err != nil {
		return nil, fmt.Errorf("%w:payload-json", ErrBackupArchiveInvalid)
	}
	if packageData.Manifest.IncludesSensitiveData != encrypted {
		return nil, fmt.Errorf("%w:encryption-flag", ErrBackupArchiveIntegrity)
	}
	if _, err := validateBackupPackage(packageData); err != nil {
		return nil, err
	}
	delete(entries, "manifest.json")
	delete(entries, "payload.json")
	files := make(map[string]*zip.File, len(entries))
	archiveFiles := backupPackageFiles(packageData)
	metadataByPath := make(map[string]domain.BackupFileMetadata, len(archiveFiles))
	for _, metadata := range archiveFiles {
		if err := validateBackupArchivePath(metadata.ArchivePath); err != nil || metadataByPath[metadata.ArchivePath].ArchivePath != "" {
			return nil, fmt.Errorf("%w:file-metadata", ErrBackupArchiveInvalid)
		}
		metadataByPath[metadata.ArchivePath] = metadata
	}
	for name, file := range entries {
		metadata, exists := metadataByPath[name]
		if !exists {
			return nil, fmt.Errorf("%w:undeclared-entry", ErrBackupArchiveInvalid)
		}
		if err := verifyBackupZipFile(file, metadata); err != nil {
			return nil, err
		}
		files[name] = file
		delete(metadataByPath, name)
	}
	if len(metadataByPath) != 0 {
		return nil, fmt.Errorf("%w:declared-entry-missing", ErrBackupArchiveIntegrity)
	}
	return &OpenedBackupArchive{BackupPackage: packageData, files: files}, nil
}

func readBackupZipEntry(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%w:entry-size", ErrBackupArchiveInvalid)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w:zip-entry", ErrBackupArchiveInvalid)
	}
	defer func() {
		_ = reader.Close()
	}()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("%w:zip-entry", ErrBackupArchiveInvalid)
	}
	return data, nil
}

func verifyBackupZipFile(file *zip.File, metadata domain.BackupFileMetadata) error {
	if metadata.SizeBytes < 0 || uint64(metadata.SizeBytes) != file.UncompressedSize64 || len(metadata.SHA256) != sha256.Size*2 {
		return fmt.Errorf("%w:%s", ErrBackupFileIntegrity, metadata.ArchivePath)
	}
	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("%w:%s", ErrBackupFileIntegrity, metadata.ArchivePath)
	}
	defer func() {
		_ = reader.Close()
	}()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(reader, metadata.SizeBytes+1))
	if err != nil || written != metadata.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(metadata.SHA256) {
		return fmt.Errorf("%w:%s", ErrBackupFileIntegrity, metadata.ArchivePath)
	}
	return nil
}

func decodeStrictBackupJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("TRAILING_JSON_DATA")
	}
	return nil
}

func detectLegacyOrInvalidBackup(reader io.Reader) error {
	data, _ := io.ReadAll(io.LimitReader(reader, 1<<20))
	trimmed := strings.TrimSpace(string(data))
	if strings.Contains(trimmed, "project-bundle") || strings.Contains(trimmed, "server-bundle") || strings.Contains(trimmed, "secret-assets-bundle") {
		return ErrLegacyBundleUnsupported
	}
	var candidate map[string]any
	if json.Unmarshal(data, &candidate) == nil {
		if _, hasBundleType := candidate["bundleType"]; hasBundleType {
			return ErrLegacyBundleUnsupported
		}
		if _, hasEntities := candidate["entities"]; hasEntities {
			return ErrLegacyBundleUnsupported
		}
		if _, hasPackageData := candidate["packageData"]; hasPackageData {
			return ErrLegacyBundleUnsupported
		}
	}
	return ErrBackupArchiveInvalid
}

func backupScopesEqual(left []domain.BackupScope, right []domain.BackupScope) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func backupDependenciesEqual(left []domain.BackupDependency, right []domain.BackupDependency) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Scope != right[index].Scope || left[index].RequiredByScope != right[index].RequiredByScope ||
			left[index].Reason != right[index].Reason || left[index].Resource.Kind != right[index].Resource.Kind ||
			left[index].Resource.ID != right[index].Resource.ID || left[index].Resource.Name != right[index].Resource.Name ||
			left[index].Resource.Scope != right[index].Resource.Scope || len(left[index].Resource.Labels) != len(right[index].Resource.Labels) {
			return false
		}
		for key, value := range left[index].Resource.Labels {
			if right[index].Resource.Labels[key] != value {
				return false
			}
		}
	}
	return true
}

func intMapsEqual(left map[string]int, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

type backupEncryptedChunkWriter struct {
	destination io.Writer
	aead        cipher.AEAD
	noncePrefix []byte
	aadPrefix   [sha256.Size]byte
	chunkSize   int
	buffer      []byte
	counter     uint64
	closed      bool
}

func newBackupEncryptedChunkWriter(destination io.Writer, password string, header backupArchiveEnvelope, headerBytes []byte) (*backupEncryptedChunkWriter, error) {
	salt, _ := base64.RawStdEncoding.DecodeString(header.Salt)
	noncePrefix, _ := base64.RawStdEncoding.DecodeString(header.NoncePrefix)
	key := argon2.IDKey([]byte(password), salt, header.ArgonTime, header.ArgonMemoryKiB, header.ArgonThreads, chacha20poly1305.KeySize)
	defer clearBytes(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	aadInput := append(append([]byte{}, backupArchiveMagic...), headerBytes...)
	return &backupEncryptedChunkWriter{
		destination: destination,
		aead:        aead,
		noncePrefix: append([]byte(nil), noncePrefix...),
		aadPrefix:   sha256.Sum256(aadInput),
		chunkSize:   header.ChunkSize,
		buffer:      make([]byte, 0, header.ChunkSize),
	}, nil
}

func (writer *backupEncryptedChunkWriter) Write(data []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("BACKUP_ENCRYPTED_WRITER_CLOSED")
	}
	written := 0
	for len(data) > 0 {
		remaining := writer.chunkSize - len(writer.buffer)
		if remaining > len(data) {
			remaining = len(data)
		}
		writer.buffer = append(writer.buffer, data[:remaining]...)
		data = data[remaining:]
		written += remaining
		if len(writer.buffer) == writer.chunkSize {
			if err := writer.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (writer *backupEncryptedChunkWriter) Close() error {
	if writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.buffer) > 0 {
		if err := writer.flush(); err != nil {
			return err
		}
	}
	var end [4]byte
	_, err := writer.destination.Write(end[:])
	return err
}

func (writer *backupEncryptedChunkWriter) flush() error {
	nonce, additionalData, err := backupChunkParameters(writer.noncePrefix, writer.aadPrefix, writer.counter)
	if err != nil {
		return err
	}
	ciphertext := writer.aead.Seal(nil, nonce, writer.buffer, additionalData)
	if len(ciphertext) > int(^uint32(0)) {
		return fmt.Errorf("%w:chunk-overflow", ErrBackupArchiveInvalid)
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(ciphertext)))
	if _, err := writer.destination.Write(size[:]); err != nil {
		return err
	}
	if _, err := writer.destination.Write(ciphertext); err != nil {
		return err
	}
	writer.buffer = writer.buffer[:0]
	writer.counter++
	return nil
}
