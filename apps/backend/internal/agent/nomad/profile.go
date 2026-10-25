package nomad

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kandev/kandev/internal/task/models"
)

var profileKeys = map[string]struct{}{
	"address": {}, "namespace": {}, "region": {}, "datacenter": {}, "node_id": {},
	"platform": {}, "ssh_target": {}, "ssh_port": {}, "ssh_host_fingerprint": {},
	"workspace_root": {}, "allocation_root": {}, "cpu_mhz": {}, "memory_mb": {},
	"nomad_token": {}, "disconnect_lost_after": {},
}

// Bounds and default for how long Nomad waits for a disconnected client before
// marking the single original allocation lost.
const (
	DefaultDisconnectLostAfter = 24 * time.Hour
	MinDisconnectLostAfter     = time.Minute
	MaxDisconnectLostAfter     = 7 * 24 * time.Hour
)

type ProfileConfig struct {
	Address            string
	Namespace          string
	Region             string
	Datacenter         string
	NodeID             string
	Platform           string
	SSHUser            string
	SSHHost            string
	SSHPort            int
	SSHHostFingerprint string
	WorkspaceRoot      string
	AllocationRoot     string
	CPUMHz             int
	MemoryMB           int
	NomadTokenSecretID string
	// DisconnectLostAfter is the bounded disconnect window of the native job.
	DisconnectLostAfter time.Duration
}

var environmentPathIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// EnvironmentWorkspacePath derives the immutable workspace directory for one
// task environment generation under an operator-configured profile root.
// Reusing the profile root directly would let separate task environments
// share a checkout and runtime files.
func EnvironmentWorkspacePath(profileRoot, environmentID string, generation int64) (string, error) {
	if profileRoot == "" || !filepath.IsAbs(profileRoot) || filepath.Clean(profileRoot) != profileRoot {
		return "", fmt.Errorf("workspace root must be an absolute normalized profile path")
	}
	if !environmentPathIDPattern.MatchString(environmentID) || generation < 1 {
		return "", fmt.Errorf("workspace path requires a safe environment ID and positive generation")
	}
	return filepath.Join(profileRoot, "environments", environmentID, fmt.Sprintf("g%d", generation)), nil
}

func ParseProfileConfig(config map[string]string) (ProfileConfig, error) {
	for key := range config {
		if _, ok := profileKeys[key]; !ok {
			return ProfileConfig{}, fmt.Errorf("unsupported Nomad profile field %q", key)
		}
	}
	get := func(key string) (string, error) {
		value := strings.TrimSpace(config[key])
		if value == "" {
			return "", fmt.Errorf("nomad profile field %q is required", key)
		}
		return value, nil
	}
	var profile ProfileConfig
	var err error
	if profile.Address, err = get("address"); err != nil {
		return profile, err
	}
	if err := validateAddress(profile.Address); err != nil {
		return profile, err
	}
	for key, target := range map[string]*string{
		"namespace": &profile.Namespace, "region": &profile.Region, "datacenter": &profile.Datacenter,
		"node_id": &profile.NodeID, "platform": &profile.Platform,
		"ssh_host_fingerprint": &profile.SSHHostFingerprint,
		"workspace_root":       &profile.WorkspaceRoot, "allocation_root": &profile.AllocationRoot,
	} {
		if *target, err = get(key); err != nil {
			return profile, err
		}
	}
	if profile.Platform != "darwin/arm64" {
		return profile, fmt.Errorf("unsupported Nomad worker platform %q", profile.Platform)
	}
	if err := parseProfileSSHAndSizing(&profile, config); err != nil {
		return profile, err
	}
	if err := normalizeProfilePaths(&profile); err != nil {
		return profile, err
	}
	if profile.DisconnectLostAfter, err = parseDisconnectLostAfter(config["disconnect_lost_after"]); err != nil {
		return profile, err
	}
	tokenRef, ok := models.ExecutorProfileSecretID(config["nomad_token"])
	if !ok || strings.TrimSpace(tokenRef) == "" {
		return profile, fmt.Errorf("nomad_token must be a Kandev secret reference")
	}
	profile.NomadTokenSecretID = tokenRef
	return profile, nil
}

func parseProfileSSHAndSizing(profile *ProfileConfig, config map[string]string) error {
	var err error
	if profile.SSHUser, profile.SSHHost, err = parseSSHTarget(config["ssh_target"]); err != nil {
		return err
	}
	if profile.SSHPort, err = positiveInt(config["ssh_port"]); err != nil {
		return fmt.Errorf("ssh_port: %w", err)
	}
	if profile.CPUMHz, err = positiveInt(config["cpu_mhz"]); err != nil {
		return fmt.Errorf("cpu_mhz: %w", err)
	}
	if profile.MemoryMB, err = positiveInt(config["memory_mb"]); err != nil {
		return fmt.Errorf("memory_mb: %w", err)
	}
	return nil
}

func normalizeProfilePaths(profile *ProfileConfig) error {
	var err error
	if !strings.HasPrefix(profile.SSHHostFingerprint, "SHA256:") || len(profile.SSHHostFingerprint) < 20 {
		return fmt.Errorf("ssh_host_fingerprint must be a pinned SHA256 fingerprint")
	}
	if profile.WorkspaceRoot, err = cleanAbsolutePath(profile.WorkspaceRoot); err != nil {
		return fmt.Errorf("workspace_root: %w", err)
	}
	if profile.AllocationRoot, err = cleanAbsolutePath(profile.AllocationRoot); err != nil {
		return fmt.Errorf("allocation_root: %w", err)
	}
	return nil
}

func validateAddress(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("nomad address must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("nomad API must use HTTPS except for loopback development")
}

func parseSSHTarget(raw string) (string, string, error) {
	value := strings.TrimSpace(raw)
	user, host, ok := strings.Cut(value, "@")
	if !ok || user == "" || host == "" || strings.Contains(host, "@") {
		return "", "", fmt.Errorf("ssh_target must be user@host")
	}
	for _, r := range user + host {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", "", fmt.Errorf("ssh_target contains invalid characters")
		}
	}
	return user, host, nil
}

func parseDisconnectLostAfter(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultDisconnectLostAfter, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < MinDisconnectLostAfter || value > MaxDisconnectLostAfter {
		return 0, fmt.Errorf("disconnect_lost_after must be a duration between %s and %s", MinDisconnectLostAfter, MaxDisconnectLostAfter)
	}
	return value, nil
}

func positiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return value, nil
}

func cleanAbsolutePath(raw string) (string, error) {
	cleaned := filepath.Clean(raw)
	if !filepath.IsAbs(raw) || cleaned != raw {
		return "", fmt.Errorf("must be a normalized absolute path")
	}
	return cleaned, nil
}
