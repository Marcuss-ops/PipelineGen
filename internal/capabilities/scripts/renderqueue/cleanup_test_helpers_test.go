package renderqueue

func storeDynamicMapVideoCache(key, source string, telemetry []byte, cacheDirs ...string) error {
	_, _, err := storeDynamicMapVideoCacheVerified(key, source, telemetry, cacheDirs...)
	return err
}
