package session

import (
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/labring/sealtun/pkg/auth"
)

const sessionsDirName = "sessions"
const sessionLockFileName = "sessions.lock"

const sessionLockWait = 10 * time.Second

var tunnelIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,53}[a-z0-9])?$`)

const (
	ConnectionStatePending    = "pending"
	ConnectionStateConnecting = "connecting"
	ConnectionStateConnected  = "connected"
	ConnectionStateError      = "error"
	ConnectionStateStopped    = "stopped"
)

type TunnelSession struct {
	TunnelID       string           `json:"tunnelId"`
	Region         string           `json:"region"`
	Namespace      string           `json:"namespace"`
	Kubeconfig     string           `json:"kubeconfig,omitempty"`
	Protocol       string           `json:"protocol"`
	Host           string           `json:"host"`
	SealosHost     string           `json:"sealosHost,omitempty"`
	CustomDomain   string           `json:"customDomain,omitempty"`
	PublicPort     int32            `json:"publicPort,omitempty"`
	LocalPort      string           `json:"localPort"`
	TargetURL      string           `json:"targetUrl,omitempty"`
	TargetTLS      *TargetTLSConfig `json:"targetTls,omitempty"`
	Secret         string           `json:"secret,omitempty"`
	BasicAuth      *BasicAuthConfig `json:"basicAuth,omitempty"`
	AccessPolicy   *AccessPolicy    `json:"accessPolicy,omitempty"`
	ResourceConfig *ResourceConfig  `json:"resourceConfig,omitempty"`
	TTL            string           `json:"ttl,omitempty"`
	ExpiresAt      string           `json:"expiresAt,omitempty"`
	Mode           string           `json:"mode,omitempty"`
	PID            int              `json:"pid"`
	// PIDStartToken is a best-effort fingerprint of the owning process (e.g. its
	// start time). It is verified alongside PID so a reused PID belonging to an
	// unrelated process is not mistaken for a still-alive tunnel owner.
	PIDStartToken   string `json:"pidStartToken,omitempty"`
	ConnectionState string `json:"connectionState,omitempty"`
	// CredentialsScrubbed marks a session whose secrets were intentionally
	// wiped (e.g. by logout). It is the authoritative scrub signal so that a
	// tunnel that legitimately has no Secret (BasicAuth/AccessPolicy only) is
	// not mistaken for a scrubbed one and silently stripped of its auth config.
	CredentialsScrubbed bool     `json:"credentialsScrubbed,omitempty"`
	LastError           string   `json:"lastError,omitempty"`
	LastConnectedAt     string   `json:"lastConnectedAt,omitempty"`
	UpdatedAt           string   `json:"updatedAt,omitempty"`
	CreatedAt           string   `json:"createdAt"`
	Resources           []string `json:"resources"`
}

type ResourceConfig struct {
	Requests *ResourceValues `json:"requests,omitempty"`
	Limits   *ResourceValues `json:"limits,omitempty"`
}

type ResourceValues struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type TargetTLSConfig struct {
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

type BasicAuthConfig struct {
	Enabled        bool   `json:"enabled"`
	Username       string `json:"username,omitempty"`
	PasswordHash   string `json:"passwordHash,omitempty"`
	PasswordSHA256 string `json:"passwordSha256,omitempty"`
}

type AccessPolicy struct {
	BearerTokenHashes []string         `json:"bearerTokenHashes,omitempty"`
	IPAllowlist       []string         `json:"ipAllowlist,omitempty"`
	IPDenylist        []string         `json:"ipDenylist,omitempty"`
	TemporaryTokens   []TemporaryToken `json:"temporaryTokens,omitempty"`
	RateLimit         string           `json:"rateLimit,omitempty"`
	Audit             *AuditConfig     `json:"audit,omitempty"`
}

type TemporaryToken struct {
	Name      string `json:"name,omitempty"`
	TokenHash string `json:"tokenHash"`
	TTL       string `json:"ttl,omitempty"`
	ExpiresAt string `json:"expiresAt"`
}

type AuditConfig struct {
	Enabled bool `json:"enabled"`
}

func SessionsDir() (string, error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return "", err
	}

	return SessionsDirFromConfigDir(root)
}

func SessionsDirFromConfigDir(root string) (string, error) {
	dir := filepath.Join(root, sessionsDirName)
	if _, err := auth.EnsurePrivateDir(dir, "sessions directory"); err != nil {
		return "", err
	}
	return dir, nil
}

func Save(session TunnelSession) error {
	dir, release, err := acquireSessionDir()
	if err != nil {
		return err
	}
	defer release()

	return saveLockedInDir(session, dir)
}

func Update(session TunnelSession) error {
	dir, release, err := acquireSessionDir()
	if err != nil {
		return err
	}
	defer release()

	if session.TunnelID == "" {
		return fmt.Errorf("session tunnel id is required")
	}
	if err := validateTunnelID(session.TunnelID); err != nil {
		return err
	}

	path := filepath.Join(dir, session.TunnelID+".json")
	if _, err := os.Stat(path); err != nil {
		return err
	}

	return saveLockedInDir(session, dir)
}

// UpdateAtomic applies a conditional mutation while holding the session file
// lock and returns the resulting record.
func UpdateAtomic(tunnelID string, mutate func(*TunnelSession) (bool, error)) (*TunnelSession, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return nil, err
	}
	if mutate == nil {
		return nil, fmt.Errorf("session mutation is required")
	}
	dir, release, err := acquireSessionDir()
	if err != nil {
		return nil, err
	}
	defer release()

	current, err := getLockedFromConfigDir(filepath.Dir(dir), tunnelID)
	if err != nil {
		return nil, err
	}
	changed, err := mutate(current)
	if err != nil {
		return nil, err
	}
	if current.TunnelID != tunnelID {
		return nil, fmt.Errorf("session mutation changed tunnel id %q to %q", tunnelID, current.TunnelID)
	}
	if changed {
		if err := saveLockedInDir(*current, dir); err != nil {
			return nil, err
		}
	}
	return current, nil
}

func acquireSessionDir() (string, func(), error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return "", nil, err
	}
	release, err := acquireSessionLockFromConfigDir(root)
	if err != nil {
		return "", nil, err
	}
	dir, err := SessionsDirFromConfigDir(root)
	if err != nil {
		release()
		return "", nil, err
	}
	return dir, release, nil
}

func saveLockedInDir(session TunnelSession, dir string) error {
	key, err := sessionEncryptionKeyFromConfigDir(filepath.Dir(dir))
	if err != nil {
		return fmt.Errorf("load session encryption key: %w", err)
	}
	return saveLockedInDirWithKey(session, dir, key)
}

func saveLockedInDirWithKey(session TunnelSession, dir string, key []byte) error {
	if err := validateTunnelID(session.TunnelID); err != nil {
		return err
	}

	if session.CreatedAt == "" {
		session.CreatedAt = time.Now().Format(time.RFC3339)
	}
	session.UpdatedAt = time.Now().Format(time.RFC3339)

	path := filepath.Join(dir, session.TunnelID+".json")
	if err := validateSessionFileForWrite(path, session.TunnelID); err != nil {
		return err
	}
	syncPIDStartToken(&session)
	if err := preserveScrubbedCredentials(path, &session, key); err != nil {
		return err
	}

	// Session files are encrypted and machine-read; compact JSON avoids writing
	// formatting whitespace on every daemon state update.
	plaintext, err := json.Marshal(session) // #nosec G117 -- tunnel secrets are encrypted at rest (see encryptSessionData) and the file is 0600.
	if err != nil {
		return err
	}
	data, err := encryptSessionDataWithKey(plaintext, key)
	if err != nil {
		return fmt.Errorf("encrypt session %s: %w", session.TunnelID, err)
	}

	// Create the temp file with O_EXCL so a same-UID attacker cannot pre-create
	// the (otherwise predictable) temp path as a symlink and redirect this write
	// to an arbitrary file. os.WriteFile would follow such a symlink.
	tmpPath := filepath.Join(dir, fmt.Sprintf("%s.%d.%d.tmp", session.TunnelID, os.Getpid(), time.Now().UnixNano()))
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- tmpPath is under the user-owned session directory; O_EXCL prevents symlink redirection.
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func validateSessionFileForWrite(path, tunnelID string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("session file %s is not a regular file", tunnelID)
	}
	return nil
}

func acquireSessionLockFromConfigDir(root string) (func(), error) {
	path := filepath.Join(root, sessionLockFileName)
	if err := validateSessionLockPath(path); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- lock path is fixed under the user-owned Sealtun config directory.
	if err != nil {
		return nil, err
	}
	if err := lockSessionFile(file, sessionLockWait); err != nil {
		_ = file.Close()
		return nil, err
	}

	return func() {
		_ = unlockSessionFile(file)
		_ = file.Close()
	}, nil
}

func validateSessionLockPath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("session lock %s is not a regular file", path)
	}
	return nil
}

func preserveScrubbedCredentials(path string, next *TunnelSession, key []byte) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from a validated tunnel ID under the session directory.
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err = decryptSessionDataWithKey(data, key)
	if err != nil {
		return fmt.Errorf("decrypt existing session %s: %w", next.TunnelID, err)
	}

	var existing TunnelSession
	if err := json.Unmarshal(data, &existing); err != nil {
		return nil
	}
	if existing.TunnelID != next.TunnelID {
		return nil
	}
	if existing.CredentialsScrubbed {
		next.Secret = ""
		next.BasicAuth = nil
		next.AccessPolicy = nil
		next.Kubeconfig = ""
		next.PID = 0
		next.ConnectionState = ConnectionStateStopped
		next.CredentialsScrubbed = true
		if next.LastError == "" {
			next.LastError = "local credentials scrubbed"
		}
	}
	return nil
}

func Delete(tunnelID string) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}

	dir, release, err := acquireSessionDir()
	if err != nil {
		return err
	}
	defer release()

	path := filepath.Join(dir, tunnelID+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ScrubCredentials() error {
	dir, release, err := acquireSessionDir()
	if err != nil {
		return err
	}
	defer release()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	key, err := sessionEncryptionKeyFromConfigDir(filepath.Dir(dir))
	if err != nil {
		return fmt.Errorf("load session encryption key: %w", err)
	}

	for _, entry := range entries {
		if !isSessionJSONFile(entry) {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path) // #nosec G304 -- entry is checked to be a regular .json file from the session directory.
		if err != nil {
			return err
		}
		data, err := decryptSessionDataWithKey(raw, key)
		if err != nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}

		var sess TunnelSession
		if err := json.Unmarshal(data, &sess); err != nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := validateTunnelID(sess.TunnelID); err != nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if sess.CredentialsScrubbed {
			continue
		}

		sess.Kubeconfig = ""
		sess.Secret = ""
		sess.BasicAuth = nil
		sess.AccessPolicy = nil
		sess.PID = 0
		sess.ConnectionState = ConnectionStateStopped
		sess.CredentialsScrubbed = true
		sess.LastError = "local credentials scrubbed"
		if err := saveLockedInDirWithKey(sess, dir, key); err != nil {
			return fmt.Errorf("scrub session %s credentials: %w", sess.TunnelID, err)
		}
	}

	return nil
}

func Get(tunnelID string) (*TunnelSession, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return nil, err
	}

	root, err := auth.CurrentSealtunDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(root); err != nil {
		return nil, err
	}
	if exists, err := sessionsDirExists(root); err != nil {
		return nil, err
	} else if !exists {
		return nil, os.ErrNotExist
	}

	release, err := acquireSessionLockFromConfigDir(root)
	if err != nil {
		return nil, err
	}
	defer release()

	return getLockedFromConfigDir(root, tunnelID)
}

func getLockedFromConfigDir(root, tunnelID string) (*TunnelSession, error) {
	dir, err := SessionsDirFromConfigDir(root)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, tunnelID+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("session file %s is not a regular file", tunnelID)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from a validated tunnel ID under the session directory.
	if err != nil {
		return nil, err
	}
	if isEncryptedSessionData(data) {
		key, keyErr := sessionEncryptionKeyFromConfigDir(root)
		if keyErr != nil {
			return nil, fmt.Errorf("load session key: %w", keyErr)
		}
		data, err = decryptSessionDataWithKey(data, key)
	}
	if err != nil {
		return nil, fmt.Errorf("decrypt session %s: %w", tunnelID, err)
	}

	var sess TunnelSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", tunnelID, err)
	}
	if sess.TunnelID != tunnelID {
		return nil, fmt.Errorf("session file %s contains tunnel id %q", tunnelID, sess.TunnelID)
	}
	if err := validateTunnelID(sess.TunnelID); err != nil {
		return nil, err
	}
	return &sess, nil
}

func List() ([]TunnelSession, error) {
	root, err := auth.CurrentSealtunDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return []TunnelSession{}, nil
	} else if err != nil {
		return nil, err
	}
	if exists, err := sessionsDirExists(root); err != nil {
		return nil, err
	} else if !exists {
		return []TunnelSession{}, nil
	}

	release, err := acquireSessionLockFromConfigDir(root)
	if err != nil {
		return nil, err
	}
	defer release()

	return listLockedFromConfigDir(root)
}

func sessionsDirExists(root string) (bool, error) {
	dir := filepath.Join(root, sessionsDirName)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("sessions directory %s is not a directory", dir)
	}
	return true, nil
}

func ListFromConfigDir(root string) ([]TunnelSession, error) {
	release, err := acquireSessionLockFromConfigDir(root)
	if err != nil {
		return nil, err
	}
	defer release()

	return listLockedFromConfigDir(root)
}

func listLockedFromConfigDir(root string) ([]TunnelSession, error) {
	dir, err := SessionsDirFromConfigDir(root)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	sessions := make([]TunnelSession, 0, len(entries))
	var sessionAEAD cipher.AEAD
	for _, entry := range entries {
		if !isSessionJSONFile(entry) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name())) // #nosec G304 -- entry is checked to be a regular .json file from the session directory.
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if isEncryptedSessionData(data) && sessionAEAD == nil {
			key, keyErr := sessionEncryptionKeyFromConfigDir(root)
			if keyErr != nil {
				return nil, fmt.Errorf("load session encryption key: %w", keyErr)
			}
			sessionAEAD, err = newSessionAEAD(key)
			if err != nil {
				return nil, fmt.Errorf("initialize session decryption: %w", err)
			}
		}
		if sessionAEAD != nil && isEncryptedSessionData(data) {
			data, err = decryptSessionDataWithAEAD(data, sessionAEAD)
			if err != nil {
				continue
			}
		}

		var sess TunnelSession
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		if err := validateTunnelID(sess.TunnelID); err != nil {
			continue
		}
		sessions = append(sessions, sess)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].TunnelID < sessions[j].TunnelID
	})

	return sessions, nil
}

func isSessionJSONFile(entry os.DirEntry) bool {
	if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
		return false
	}
	info, err := entry.Info()
	return err == nil && info.Mode().IsRegular()
}

func validateTunnelID(tunnelID string) error {
	if tunnelID == "" {
		return fmt.Errorf("session tunnel id is required")
	}
	if !tunnelIDPattern.MatchString(tunnelID) {
		return fmt.Errorf("invalid session tunnel id %q", tunnelID)
	}
	return nil
}

func ValidateTunnelIDForExternalUse(tunnelID string) error {
	return validateTunnelID(tunnelID)
}

func LockFileForExternalUse(file *os.File, timeout time.Duration) (func(), error) {
	if file == nil {
		return nil, fmt.Errorf("lock file is required")
	}
	if err := lockSessionFile(file, timeout); err != nil {
		return nil, err
	}
	return func() {
		_ = unlockSessionFile(file)
	}, nil
}

func IsStale(sess TunnelSession, gracePeriod time.Duration) bool {
	return IsStaleWithOwner(sess, gracePeriod, OwnerAlive(sess))
}

func IsStaleWithOwner(sess TunnelSession, gracePeriod time.Duration, ownerAlive bool) bool {
	if sess.ConnectionState == ConnectionStateStopped {
		return true
	}

	if ownerAlive {
		return false
	}

	if gracePeriod <= 0 {
		return true
	}

	ts := sess.UpdatedAt
	if ts == "" {
		ts = sess.CreatedAt
	}
	if ts == "" {
		return true
	}

	createdAt, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return true
	}

	return time.Since(createdAt) >= gracePeriod
}

func OwnerAlive(sess TunnelSession) bool {
	if !ProcessAlive(sess.PID) {
		return false
	}
	// Defend against PID reuse: if we recorded a start token when the PID was
	// captured, the currently-live PID must still carry the same token. A
	// mismatch means the original owner died and the PID was recycled by an
	// unrelated process. When either token is empty (older sessions, or a
	// platform that cannot resolve it) we fall back to the PID-only check.
	if sess.PIDStartToken == "" {
		return true
	}
	current := ProcessStartToken(sess.PID)
	if current == "" {
		return true
	}
	return current == sess.PIDStartToken
}

// syncPIDStartToken keeps PIDStartToken consistent with PID on every save. When
// a session records the current process as its owner, capture that process's
// start token so later liveness checks can detect PID reuse. When PID is cleared
// (stopped/scrubbed) or refers to another process, drop any stale token rather
// than persist one that no longer corresponds to PID.
func syncPIDStartToken(session *TunnelSession) {
	if session.PID <= 0 {
		session.PIDStartToken = ""
		return
	}
	if session.PID == os.Getpid() {
		session.PIDStartToken = ProcessStartToken(session.PID)
		return
	}
	// PID belongs to another process (e.g. the daemon writing on behalf of a
	// tunnel it owns); only refresh the token if we don't already have one.
	if session.PIDStartToken == "" {
		session.PIDStartToken = ProcessStartToken(session.PID)
	}
}

func RuntimeStatus(sess TunnelSession) string {
	return RuntimeStatusWithOwner(sess, OwnerAlive(sess))
}

func RuntimeStatusWithOwner(sess TunnelSession, ownerAlive bool) string {
	if sess.ConnectionState == ConnectionStateStopped {
		return "stopped"
	}

	if !ownerAlive {
		return "stale"
	}

	if sess.Mode != "daemon" {
		return "active"
	}

	switch sess.ConnectionState {
	case ConnectionStateConnected:
		return "active"
	case ConnectionStatePending, ConnectionStateConnecting:
		return "connecting"
	case ConnectionStateError:
		return "error"
	default:
		return "stale"
	}
}
