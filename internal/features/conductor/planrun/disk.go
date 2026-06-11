package planrun

// DiskChecker reports the bytes available to an unprivileged user on the
// filesystem backing a path. It is the injectable OS boundary for the
// disk-space preflight, mirroring how [Git] abstracts the git CLI so the
// preflight matrix stays unit-testable without touching a real volume.
type DiskChecker interface {
	// AvailableBytes returns the free space (for a non-root caller) on the
	// filesystem that holds path. A non-nil error means the platform could
	// not be measured; callers treat that as "skip the check" rather than a
	// hard failure, so an unmeasurable platform never blocks a run.
	AvailableBytes(path string) (uint64, error)
}

// defaultMinFreeDiskBytes is the conservative free-space floor enforced
// before a fresh worktree checkout when the caller does not specify one.
// It is a safety net against the catastrophic mid-run ENOSPC crash that a
// near-full disk produces, not a guarantee that a given install will fit:
// per-plan installs (e.g. a yarn monorepo's node_modules) can dwarf this.
const defaultMinFreeDiskBytes uint64 = 1 << 30 // 1 GiB
