package migrate

// SetSyncDir swaps the directory fsync for a test and returns a restore func.
func SetSyncDir(f func(dir string) error) (restore func()) {
	old := syncDir
	syncDir = f
	return func() { syncDir = old }
}
