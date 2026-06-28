package tui

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

func TestDiscoverUploadTasks(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sshmanager_upload_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	file1Path := filepath.Join(tempDir, "file1.txt")
	if err := os.WriteFile(file1Path, []byte("0123456789"), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}

	tasks, err := discoverUploadTasks(nil, file1Path, "/remote/file1.txt")
	if err != nil {
		t.Fatalf("discoverUploadTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Size != 10 {
		t.Errorf("expected size 10, got %d", tasks[0].Size)
	}
	if tasks[0].LocalPath != file1Path {
		t.Errorf("expected local path %q, got %q", file1Path, tasks[0].LocalPath)
	}
	if tasks[0].RemotePath != "/remote/file1.txt" {
		t.Errorf("expected remote path %q, got %q", "/remote/file1.txt", tasks[0].RemotePath)
	}
}

func TestDiscoverUploadTasksDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sshmanager_upload_dir_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a sub-directory and a file in it
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	file1Path := filepath.Join(subDir, "file1.txt")
	if err := os.WriteFile(file1Path, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}

	tasks, err := discoverUploadTasks(nil, tempDir, "/remote")
	if err != nil {
		t.Fatalf("discoverUploadTasks failed: %v", err)
	}

	// We expect 3 tasks: the root directory, the subdirectory, and the file
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	var rootTask, subdirTask, fileTask *fileTransferTask
	for i := range tasks {
		t := &tasks[i]
		if t.LocalPath == tempDir {
			rootTask = t
		} else if t.LocalPath == subDir {
			subdirTask = t
		} else if t.LocalPath == file1Path {
			fileTask = t
		}
	}

	if rootTask == nil || !rootTask.IsDir {
		t.Errorf("expected root directory task to be IsDir")
	}
	if subdirTask == nil || !subdirTask.IsDir {
		t.Errorf("expected subdir task to be IsDir")
	}
	if fileTask == nil || fileTask.IsDir || fileTask.Size != 5 {
		t.Errorf("expected file task to not be IsDir and have size 5")
	}
}
