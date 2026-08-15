package system

import "syscall"

// FreeDisk returns the free bytes on the filesystem containing path.
func FreeDisk(path string) (uint64, error) {
	_, _, free, err := statDisk(path)
	return free, err
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
