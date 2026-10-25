package nomad

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type EnvironmentState struct {
	Version                int    `json:"version"`
	Revision               uint64 `json:"revision"`
	EnvironmentID          string `json:"environment_id"`
	Generation             int64  `json:"generation"`
	OperationID            string `json:"operation_id"`
	InstallationID         string `json:"installation_id"`
	ExecutionID            string `json:"execution_id"`
	InputDigest            string `json:"input_digest"`
	JobID                  string `json:"job_id"`
	Namespace              string `json:"namespace"`
	Region                 string `json:"region"`
	Datacenter             string `json:"datacenter"`
	NodeID                 string `json:"node_id"`
	Platform               string `json:"platform"`
	SSHHost                string `json:"ssh_host"`
	SSHUser                string `json:"ssh_user"`
	SSHPort                int    `json:"ssh_port"`
	SSHHostFingerprint     string `json:"ssh_host_fingerprint"`
	WorkspaceRoot          string `json:"workspace_root"`
	WorkspacePath          string `json:"workspace_path"`
	WorkspaceInputPath     string `json:"workspace_input_path"`
	WorkspaceInputCaptured bool   `json:"workspace_input_captured"`
	AllocationRoot         string `json:"allocation_root"`
	AgentctlPath           string `json:"agentctl_path,omitempty"`
	BootstrapPath          string `json:"bootstrap_path,omitempty"`
	NativeJobPolicyVersion int    `json:"native_job_policy_version,omitempty"`
	NativeJobSpecSHA256    string `json:"native_job_spec_sha256,omitempty"`
	CPUMHz                 int    `json:"cpu_mhz"`
	MemoryMB               int    `json:"memory_mb"`
	JobCreateIndex         uint64 `json:"job_create_index,omitempty"`
	JobDefinitionIndex     uint64 `json:"job_definition_index,omitempty"`
	AllocationID           string `json:"allocation_id,omitempty"`
	AllocationCreateIndex  uint64 `json:"allocation_create_index,omitempty"`
	ControlPort            int    `json:"control_port,omitempty"`
	InstancePort           int    `json:"instance_port,omitempty"`
	ServerIdentity         string `json:"server_identity,omitempty"`
	Phase                  string `json:"phase"`
	DrainReceiptDigest     string `json:"drain_receipt_digest,omitempty"`
	DrainReceiptGeneration int64  `json:"drain_receipt_generation,omitempty"`
}

func (s EnvironmentState) Validate() error {
	if !s.hasOperationIdentity() {
		return fmt.Errorf("nomad environment state has invalid immutable operation identity")
	}
	if !s.hasWorkerIdentity() {
		return fmt.Errorf("nomad environment state has incomplete worker identity")
	}
	if !s.hasSSHIdentity() {
		return fmt.Errorf("nomad environment state has incomplete SSH identity")
	}
	return s.validateWorkspaceAndEvidence()
}

func (s EnvironmentState) hasOperationIdentity() bool {
	return s.Version == 1 && s.EnvironmentID != "" && s.Generation >= 1 && s.OperationID != "" &&
		s.InstallationID != "" && s.ExecutionID != "" && digestPattern.MatchString(s.InputDigest) && s.JobID != ""
}

func (s EnvironmentState) hasWorkerIdentity() bool {
	return s.Namespace != "" && s.Region != "" && s.Datacenter != "" && s.NodeID != "" && s.Platform == "darwin/arm64"
}

func (s EnvironmentState) hasSSHIdentity() bool {
	return s.SSHHost != "" && s.SSHUser != "" && s.SSHPort >= 1 && s.SSHPort <= 65535 && strings.HasPrefix(s.SSHHostFingerprint, "SHA256:")
}

func (s EnvironmentState) validateWorkspaceAndEvidence() error {
	if s.WorkspaceRoot == "" || s.WorkspacePath == "" || s.AllocationRoot == "" || s.CPUMHz <= 0 || s.MemoryMB <= 0 {
		return fmt.Errorf("nomad environment state has incomplete resource identity")
	}
	if !s.WorkspaceInputCaptured && s.WorkspaceInputPath != "" {
		return fmt.Errorf("nomad environment state has workspace input without capture evidence")
	}
	if err := s.validateJobEvidence(); err != nil {
		return err
	}
	expectedWorkspacePath, err := EnvironmentWorkspacePath(s.WorkspaceRoot, s.EnvironmentID, s.Generation)
	if err != nil || expectedWorkspacePath != s.WorkspacePath {
		return fmt.Errorf("nomad environment state workspace does not match its owner and generation")
	}
	return s.validatePhaseAndIndexes()
}

func (s EnvironmentState) validateJobEvidence() error {
	jobEvidenceFields := []bool{s.AgentctlPath != "", s.BootstrapPath != "", s.NativeJobPolicyVersion != 0, s.NativeJobSpecSHA256 != ""}
	jobEvidenceCount := 0
	for _, present := range jobEvidenceFields {
		if present {
			jobEvidenceCount++
		}
	}
	if jobEvidenceCount != 0 && jobEvidenceCount != len(jobEvidenceFields) {
		return fmt.Errorf("nomad environment state has incomplete immutable job evidence")
	}
	if jobEvidenceCount == len(jobEvidenceFields) {
		if s.NativeJobPolicyVersion != NativeJobPolicyVersion || !digestPattern.MatchString(s.NativeJobSpecSHA256) {
			return fmt.Errorf("nomad environment state has unsupported native job policy evidence")
		}
		agentctlPath, agentctlErr := normalizedAbsolutePath(s.AgentctlPath)
		bootstrapPath, bootstrapErr := normalizedAbsolutePath(s.BootstrapPath)
		if agentctlErr != nil || bootstrapErr != nil ||
			!strings.HasPrefix(agentctlPath, s.AllocationRoot+string(filepath.Separator)) ||
			!strings.HasPrefix(bootstrapPath, s.AllocationRoot+string(filepath.Separator)) ||
			!strings.HasSuffix(bootstrapPath, ".sh") {
			return fmt.Errorf("nomad environment state has invalid staged artifact paths")
		}
	}
	return nil
}

func (s EnvironmentState) validatePhaseAndIndexes() error {
	switch s.Phase {
	case "intent", "registered", "allocated", "ready", "cleanup_pending", "quarantined", "absent":
	default:
		return fmt.Errorf("nomad environment state has invalid phase")
	}
	if err := s.validateRuntimeIndexes(); err != nil {
		return err
	}
	return s.validateDrainReceipt()
}

func (s EnvironmentState) validateRuntimeIndexes() error {
	if (s.AllocationID == "") != (s.AllocationCreateIndex == 0) {
		return fmt.Errorf("nomad allocation identity is incomplete")
	}
	if (s.JobCreateIndex == 0) != (s.JobDefinitionIndex == 0) {
		return fmt.Errorf("nomad job creation and definition indexes are incomplete")
	}
	if (s.ControlPort == 0) != (s.InstancePort == 0) || !validPortRange(s.ControlPort) || !validPortRange(s.InstancePort) {
		return fmt.Errorf("nomad runtime ports are incomplete or invalid")
	}
	return nil
}

func validPortRange(port int) bool {
	return port >= 0 && port <= 65535
}

func (s EnvironmentState) validateDrainReceipt() error {
	if (s.DrainReceiptDigest == "") != (s.DrainReceiptGeneration == 0) ||
		(s.DrainReceiptDigest != "" && (!digestPattern.MatchString(s.DrainReceiptDigest) || s.DrainReceiptGeneration != s.Generation)) {
		return fmt.Errorf("nomad drain receipt does not match the ownership generation")
	}
	if s.Phase == "absent" && s.DrainReceiptDigest == "" {
		return fmt.Errorf("nomad absence requires positive drain receipt evidence")
	}
	return nil
}

func MarshalEnvironmentState(state EnvironmentState) ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func UnmarshalEnvironmentState(data []byte) (EnvironmentState, error) {
	var state EnvironmentState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return EnvironmentState{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return EnvironmentState{}, fmt.Errorf("nomad environment state contains trailing data")
	}
	if err := state.Validate(); err != nil {
		return EnvironmentState{}, err
	}
	return state, nil
}

const EnvironmentStateMetadataKey = "nomad_environment_state"
const NativeJobPolicyVersion = 1

func PutEnvironmentState(metadata map[string]any, state EnvironmentState) error {
	if metadata == nil {
		return fmt.Errorf("nomad environment metadata is nil")
	}
	encoded, err := MarshalEnvironmentState(state)
	if err != nil {
		return err
	}
	metadata[EnvironmentStateMetadataKey] = json.RawMessage(encoded)
	return nil
}

func EnvironmentStateFromMetadata(metadata map[string]any) (EnvironmentState, error) {
	if metadata == nil {
		return EnvironmentState{}, fmt.Errorf("nomad environment metadata is missing")
	}
	value, ok := metadata[EnvironmentStateMetadataKey]
	if !ok || value == nil {
		return EnvironmentState{}, fmt.Errorf("nomad environment state is missing")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return EnvironmentState{}, fmt.Errorf("encode Nomad environment state: %w", err)
	}
	return UnmarshalEnvironmentState(encoded)
}

type JobIdentity struct {
	EnvironmentID  string
	Generation     int64
	OperationID    string
	InstallationID string
}

func StableJobID(identity JobIdentity) (string, error) {
	if identity.EnvironmentID == "" || identity.Generation < 1 || identity.OperationID == "" || identity.InstallationID == "" {
		return "", fmt.Errorf("nomad job identity is incomplete")
	}
	canonical := fmt.Sprintf("%s\x00%d\x00%s\x00%s", identity.EnvironmentID, identity.Generation, identity.OperationID, identity.InstallationID)
	digest := sha256.Sum256([]byte(canonical))
	return "kandev-" + hex.EncodeToString(digest[:16]), nil
}
