package application

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

func decryptBackupArchiveBody(destination io.Writer, source *bufio.Reader, password string, header backupArchiveEnvelope, headerBytes []byte) error {
	salt, _ := base64.RawStdEncoding.DecodeString(header.Salt)
	noncePrefix, _ := base64.RawStdEncoding.DecodeString(header.NoncePrefix)
	key := argon2.IDKey([]byte(password), salt, header.ArgonTime, header.ArgonMemoryKiB, header.ArgonThreads, chacha20poly1305.KeySize)
	defer clearBytes(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return err
	}
	aadInput := append(append([]byte{}, backupArchiveMagic...), headerBytes...)
	aadPrefix := sha256.Sum256(aadInput)
	for counter := uint64(0); ; counter++ {
		var sizeBytes [4]byte
		if _, err := io.ReadFull(source, sizeBytes[:]); err != nil {
			return ErrBackupArchiveTruncated
		}
		size := binary.BigEndian.Uint32(sizeBytes[:])
		if size == 0 {
			if _, err := source.Peek(1); !errors.Is(err, io.EOF) {
				return fmt.Errorf("%w:trailing-data", ErrBackupArchiveInvalid)
			}
			return nil
		}
		if uint64(size) > uint64(header.ChunkSize+aead.Overhead()) {
			return fmt.Errorf("%w:encrypted-chunk-size", ErrBackupArchiveInvalid)
		}
		ciphertext := make([]byte, int(size))
		if _, err := io.ReadFull(source, ciphertext); err != nil {
			return ErrBackupArchiveTruncated
		}
		nonce, additionalData, err := backupChunkParameters(noncePrefix, aadPrefix, counter)
		if err != nil {
			return err
		}
		plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
		if err != nil {
			return ErrBackupArchiveAuthentication
		}
		if _, err := destination.Write(plaintext); err != nil {
			return err
		}
	}
}

func backupChunkParameters(noncePrefix []byte, aadPrefix [sha256.Size]byte, counter uint64) ([]byte, []byte, error) {
	if len(noncePrefix) != backupArchiveNoncePrefixSize || counter == ^uint64(0) {
		return nil, nil, fmt.Errorf("%w:chunk-counter", ErrBackupArchiveInvalid)
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	copy(nonce, noncePrefix)
	binary.BigEndian.PutUint64(nonce[backupArchiveNoncePrefixSize:], counter)
	additionalData := make([]byte, sha256.Size+8)
	copy(additionalData, aadPrefix[:])
	binary.BigEndian.PutUint64(additionalData[sha256.Size:], counter)
	return nonce, additionalData, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
