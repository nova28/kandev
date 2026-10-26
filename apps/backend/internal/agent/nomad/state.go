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
	"time"
)

const (
	phaseAllocated = "allocated"
	phaseReady     = "ready"
)

// PhaseOperatorReleased is the terminal phase of a retained environment whose
// absence an operator attested by hand. It is never proof of absence.
const PhaseOperatorReleased = "operator_released"

// OperatorAttestation is the operator's manual statement that the retained
// runtime is gone and the worker fence is removed.
type OperatorAttestation struct {
	ProcessTreeAbsent    bool   `json:"process_tree_absent"`
	JobStoppedPurgeFalse bool   `json:"job_stopped_purge_false"`
	FenceRemoved         bool   `json:"fence_removed"`
	Operator             string `json:"operator"`
}

// Complete reports whether every attested fact holds and an operator is named.
func (a OperatorAttestation) Complete() bool {
	return a.ProcessTreeAbsent && a.JobStoppedPurgeFalse && a.FenceRemoved && strings.TrimSpace(a.Operator) != ""
}

// OperatorAttestationDigest is the SHA-256 of the canonical attestation bound
// to the exact retained environment identity.
func OperatorAttestationDigest(state EnvironmentState, attestation OperatorAttestation) string {
	canonical := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%t\x00%t\x00%t\x00%s", state.NodeID, state.EnvironmentID, state.Generation,
		state.JobID, attestation.ProcessTreeAbsent, attestation.JobStoppedPurgeFalse, attestation.FenceRemoved, attestation.Operator)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

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
	QuarantineState        string `json:"quarantine_state,omitempty"`
	// OperatorAttestationDigest binds an operator_released state to the
	// operator's manual absence attestation. It is empty in every other phase.
	OperatorAttestationDigest string `json:"operator_attestation_digest,omitempty"`
	// DisconnectLostAfterSeconds is the immutable disconnect window of the job.
	// Zero is a retained environment created before the window was configurable.
	DisconnectLostAfterSeconds int64 `json:"disconnect_lost_after_seconds,omitempty"`
	// PlacementMode is ModePool for pool-placed environments; empty is legacy.
	PlacementMode string `json:"placement_mode,omitempty"`
	// NodeContractSHA256 binds the placed node's kandev.* metadata at attach time.
	NodeContractSHA256 string `json:"node_contract_sha256,omitempty"`
	NodeAttach         string `json:"node_attach,omitempty"`
	StateRoot          string `json:"state_root,omitempty"`
}

func (s EnvironmentState) Validate() error {
	switch s.PlacementMode {
	case "":
		if s.NodeContractSHA256 != "" || s.NodeAttach != "" || s.StateRoot != "" {
			return fmt.Errorf("nomad legacy environment state carries pool placement fields")
		}
	case ModePool:
		return s.validatePool()
	default:
		return fmt.Errorf("nomad environment state has invalid placement mode")
	}
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
	return s.Namespace != "" && s.Region != "" && s.Datacenter != "" && s.NodeID != "" && s.Platform == WorkerPlatform
}

func (s EnvironmentState) hasSSHIdentity() bool {
	return s.SSHHost != "" && s.SSHUser != "" && s.SSHPort >= 1 && s.SSHPort <= 65535 && strings.HasPrefix(s.SSHHostFingerprint, "SHA256:")
}

func (s EnvironmentState) validateWorkspaceAndEvidence() error {
	if s.WorkspaceRoot == "" || s.WorkspacePath == "" || s.AllocationRoot == "" || s.CPUMHz <= 0 || s.MemoryMB <= 0 {
		return fmt.Errorf("nomad environment state has incomplete resource identity")
	}
	if s.DisconnectLostAfterSeconds != 0 && (s.DisconnectLostAfterSeconds < int64(MinDisconnectLostAfter/time.Second) || s.DisconnectLostAfterSeconds > int64(MaxDisconnectLostAfter/time.Second)) {
		return fmt.Errorf("nomad environment state has an out-of-range disconnect window")
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
			(s.AllocationRoot != "" && (!strings.HasPrefix(agentctlPath, s.AllocationRoot+string(filepath.Separator)) ||
				!strings.HasPrefix(bootstrapPath, s.AllocationRoot+string(filepath.Separator)))) ||
			!strings.HasSuffix(bootstrapPath, ".sh") {
			return fmt.Errorf("nomad environment state has invalid staged artifact paths")
		}
	}
	return nil
}

func (s EnvironmentState) validatePhaseAndIndexes() error {
	switch s.Phase {
	case "intent", "registered", phaseAllocated, phaseReady, "cleanup_pending", "quarantined", "absent", PhaseOperatorReleased:
	default:
		return fmt.Errorf("nomad environment state has invalid phase")
	}
	switch s.QuarantineState {
	case "", QuarantinePending, QuarantineMarked:
	default:
		return fmt.Errorf("nomad environment state has invalid quarantine state")
	}
	if err := s.validateRuntimeIndexes(); err != nil {
		return err
	}
	if err := s.validateOperatorAttestation(); err != nil {
		return err
	}
	return s.validateDrainReceipt()
}

func (s EnvironmentState) validateOperatorAttestation() error {
	released := s.Phase == PhaseOperatorReleased
	if released != (s.OperatorAttestationDigest != "") ||
		(released && !digestPattern.MatchString(s.OperatorAttestationDigest)) {
		return fmt.Errorf("nomad operator release requires exactly one attestation digest")
	}
	return nil
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

// poolNodeRequired reports whether the placed node must be recorded: from the
// allocated phase onward, or once an allocation exists.
func (s EnvironmentState) poolNodeRequired() bool {
	return s.Phase == phaseAllocated || s.Phase == phaseReady || s.AllocationID != ""
}

func (s EnvironmentState) validatePool() error {
	if !s.hasOperationIdentity() {
		return fmt.Errorf("nomad environment state has invalid immutable operation identity")
	}
	if s.Namespace == "" || s.Region == "" || s.Datacenter == "" || s.Platform != WorkerPlatform || s.CPUMHz <= 0 || s.MemoryMB <= 0 {
		return fmt.Errorf("nomad pool environment state has incomplete resource identity")
	}
	if s.DisconnectLostAfterSeconds != 0 && (s.DisconnectLostAfterSeconds < int64(MinDisconnectLostAfter/time.Second) || s.DisconnectLostAfterSeconds > int64(MaxDisconnectLostAfter/time.Second)) {
		return fmt.Errorf("nomad environment state has an out-of-range disconnect window")
	}
	if !s.WorkspaceInputCaptured && s.WorkspaceInputPath != "" {
		return fmt.Errorf("nomad environment state has workspace input without capture evidence")
	}
	if err := s.validatePoolNode(); err != nil {
		return err
	}
	if err := s.validateJobEvidence(); err != nil {
		return err
	}
	return s.validatePhaseAndIndexes()
}

func (s EnvironmentState) validatePoolNode() error {
	recorded := s.NodeID != ""
	if !recorded {
		if s.poolNodeRequired() {
			return fmt.Errorf("nomad pool environment state requires the placed node once allocated")
		}
		if s.NodeContractSHA256 != "" || s.NodeAttach != "" || s.StateRoot != "" || s.SSHHost != "" || s.SSHPort != 0 ||
			s.SSHHostFingerprint != "" || s.WorkspaceRoot != "" || s.WorkspacePath != "" {
			return fmt.Errorf("nomad pool environment state has node details without a placed node")
		}
		return nil
	}
	return s.validateRecordedPoolNode()
}

func (s EnvironmentState) validateRecordedPoolNode() error {
	if !safeNodeID.MatchString(s.NodeID) || !digestPattern.MatchString(s.NodeContractSHA256) {
		return fmt.Errorf("nomad pool environment state has an invalid node identity")
	}
	if _, err := normalizedAbsolutePath(s.StateRoot); err != nil {
		return fmt.Errorf("nomad pool environment state has an invalid state root")
	}
	switch s.NodeAttach {
	case AttachLocal:
		if s.SSHHost != "" || s.SSHPort != 0 || s.SSHHostFingerprint != "" {
			return fmt.Errorf("nomad pool environment state has SSH identity on a local node")
		}
	case AttachSSH:
		if !s.hasSSHIdentityWithoutUser() {
			return fmt.Errorf("nomad environment state has incomplete SSH identity")
		}
	default:
		return fmt.Errorf("nomad pool environment state has invalid node attach")
	}
	expected, err := EnvironmentWorkspacePath(s.WorkspaceRoot, s.EnvironmentID, s.Generation)
	if err != nil || expected != s.WorkspacePath {
		return fmt.Errorf("nomad environment state workspace does not match its owner and generation")
	}
	return nil
}

func (s EnvironmentState) hasSSHIdentityWithoutUser() bool {
	return s.SSHHost != "" && s.SSHPort >= 1 && s.SSHPort <= 65535 && strings.HasPrefix(s.SSHHostFingerprint, "SHA256:")
}

// ValidatePoolStateTransition rejects a change to the node, transport or
// workspace identity of a pool environment once the node was recorded.
func ValidatePoolStateTransition(prev, next EnvironmentState) error {
	if prev.PlacementMode != next.PlacementMode {
		return fmt.Errorf("nomad environment placement mode is immutable")
	}
	if prev.PlacementMode != ModePool || prev.NodeID == "" {
		return nil
	}
	if prev.NodeID != next.NodeID || prev.NodeContractSHA256 != next.NodeContractSHA256 || prev.NodeAttach != next.NodeAttach ||
		prev.StateRoot != next.StateRoot || prev.SSHHost != next.SSHHost || prev.SSHPort != next.SSHPort ||
		prev.SSHHostFingerprint != next.SSHHostFingerprint || prev.WorkspaceRoot != next.WorkspaceRoot ||
		prev.WorkspacePath != next.WorkspacePath {
		return fmt.Errorf("nomad pool environment node identity is immutable once recorded")
	}
	return nil
}
