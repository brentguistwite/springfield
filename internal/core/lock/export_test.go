package lock

// ReadHeldFromPath exposes readHeld for white-box tests that need to verify
// the fallback behavior (empty / missing lock file → ErrLockHeld{PID:0}).
func ReadHeldFromPath(path string) *ErrLockHeld {
	return readHeld(path)
}
