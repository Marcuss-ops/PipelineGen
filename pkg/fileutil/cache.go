package fileutil

import "os"

func UsableCachedClip(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		_ = os.Remove(path)
		return false, nil
	}
	if info.Size() <= 0 {
		_ = os.Remove(path)
		return false, nil
	}
	return true, nil
}
