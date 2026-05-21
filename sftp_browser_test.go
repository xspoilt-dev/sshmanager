package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}

	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q; want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestRemoteJoin(t *testing.T) {
	tests := []struct {
		dir  string
		name string
		want string
	}{
		{"/", "test", "/test"},
		{"/home/user", "file.txt", "/home/user/file.txt"},
		{"/home/user/", "file.txt", "/home/user/file.txt"},
		{".", "file.txt", "./file.txt"},
	}

	for _, tt := range tests {
		got := remoteJoin(tt.dir, tt.name)
		if got != tt.want {
			t.Errorf("remoteJoin(%q, %q) = %q; want %q", tt.dir, tt.name, got, tt.want)
		}
	}
}

func TestCountLocalFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sshmanager_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	file1Path := filepath.Join(tempDir, "file1.txt")
	if err := os.WriteFile(file1Path, []byte("0123456789"), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}

	subdir := filepath.Join(tempDir, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	file2Path := filepath.Join(subdir, "file2.txt")
	if err := os.WriteFile(file2Path, []byte("01234567890123456789"), 0644); err != nil {
		t.Fatalf("failed to write file2: %v", err)
	}

	symlinkPath := filepath.Join(tempDir, "symlink_to_file")
	if err := os.Symlink("file1.txt", symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	totalSize, err := countLocalFiles(tempDir)
	if err != nil {
		t.Fatalf("countLocalFiles failed: %v", err)
	}

	var expectedSize int64 = 39
	symlinkInfo, err := os.Lstat(symlinkPath)
	if err == nil {
		expectedSize = 10 + 20 + symlinkInfo.Size()
	}

	if totalSize != expectedSize {
		t.Errorf("countLocalFiles = %d; want %d", totalSize, expectedSize)
	}
}
