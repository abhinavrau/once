package system

import (
	"os"
	"path/filepath"
	"syscall"
)

// FreeDisk returns the free bytes on the filesystem containing path.
func FreeDisk(path string) (uint64, error) {
	_, _, free, err := statDisk(path)
	return free, err
}

// SameDevice reports whether two paths live on the same filesystem device.
// A path that does not exist yet is judged by its closest existing ancestor,
// since that is the device it would be created on.
func SameDevice(a, b string) (bool, error) {
	da, err := deviceOf(a)
	if err != nil {
		return false, err
	}
	db, err := deviceOf(b)
	if err != nil {
		return false, err
	}
	return da == db, nil
}

func statDisk(path string) (total, used, free uint64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return
	}

	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bfree * uint64(stat.Bsize)
	used = total - free
	return
}

// Helpers

func deviceOf(path string) (uint64, error) {
	for {
		var st syscall.Stat_t
		err := syscall.Stat(path, &st)
		if err == nil {
			return st.Dev, nil
		}
		if !os.IsNotExist(err) {
			return 0, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, err
		}
		path = parent
	}
}
