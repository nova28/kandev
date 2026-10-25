package lifecycle

// Nomad environment phases persisted in the typed inventory state.
const (
	nomadPhaseIntent         = "intent"
	nomadPhaseRegistered     = "registered"
	nomadPhaseAllocated      = "allocated"
	nomadPhaseReady          = "ready"
	nomadPhaseCleanupPending = "cleanup_pending"
)

// nomadAllocationClientStatusRunning is the Nomad client status of a running allocation.
const nomadAllocationClientStatusRunning = "running"

// Task environment row statuses observed during Nomad recovery.
const (
	nomadEnvironmentStatusReady    = "ready"
	nomadEnvironmentStatusCreating = "creating"
)

// Nomad profile snapshot metadata keys.
const (
	nomadMetadataKeyAddress        = "address"
	nomadMetadataKeyRegion         = "region"
	nomadMetadataKeyDatacenter     = "datacenter"
	nomadMetadataKeyNodeID         = "node_id"
	nomadMetadataKeyPlatform       = "platform"
	nomadMetadataKeySSHTarget      = "ssh_target"
	nomadMetadataKeyWorkspaceRoot  = "workspace_root"
	nomadMetadataKeyAllocationRoot = "allocation_root"
	nomadMetadataKeyCPUMHz         = "cpu_mhz"
	nomadMetadataKeyMemoryMB       = "memory_mb"
	nomadMetadataKeyToken          = "nomad_token"
)

// Name prefixes for Nomad agentctl runtime credentials.
const (
	nomadCredentialPrefixBootstrap = "agentctl-bootstrap"
	nomadCredentialPrefixAuth      = "agentctl-auth"
)
