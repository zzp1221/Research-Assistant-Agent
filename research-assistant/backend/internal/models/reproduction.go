package models

import "time"

// RuntimePermissionMode controls how the runtime gate treats important actions.
type RuntimePermissionMode string

const (
	RuntimePermissionConfirmImportant RuntimePermissionMode = "confirm_important"
	RuntimePermissionFullAccess       RuntimePermissionMode = "full_access"
)

// RuntimeHardLimits are enforced for both permission modes.
type RuntimeHardLimits struct {
	MaxImageBytes     int64    `json:"max_image_bytes,omitempty"`
	MaxDownloadBytes  int64    `json:"max_download_bytes,omitempty"`
	MaxDiskGB         float64  `json:"max_disk_gb,omitempty"`
	MaxRuntimeSec     int      `json:"max_runtime_sec,omitempty"`
	AllowedRegistries []string `json:"allowed_registries,omitempty"`
}

// RuntimeGrant records a user approval that can be reused for the same action kind.
type RuntimeGrant struct {
	Action    RuntimeActionKind `json:"action"`
	GrantedBy string            `json:"granted_by,omitempty"`
	GrantedAt time.Time         `json:"granted_at"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
}

// RuntimePolicy is persisted with the plan so approval decisions survive restarts.
type RuntimePolicy struct {
	Mode           RuntimePermissionMode `json:"mode"`
	HardLimits     RuntimeHardLimits     `json:"hard_limits,omitempty"`
	Grants         []RuntimeGrant        `json:"grants,omitempty"`
	RequestedBy    string                `json:"requested_by,omitempty"`
	RequestedAt    time.Time             `json:"requested_at,omitempty"`
	AcknowledgedAt *time.Time            `json:"acknowledged_at,omitempty"`
}

// RuntimeActionKind enumerates operations that may need explicit approval.
type RuntimeActionKind string

const (
	RuntimeActionImagePull         RuntimeActionKind = "image_pull"
	RuntimeActionDependencyInstall RuntimeActionKind = "dependency_install"
	RuntimeActionSystemPackage     RuntimeActionKind = "system_package_install"
	RuntimeActionDatasetDownload   RuntimeActionKind = "dataset_download"
	RuntimeActionModelDownload     RuntimeActionKind = "model_download"
	RuntimeActionGPUUse            RuntimeActionKind = "gpu_use"
	RuntimeActionNetworkAccess     RuntimeActionKind = "network_access"
)

// RuntimeActionStatus is the lifecycle of a runtime approval request.
type RuntimeActionStatus string

const (
	RuntimeActionPending  RuntimeActionStatus = "pending"
	RuntimeActionApproved RuntimeActionStatus = "approved"
	RuntimeActionRejected RuntimeActionStatus = "rejected"
	RuntimeActionExpired  RuntimeActionStatus = "expired"
	RuntimeActionCanceled RuntimeActionStatus = "canceled"
)

// RuntimeActionRequest is persisted in PlanGraph.RuntimeActions.
type RuntimeActionRequest struct {
	ID             string              `json:"id"`
	PlanID         string              `json:"plan_id"`
	TaskID         string              `json:"task_id"`
	NodeType       string              `json:"node_type"`
	Kind           RuntimeActionKind   `json:"kind"`
	Title          string              `json:"title"`
	Details        map[string]any      `json:"details,omitempty"`
	Risk           string              `json:"risk,omitempty"`
	EstimatedBytes int64               `json:"estimated_bytes,omitempty"`
	EstimatedSec   int                 `json:"estimated_sec,omitempty"`
	Status         RuntimeActionStatus `json:"status"`
	RequestedAt    time.Time           `json:"requested_at"`
	DecidedAt      *time.Time          `json:"decided_at,omitempty"`
	DecidedBy      string              `json:"decided_by,omitempty"`
	DecisionNote   string              `json:"decision_note,omitempty"`
	AutoApproved   bool                `json:"auto_approved,omitempty"`
}

// ReproductionRuntimeSpec is the typed runtime contract for one paper reproduction.
type ReproductionRuntimeSpec struct {
	Version            string        `json:"version"`
	Language           string        `json:"language"`
	BaseImage          string        `json:"base_image"`
	ImageDigest        string        `json:"image_digest,omitempty"`
	PythonVersion      string        `json:"python_version,omitempty"`
	SystemPackages     []string      `json:"system_packages,omitempty"`
	PythonDependencies []PackageSpec `json:"python_dependencies,omitempty"`
	GPURequired        bool          `json:"gpu_required,omitempty"`
	GPUType            string        `json:"gpu_type,omitempty"`
	CUDAVersion        string        `json:"cuda_version,omitempty"`
	DatasetRefs        []DatasetRef  `json:"dataset_refs,omitempty"`
	ModelRefs          []ModelRef    `json:"model_refs,omitempty"`
	RunCommand         []string      `json:"run_command,omitempty"`
	Entrypoint         string        `json:"entrypoint,omitempty"`
	EntrypointRelative string        `json:"entrypoint_relative,omitempty"`
	PythonPath         []string      `json:"python_path,omitempty"`
	WorkingDir         string        `json:"working_dir,omitempty"`
	NetworkAccess      bool          `json:"network_access,omitempty"`
	DiskGB             float64       `json:"disk_gb,omitempty"`
	MemoryGB           float64       `json:"memory_gb,omitempty"`
	TimeoutSec         int           `json:"timeout_sec,omitempty"`
}

type PackageSpec struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type DatasetRef struct {
	Name      string `json:"name"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
}

type ModelRef struct {
	Name      string `json:"name"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
}

// EnvironmentManifest is the auditable result of runtime validation.
type EnvironmentManifest struct {
	Version        string            `json:"version"`
	Image          string            `json:"image"`
	ImageDigest    string            `json:"image_digest,omitempty"`
	Language       string            `json:"language,omitempty"`
	PythonVersion  string            `json:"python_version,omitempty"`
	PipFreeze      []PackageSpec     `json:"pip_freeze,omitempty"`
	SystemPackages []string          `json:"system_packages,omitempty"`
	GPU            string            `json:"gpu,omitempty"`
	CUDAVersion    string            `json:"cuda_version,omitempty"`
	DatasetHashes  map[string]string `json:"dataset_hashes,omitempty"`
	ModelHashes    map[string]string `json:"model_hashes,omitempty"`
	RunCommand     []string          `json:"run_command,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	// NetworkMode records whether the runtime ever had egress (none/bridge) and
	// Network*Bytes the measured traffic, so a reproduction is auditable.
	NetworkMode    string               `json:"network_mode,omitempty"`
	NetworkRXBytes int64                `json:"network_rx_bytes,omitempty"`
	NetworkTXBytes int64                `json:"network_tx_bytes,omitempty"`
	HardLimits     *RuntimeHardLimits   `json:"hard_limits,omitempty"`
	StartedAt      time.Time            `json:"started_at"`
	FinishedAt     *time.Time           `json:"finished_at,omitempty"`
	Status         string               `json:"status"`
	Missing        []MissingRequirement `json:"missing,omitempty"`
}
