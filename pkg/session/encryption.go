package session

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/labring/sealtun/pkg/auth"
)

// Session files hold the tunnel secret and an embedded kubeconfig (which itself
// carries a cluster bearer token). Persisting those as plaintext means a crash
// or SIGKILL leaves live credentials readable on disk (and in backups/Time
// Machine) until the next explicit scrub. To shrink that window we encrypt the
// on-disk blob at rest with AES-256-GCM, keyed by a local key file readable only
// by the owner. The in-memory representation is unchanged; only the bytes that
// touch the filesystem are protected.
const (
	sessionKeyFileName    = "session.key"
	encryptedSessionMagic = "STES1:" // prefix marking an encrypted session blob
)

// encryptSessionData encrypts a marshaled session for storage. Session files
// contain live credentials, so encryption failures must stop the write rather
// than silently fall back to plaintext.
func encryptSessionData(plaintext []byte) ([]byte, error) {
	key, err := sessionEncryptionKey()
	if err != nil {
		return nil, err
	}
	return encryptSessionDataWithKey(plaintext, key)
}

func encryptSessionDataWithKey(plaintext, key []byte) ([]byte, error) {
	gcm, err := newSessionAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := make([]byte, 0, len(encryptedSessionMagic)+base64.StdEncoding.EncodedLen(len(sealed)))
	encoded = append(encoded, encryptedSessionMagic...)
	b64 := make([]byte, base64.StdEncoding.EncodedLen(len(sealed)))
	base64.StdEncoding.Encode(b64, sealed)
	return append(encoded, b64...), nil
}

// decryptSessionData reverses encryptSessionData. Blobs without the magic prefix
// are treated as legacy plaintext and returned unchanged, so existing session
// files keep working without a migration step.
func decryptSessionData(data []byte) ([]byte, error) {
	if !isEncryptedSessionData(data) {
		return data, nil
	}
	key, err := sessionEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("load session key: %w", err)
	}
	return decryptSessionDataWithKey(data, key)
}

func decryptSessionDataWithKey(data, key []byte) ([]byte, error) {
	if !isEncryptedSessionData(data) {
		return data, nil
	}
	gcm, err := newSessionAEAD(key)
	if err != nil {
		return nil, err
	}
	return decryptSessionDataWithAEAD(data, gcm)
}

func newSessionAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decryptSessionDataWithAEAD(data []byte, gcm cipher.AEAD) ([]byte, error) {
	if !isEncryptedSessionData(data) {
		return data, nil
	}
	payload := data[len(encryptedSessionMagic):]
	sealed := make([]byte, base64.StdEncoding.DecodedLen(len(payload)))
	n, err := base64.StdEncoding.Decode(sealed, payload)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted session: %w", err)
	}
	sealed = sealed[:n]

	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("encrypted session is truncated")
	}
	nonce := sealed[:gcm.NonceSize()]
	ciphertext := sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt session: %w", err)
	}
	return plaintext, nil
}

func isEncryptedSessionData(data []byte) bool {
	return bytes.HasPrefix(data, []byte(encryptedSessionMagic))
}

// sessionEncryptionKey returns the local 32-byte session key, creating it on
// first use. The key file is written 0600 under the user-owned config dir.
func sessionEncryptionKey() ([]byte, error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return nil, err
	}
	return sessionEncryptionKeyFromConfigDir(root)
}

func sessionEncryptionKeyFromConfigDir(root string) ([]byte, error) {
	path := filepath.Join(root, sessionKeyFileName)

	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("session key %s is not a regular file", path)
		}
		key, err := os.ReadFile(path) // #nosec G304 -- fixed path under the user-owned config directory, Lstat-validated.
		if err != nil {
			return nil, err
		}
		if len(key) == 32 {
			return key, nil
		}
		// A malformed key file would make every session undecryptable; surface
		// it rather than silently regenerating and orphaning existing data.
		return nil, fmt.Errorf("session key %s is corrupt (expected 32 bytes, got %d)", path, len(key))
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// O_EXCL so a same-UID attacker cannot pre-create the key file as a symlink.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- fixed path under the user-owned config directory.
	if err != nil {
		if os.IsExist(err) {
			// Lost a race with another process. Re-validate the path is a regular
			// (non-symlink) file before reading, so an attacker who pre-created or
			// swapped the path for a symlink in the race window cannot redirect
			// this read.
			if info, lerr := os.Lstat(path); lerr == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return nil, fmt.Errorf("session key %s is not a regular file", path)
				}
				existing, rerr := os.ReadFile(path) // #nosec G304 -- fixed path under the user-owned config directory, Lstat-validated.
				if rerr == nil && len(existing) == 32 {
					return existing, nil
				}
			}
		}
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return key, nil
}
