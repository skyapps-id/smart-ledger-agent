package database

import "os"

// mkdirAll dibungkus agar mudah di-mock saat testing.
func mkdirAll(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
