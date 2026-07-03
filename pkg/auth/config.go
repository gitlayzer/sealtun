package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Workspace struct {
	UID      string `json:"uid"`
	ID       string `json:"id"`
	TeamName string `json:"teamName"`
}

type AuthData struct {
	Region           string     `json:"region"`
	SealosDomain     string     `json:"sealos_domain,omitempty"`
	AccessToken      string     `json:"access_token"`
	RegionalToken    string     `json:"regional_token"`
	AuthenticatedAt  string     `json:"authenticated_at"`
	AuthMethod       string     `json:"auth_method"`
	CurrentWorkspace *Workspace `json:"current_workspace,omitempty"`
}

const (
	currentConfigDir = ".sealtun"
	legacyConfigDir  = ".sealos"
	profilesDirName  = "profiles"
	profileFileName  = "current_profile"
)

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type Profile struct {
	Name              string     `json:"name"`
	Region            string     `json:"region,omitempty"`
	SealosDomain      string     `json:"sealosDomain,omitempty"`
	AuthenticatedAt   string     `json:"authenticatedAt,omitempty"`
	WorkspaceID       string     `json:"workspaceId,omitempty"`
	WorkspaceName     string     `json:"workspaceName,omitempty"`
	Current           bool       `json:"current"`
	KubeconfigPresent bool       `json:"kubeconfigPresent"`
	Error             string     `json:"error,omitempty"`
	AuthData          *AuthData  `json:"-"`
	CurrentWorkspace  *Workspace `json:"-"`
}

// GetSealosDir returns the config directory, copying only Sealtun-owned legacy files when needed.
func GetSealosDir() (string, error) {
	return getSealtunDir(true)
}

// CurrentSealtunDir returns the active Sealtun config directory path without
// creating it or migrating legacy ~/.sealos data. Use this for scoped readers
// where reading alternate config roots would be surprising.
func CurrentSealtunDir() (string, error) {
	home, err := sealtunHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, currentConfigDir)
	if err := validateExistingPrivateDir(dir, "config directory"); err != nil {
		if os.IsNotExist(err) {
			return dir, nil
		}
		return dir, err
	}
	return dir, nil
}

func getSealtunDir(migrateLegacy bool) (string, error) {
	home, err := sealtunHomeDir()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(home, currentConfigDir)
	legacyDir := filepath.Join(home, legacyConfigDir)

	currentMissing, err := EnsurePrivateDir(dir, "config directory")
	if err != nil {
		return "", err
	}
	if currentMissing && migrateLegacy {
		if info, legacyErr := os.Lstat(legacyDir); legacyErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if err := copyLegacyConfigFiles(legacyDir, dir); err != nil {
				return "", fmt.Errorf("copy legacy config files from %s to %s: %w", legacyDir, dir, err)
			}
		}
	}

	// Verify directory is writable with a unique file so concurrent CLI starts
	// do not contend on a fixed probe path.
	file, err := os.CreateTemp(dir, ".write_test.*")
	if err != nil {
		return "", fmt.Errorf("config directory %s is not writable: %w", dir, err)
	}
	testFile := file.Name()
	if _, err := file.Write([]byte("ok")); err != nil {
		_ = file.Close()
		_ = os.Remove(testFile)
		return "", fmt.Errorf("config directory %s is not writable: %w", dir, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(testFile)
		return "", fmt.Errorf("config directory %s is not writable: %w", dir, err)
	}
	_ = os.Remove(testFile)

	return dir, nil
}

func sealtunHomeDir() (string, error) {
	if home := strings.TrimSpace(os.Getenv("SEALTUN_HOME")); home != "" {
		return home, nil
	}
	if os.Geteuid() == 0 && strings.TrimSpace(os.Getenv("SUDO_USER")) != "" {
		if sudoHome := strings.TrimSpace(os.Getenv("SUDO_HOME")); sudoHome != "" {
			return sudoHome, nil
		}
		sudoUser, err := user.Lookup(strings.TrimSpace(os.Getenv("SUDO_USER")))
		if err == nil && strings.TrimSpace(sudoUser.HomeDir) != "" {
			return sudoUser.HomeDir, nil
		}
	}
	return os.UserHomeDir()
}

// EnsurePrivateDir creates a private config subdirectory and rejects symlinks.
func EnsurePrivateDir(dir, label string) (bool, error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s %s must not be a symlink", label, dir)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s %s is not a directory", label, dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		// #nosec G302 -- directories require execute bits; 0700 keeps config private to the user.
		if err := os.Chmod(dir, 0o700); err != nil {
			return false, fmt.Errorf("restrict %s %s permissions: %w", label, dir, err)
		}
	}
	return false, nil
}

func validateExistingPrivateDir(dir, label string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %s must not be a symlink", label, dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %s is not a directory", label, dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s %s permissions must be 0700 or stricter", label, dir)
	}
	return nil
}

func copyLegacyConfigFiles(legacyDir, dir string) error {
	for _, name := range []string{"auth.json", "kubeconfig"} {
		if err := copyLegacyFile(filepath.Join(legacyDir, name), filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyLegacyFile(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}

	in, err := os.Open(src) // #nosec G304 -- legacy source is a validated regular file under the user-owned legacy config directory.
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- destination is a fixed file under the user-owned Sealtun config directory.
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// SaveAuthData saves auth and kubeconfig
func SaveAuthData(authData AuthData, kubeconfig string) error {
	dir, err := GetSealosDir()
	if err != nil {
		return err
	}

	return saveAuthDataToDir(dir, authData, kubeconfig)
}

func saveAuthDataToDir(dir string, authData AuthData, kubeconfig string) error {
	b, err := json.MarshalIndent(authData, "", "  ") // #nosec G117 -- auth data is intentionally persisted with 0600 permissions for CLI reuse.
	if err != nil {
		return err
	}

	kcPath := filepath.Join(dir, "kubeconfig")
	previousKubeconfig, hadPreviousKubeconfig, err := readExistingFile(kcPath)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(kcPath, []byte(kubeconfig), 0600); err != nil {
		return err
	}

	authPath := filepath.Join(dir, "auth.json")
	if err := writeFileAtomic(authPath, b, 0600); err != nil {
		if rollbackErr := restoreFile(kcPath, previousKubeconfig, hadPreviousKubeconfig, 0600); rollbackErr != nil {
			return fmt.Errorf("%w; failed to restore previous kubeconfig: %v", err, rollbackErr)
		}
		return err
	}

	return nil
}

func ValidateProfileName(name string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return "", fmt.Errorf("profile name is required")
	}
	if normalized == "." || normalized == ".." || !profileNamePattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid profile name %q: use 1-64 lowercase letters, numbers, dots, underscores, or hyphens, starting with a letter or number", name)
	}
	return normalized, nil
}

func ProfilesDir() (string, error) {
	root, err := GetSealosDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, profilesDirName)
	if _, err := EnsurePrivateDir(dir, "profiles directory"); err != nil {
		return "", err
	}
	return dir, nil
}

func profileDir(name string) (string, string, error) {
	normalized, err := ValidateProfileName(name)
	if err != nil {
		return "", "", err
	}
	root, err := ProfilesDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(root, normalized), normalized, nil
}

func SaveProfile(name string, authData AuthData, kubeconfig string) (string, error) {
	dir, normalized, err := profileDir(name)
	if err != nil {
		return "", err
	}
	if err := ensureProfileDir(dir, normalized); err != nil {
		return "", err
	}
	if err := saveAuthDataToDir(dir, authData, kubeconfig); err != nil {
		return "", err
	}
	return normalized, nil
}

func ensureProfileDir(dir, name string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("profile %s path is a symlink", name)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile %s is not a directory", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		// #nosec G302 -- profile directories require execute bits; 0700 keeps saved credentials private.
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restrict profile %s directory permissions: %w", name, err)
		}
	}
	return nil
}

func validateProfileDir(dir, name string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return fmt.Errorf("profile %s does not exist", name)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("profile %s path is a symlink", name)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile %s is not a directory", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		// #nosec G302 -- profile directories require execute bits; 0700 keeps saved credentials private.
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restrict profile %s directory permissions: %w", name, err)
		}
	}
	return nil
}

func LoadProfile(name string) (*Profile, string, error) {
	dir, normalized, err := profileDir(name)
	if err != nil {
		return nil, "", err
	}
	if err := validateProfileDir(dir, normalized); err != nil {
		return nil, "", err
	}
	authData, err := loadAuthDataFromPath(filepath.Join(dir, "auth.json"))
	if err != nil {
		return nil, "", err
	}
	kubeconfig, err := readRegularFile(filepath.Join(dir, "kubeconfig"), "profile kubeconfig")
	if err != nil {
		return nil, "", err
	}
	current, _ := CurrentProfileName()
	return profileFromAuthData(normalized, authData, current == normalized, true), string(kubeconfig), nil
}

func ActivateProfile(name string) error {
	profile, kubeconfig, err := LoadProfile(name)
	if err != nil {
		return err
	}
	if profile.AuthData == nil {
		return fmt.Errorf("profile %s has no auth data", profile.Name)
	}
	if err := SaveAuthData(*profile.AuthData, kubeconfig); err != nil {
		return err
	}
	return SetCurrentProfileName(profile.Name)
}

func DeleteProfile(name string) error {
	dir, normalized, err := profileDir(name)
	if err != nil {
		return err
	}
	if err := validateProfileDir(dir, normalized); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	current, err := CurrentProfileName()
	if err != nil {
		return err
	}
	if current == normalized {
		return ClearCurrentProfileName()
	}
	return nil
}

func ListProfiles() ([]Profile, error) {
	root, err := CurrentSealtunDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return []Profile{}, nil
	} else if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, profilesDirName)
	if err := validateExistingPrivateDir(dir, "profiles directory"); os.IsNotExist(err) {
		return []Profile{}, nil
	} else if err != nil {
		return nil, err
	}
	current, err := CurrentProfileNameFromDir(root)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	profiles := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name, err := ValidateProfileName(entry.Name())
		if err != nil {
			continue
		}
		profilePath := filepath.Join(dir, name)
		_, kubeconfigPresent, kubeconfigErr := readExistingFile(filepath.Join(profilePath, "kubeconfig"))
		authData, err := loadAuthDataFromPath(filepath.Join(profilePath, "auth.json"))
		if err != nil {
			profile := profileFromAuthData(name, nil, current == name, kubeconfigPresent)
			profile.Error = err.Error()
			profiles = append(profiles, *profile)
			continue
		}
		profile := profileFromAuthData(name, authData, current == name, kubeconfigPresent)
		if kubeconfigErr != nil {
			profile.Error = kubeconfigErr.Error()
		} else if !kubeconfigPresent {
			profile.Error = "kubeconfig is missing"
		}
		profiles = append(profiles, *profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, nil
}

func SetCurrentProfileName(name string) error {
	normalized, err := ValidateProfileName(name)
	if err != nil {
		return err
	}
	root, err := GetSealosDir()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(root, profileFileName), []byte(normalized+"\n"), 0o600)
}

func CurrentProfileName() (string, error) {
	root, err := GetSealosDir()
	if err != nil {
		return "", err
	}
	return CurrentProfileNameFromDir(root)
}

func CurrentProfileNameFromDir(root string) (string, error) {
	data, err := readRegularFile(filepath.Join(root, profileFileName), "current profile marker")
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ValidateProfileName(string(data))
}

func ClearCurrentProfileName() error {
	root, err := GetSealosDir()
	if err != nil {
		return err
	}
	path := filepath.Join(root, profileFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ActiveKubeconfig() (string, error) {
	dir, err := GetSealosDir()
	if err != nil {
		return "", err
	}
	data, err := readRegularFile(filepath.Join(dir, "kubeconfig"), "active kubeconfig")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func profileFromAuthData(name string, authData *AuthData, current bool, kubeconfigPresent bool) *Profile {
	profile := &Profile{
		Name:              name,
		Current:           current,
		KubeconfigPresent: kubeconfigPresent,
		AuthData:          authData,
	}
	if authData == nil {
		return profile
	}
	profile.Region = authData.Region
	profile.SealosDomain = authData.SealosDomain
	profile.AuthenticatedAt = authData.AuthenticatedAt
	profile.CurrentWorkspace = authData.CurrentWorkspace
	if authData.CurrentWorkspace != nil {
		profile.WorkspaceID = authData.CurrentWorkspace.ID
		profile.WorkspaceName = authData.CurrentWorkspace.TeamName
	}
	return profile
}

func readExistingFile(path string) ([]byte, bool, error) {
	data, err := readRegularFile(path, "config file")
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func restoreFile(path string, data []byte, exists bool, perm os.FileMode) error {
	if !exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, data, perm)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(path), os.Getpid(), time.Now().UnixNano()))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm) // #nosec G304 -- temp file is created next to a fixed Sealtun config file.
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// LoadAuthData reads the saved auth data
func LoadAuthData() (*AuthData, error) {
	dir, err := GetSealosDir()
	if err != nil {
		return nil, err
	}
	return LoadAuthDataFromDir(dir)
}

func LoadAuthDataFromDir(dir string) (*AuthData, error) {
	return loadAuthDataFromPath(filepath.Join(dir, "auth.json"))
}

func loadAuthDataFromPath(path string) (*AuthData, error) {
	b, err := readRegularFile(path, "auth file")
	if err != nil {
		return nil, err
	}
	var data AuthData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func readRegularFile(path, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", label, path)
	}
	return os.ReadFile(path) // #nosec G304 -- callers pass fixed Sealtun config paths or validated profile paths.
}

// ClearAuthData removes the persisted auth session and kubeconfig files.
func ClearAuthData() error {
	dir, err := GetSealosDir()
	if err != nil {
		return err
	}

	for _, name := range []string{"auth.json", "kubeconfig"} {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, profileFileName)); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
