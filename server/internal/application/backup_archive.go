package application

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	backupArchiveHeaderLimit        = 64 << 10
	backupArchiveManifestLimit      = 4 << 20
	backupArchivePayloadLimit       = 256 << 20
	backupArchiveDefaultChunkSize   = 1 << 20
	backupArchiveArgonTime          = uint32(3)
	backupArchiveArgonMemoryKiB     = uint32(64 * 1024)
	backupArchiveArgonParallelism   = uint8(1)
	backupArchiveSaltSize           = 16
	backupArchiveNoncePrefixSize    = 16
	backupArchiveMinimumPassword    = 12
	backupArchiveMaximumChunkSize   = 16 << 20
	backupArchiveMinimumChunkSize   = 64 << 10
	backupArchiveMaximumMemoryKiB   = 512 * 1024
	backupArchiveMaximumArgonTime   = 10
	backupArchiveMaximumParallelism = 16
)

var backupArchiveMagic = []byte("IQUOPS-BACKUP\x00")

var (
	ErrBackupArchiveInvalid        = errors.New("BACKUP_ARCHIVE_INVALID")
	ErrBackupArchiveAuthentication = errors.New("BACKUP_ARCHIVE_AUTHENTICATION_FAILED")
	ErrBackupArchiveIntegrity      = errors.New("BACKUP_ARCHIVE_INTEGRITY_FAILED")
	ErrBackupArchiveTruncated      = errors.New("BACKUP_ARCHIVE_TRUNCATED")
	ErrBackupPasswordRequired      = errors.New("BACKUP_PASSWORD_REQUIRED")
	ErrBackupPasswordTooShort      = errors.New("BACKUP_PASSWORD_TOO_SHORT")
	ErrBackupSecretMaterialMissing = errors.New("BACKUP_SECRET_MATERIAL_MISSING")
	ErrBackupFileMissing           = errors.New("BACKUP_FILE_MISSING")
	ErrBackupFileIntegrity         = errors.New("BACKUP_FILE_INTEGRITY_FAILED")
	ErrLegacyBundleUnsupported     = errors.New("LEGACY_BUNDLE_UNSUPPORTED")
)

type BackupArchiveStore interface {
	LoadState(context.Context) (domain.State, error)
	GetSecretMaterial(context.Context, string) (string, error)
}

type BackupArchiveService struct {
	Store              BackupArchiveStore
	ApplicationVersion string
	SchemaVersion      string
	Now                func() string
}

type BackupArchiveFile struct {
	ArchivePath string
	Reader      io.Reader
}

type OpenedBackupArchive struct {
	domain.BackupPackage
	scratch     *os.File
	scratchPath string
	files       map[string]*zip.File
}

type backupArchiveEnvelope struct {
	Version        int    `json:"version"`
	Encrypted      bool   `json:"encrypted"`
	Cipher         string `json:"cipher,omitempty"`
	KDF            string `json:"kdf,omitempty"`
	ArgonTime      uint32 `json:"argonTime,omitempty"`
	ArgonMemoryKiB uint32 `json:"argonMemoryKiB,omitempty"`
	ArgonThreads   uint8  `json:"argonThreads,omitempty"`
	Salt           string `json:"salt,omitempty"`
	NoncePrefix    string `json:"noncePrefix,omitempty"`
	ChunkSize      int    `json:"chunkSize,omitempty"`
}

func NewBackupArchiveService(store BackupArchiveStore, applicationVersion string, schemaVersion string) *BackupArchiveService {
	return &BackupArchiveService{
		Store:              store,
		ApplicationVersion: applicationVersion,
		SchemaVersion:      schemaVersion,
		Now:                domain.Now,
	}
}

func (service *BackupArchiveService) BuildPackage(ctx context.Context, options domain.BackupOptions) (domain.BackupPackage, error) {
	if service == nil || service.Store == nil {
		return domain.BackupPackage{}, errors.New("BACKUP_STORE_REQUIRED")
	}
	if strings.TrimSpace(service.ApplicationVersion) == "" || strings.TrimSpace(service.SchemaVersion) == "" {
		return domain.BackupPackage{}, errors.New("BACKUP_VERSION_METADATA_REQUIRED")
	}
	state, err := service.Store.LoadState(ctx)
	if err != nil {
		return domain.BackupPackage{}, err
	}
	effectiveScopes, dependencies, err := domain.ResolveBackupScopeClosure(state, options.RequestedScopes)
	if err != nil {
		return domain.BackupPackage{}, err
	}
	requestedScopes, err := domain.NormalizeBackupScopes(options.RequestedScopes)
	if err != nil {
		return domain.BackupPackage{}, err
	}
	payload := domain.BuildBackupPayload(state, service.SchemaVersion, effectiveScopes, options.IncludeLogsAndAudit)
	if options.IncludeSensitiveData {
		for _, secretRef := range domain.BackupPayloadSecretRefs(payload) {
			material, materialErr := service.Store.GetSecretMaterial(ctx, secretRef)
			if materialErr != nil || material == "" {
				return domain.BackupPackage{}, fmt.Errorf("%w:%s", ErrBackupSecretMaterialMissing, secretRef)
			}
			payload.SecretMaterials = append(payload.SecretMaterials, domain.BackupSecretMaterial{SecretRef: secretRef, Value: material})
		}
	}
	payloadBytes, err := CanonicalBackupPayload(payload)
	if err != nil {
		return domain.BackupPackage{}, err
	}
	payloadHash := sha256.Sum256(payloadBytes)
	now := service.Now
	if now == nil {
		now = domain.Now
	}
	manifest := domain.BackupManifest{
		ApplicationVersion:    service.ApplicationVersion,
		SchemaVersion:         service.SchemaVersion,
		RequestedScopes:       requestedScopes,
		EffectiveScopes:       effectiveScopes,
		DependencyReasons:     dependencies,
		RecordCounts:          domain.BackupPayloadCounts(payload),
		IncludesSensitiveData: options.IncludeSensitiveData,
		IncludesLogsAndAudit:  options.IncludeLogsAndAudit,
		PayloadSHA256:         hex.EncodeToString(payloadHash[:]),
		CreatedAt:             now(),
	}
	return domain.BackupPackage{Manifest: manifest, Payload: payload}, nil
}

func (service *BackupArchiveService) Write(ctx context.Context, destination io.Writer, options domain.BackupOptions, password string, files []BackupArchiveFile) (domain.BackupManifest, error) {
	packageData, err := service.BuildPackage(ctx, options)
	if err != nil {
		return domain.BackupManifest{}, err
	}
	if err := WriteBackupArchive(destination, packageData, password, files); err != nil {
		return domain.BackupManifest{}, err
	}
	return packageData.Manifest, nil
}

func CanonicalBackupPayload(payload domain.BackupPayload) ([]byte, error) {
	return json.Marshal(payload)
}

func WriteBackupArchive(destination io.Writer, packageData domain.BackupPackage, password string, files []BackupArchiveFile) error {
	if destination == nil {
		return fmt.Errorf("%w:destination", ErrBackupArchiveInvalid)
	}
	payloadBytes, err := validateBackupPackage(packageData)
	if err != nil {
		return err
	}
	archiveFiles := backupPackageFiles(packageData)
	fileReaders, err := validateBackupArchiveFiles(archiveFiles, files)
	if err != nil {
		return err
	}

	header := backupArchiveEnvelope{Version: domain.BackupArchiveVersion, Encrypted: packageData.Manifest.IncludesSensitiveData}
	bodyWriter := destination
	var encryptedWriter *backupEncryptedChunkWriter
	if header.Encrypted {
		if password == "" {
			return ErrBackupPasswordRequired
		}
		if !validBackupPassword(password) {
			return ErrBackupPasswordTooShort
		}
		salt := make([]byte, backupArchiveSaltSize)
		noncePrefix := make([]byte, backupArchiveNoncePrefixSize)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return err
		}
		if _, err := io.ReadFull(rand.Reader, noncePrefix); err != nil {
			return err
		}
		header.Cipher = "xchacha20-poly1305"
		header.KDF = "argon2id"
		header.ArgonTime = backupArchiveArgonTime
		header.ArgonMemoryKiB = backupArchiveArgonMemoryKiB
		header.ArgonThreads = backupArchiveArgonParallelism
		header.Salt = base64.RawStdEncoding.EncodeToString(salt)
		header.NoncePrefix = base64.RawStdEncoding.EncodeToString(noncePrefix)
		header.ChunkSize = backupArchiveDefaultChunkSize
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if err := writeBackupEnvelopeHeader(destination, headerBytes); err != nil {
		return err
	}
	if header.Encrypted {
		encryptedWriter, err = newBackupEncryptedChunkWriter(destination, password, header, headerBytes)
		if err != nil {
			return err
		}
		bodyWriter = encryptedWriter
	}

	zipWriter := zip.NewWriter(bodyWriter)
	manifestBytes, err := json.Marshal(packageData.Manifest)
	if err == nil {
		err = writeBackupZipBytes(zipWriter, "manifest.json", manifestBytes, zip.Deflate)
	}
	if err == nil {
		err = writeBackupZipBytes(zipWriter, "payload.json", payloadBytes, zip.Deflate)
	}
	if err == nil {
		for _, metadata := range archiveFiles {
			if err = writeBackupZipFile(zipWriter, metadata, fileReaders[metadata.ArchivePath]); err != nil {
				break
			}
		}
	}
	closeErr := zipWriter.Close()
	if err == nil {
		err = closeErr
	}
	if encryptedWriter != nil {
		closeErr = encryptedWriter.Close()
		if err == nil {
			err = closeErr
		}
	}
	return err
}

func validBackupPassword(password string) bool {
	return utf8.RuneCountInString(password) >= backupArchiveMinimumPassword
}

func OpenBackupArchive(source io.Reader, password string) (*OpenedBackupArchive, error) {
	if source == nil {
		return nil, fmt.Errorf("%w:source", ErrBackupArchiveInvalid)
	}
	buffered := bufio.NewReader(source)
	magic, err := buffered.Peek(len(backupArchiveMagic))
	if err != nil || string(magic) != string(backupArchiveMagic) {
		return nil, detectLegacyOrInvalidBackup(buffered)
	}
	if _, err := buffered.Discard(len(backupArchiveMagic)); err != nil {
		return nil, ErrBackupArchiveTruncated
	}
	var headerSizeBytes [4]byte
	if _, err := io.ReadFull(buffered, headerSizeBytes[:]); err != nil {
		return nil, ErrBackupArchiveTruncated
	}
	headerSize := binary.BigEndian.Uint32(headerSizeBytes[:])
	if headerSize == 0 || headerSize > backupArchiveHeaderLimit {
		return nil, fmt.Errorf("%w:header-size", ErrBackupArchiveInvalid)
	}
	headerBytes := make([]byte, headerSize)
	if _, err := io.ReadFull(buffered, headerBytes); err != nil {
		return nil, ErrBackupArchiveTruncated
	}
	var header backupArchiveEnvelope
	if err := decodeStrictBackupJSON(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w:header", ErrBackupArchiveInvalid)
	}
	if err := validateBackupEnvelope(header); err != nil {
		return nil, err
	}

	scratch, err := os.CreateTemp("", "iquops-backup-*.zip")
	if err != nil {
		return nil, err
	}
	scratchPath := scratch.Name()
	cleanup := func() {
		_ = scratch.Close()
		_ = os.Remove(scratchPath)
	}
	if err := scratch.Chmod(0o600); err != nil {
		cleanup()
		return nil, err
	}
	if header.Encrypted {
		if password == "" {
			cleanup()
			return nil, ErrBackupPasswordRequired
		}
		err = decryptBackupArchiveBody(scratch, buffered, password, header, headerBytes)
	} else {
		_, err = io.Copy(scratch, buffered)
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	info, err := scratch.Stat()
	if err != nil {
		cleanup()
		return nil, err
	}
	zipReader, err := zip.NewReader(scratch, info.Size())
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("%w:zip", ErrBackupArchiveInvalid)
	}
	opened, err := parseOpenedBackupArchive(zipReader, header.Encrypted)
	if err != nil {
		cleanup()
		return nil, err
	}
	opened.scratch = scratch
	opened.scratchPath = scratchPath
	if err := os.Remove(scratchPath); err == nil {
		opened.scratchPath = ""
	}
	return opened, nil
}

func ReadBackupArchive(source io.Reader, password string) (domain.BackupPackage, error) {
	opened, err := OpenBackupArchive(source, password)
	if err != nil {
		return domain.BackupPackage{}, err
	}
	defer func() {
		_ = opened.Close()
	}()
	return opened.BackupPackage, nil
}

func (archive *OpenedBackupArchive) FileNames() []string {
	if archive == nil {
		return nil
	}
	result := make([]string, 0, len(archive.files))
	for name := range archive.files {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (archive *OpenedBackupArchive) OpenFile(archivePath string) (io.ReadCloser, error) {
	if archive == nil || archive.scratch == nil {
		return nil, errors.New("BACKUP_ARCHIVE_CLOSED")
	}
	file := archive.files[archivePath]
	if file == nil {
		return nil, os.ErrNotExist
	}
	return file.Open()
}

func (archive *OpenedBackupArchive) Close() error {
	if archive == nil {
		return nil
	}
	var result error
	if archive.scratch != nil {
		result = archive.scratch.Close()
		archive.scratch = nil
	}
	if archive.scratchPath != "" {
		if err := os.Remove(archive.scratchPath); result == nil && err != nil && !errors.Is(err, os.ErrNotExist) {
			result = err
		}
		archive.scratchPath = ""
	}
	return result
}

func validateBackupPackage(packageData domain.BackupPackage) ([]byte, error) {
	manifest := packageData.Manifest
	if manifest.ApplicationVersion == "" || manifest.SchemaVersion == "" || manifest.CreatedAt == "" {
		return nil, fmt.Errorf("%w:metadata", ErrBackupArchiveInvalid)
	}
	if manifest.SchemaVersion != packageData.Payload.SchemaVersion {
		return nil, fmt.Errorf("%w:schema-version", ErrBackupArchiveIntegrity)
	}
	requested, err := domain.NormalizeBackupScopes(manifest.RequestedScopes)
	if err != nil || !backupScopesEqual(requested, manifest.RequestedScopes) {
		return nil, fmt.Errorf("%w:requested-scopes", ErrBackupArchiveInvalid)
	}
	effective, err := domain.NormalizeBackupScopes(manifest.EffectiveScopes)
	if err != nil || !backupScopesEqual(effective, manifest.EffectiveScopes) || !backupScopesEqual(effective, packageData.Payload.Scopes) {
		return nil, fmt.Errorf("%w:effective-scopes", ErrBackupArchiveIntegrity)
	}
	if err := validateBackupPayloadStructure(packageData.Payload, effective); err != nil {
		return nil, err
	}
	logicalState := backupPayloadState(packageData.Payload, effective, false)
	resolvedScopes, dependencies, err := domain.ResolveBackupScopeClosure(logicalState, manifest.RequestedScopes)
	if err != nil || !backupScopesEqual(resolvedScopes, effective) {
		return nil, fmt.Errorf("%w:dependency-closure", ErrBackupArchiveIntegrity)
	}
	if !backupDependenciesEqual(dependencies, manifest.DependencyReasons) {
		return nil, fmt.Errorf("%w:dependency-reasons", ErrBackupArchiveIntegrity)
	}
	if manifest.IncludesLogsAndAudit != (packageData.Payload.LogsAndAudit != nil) {
		return nil, fmt.Errorf("%w:logs-and-audit", ErrBackupArchiveIntegrity)
	}
	if !manifest.IncludesSensitiveData && len(packageData.Payload.SecretMaterials) != 0 {
		return nil, fmt.Errorf("%w:unexpected-secret-material", ErrBackupArchiveIntegrity)
	}
	payloadBytes, err := CanonicalBackupPayload(packageData.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w:payload", ErrBackupArchiveInvalid)
	}
	hash := sha256.Sum256(payloadBytes)
	if manifest.PayloadSHA256 != hex.EncodeToString(hash[:]) {
		return nil, fmt.Errorf("%w:payload-hash", ErrBackupArchiveIntegrity)
	}
	if !intMapsEqual(manifest.RecordCounts, domain.BackupPayloadCounts(packageData.Payload)) {
		return nil, fmt.Errorf("%w:counts", ErrBackupArchiveIntegrity)
	}
	if err := validateBackupSecretMaterials(packageData.Payload, manifest.IncludesSensitiveData); err != nil {
		return nil, err
	}
	return payloadBytes, nil
}
