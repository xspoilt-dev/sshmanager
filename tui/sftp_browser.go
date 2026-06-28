package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"sshmanager/config"
	sshcmd "sshmanager/ssh"
)

type Panel int

const (
	LocalPanel Panel = iota
	RemotePanel
	TerminalPanel
)

type FileItem struct {
	Name  string
	IsDir bool
	Size  int64
}

type SFTPBrowser struct {
	Server      config.Server
	sshClient   *ssh.Client
	sftpClient  *sftp.Client
	activePanel Panel

	localDir   string
	localItems []FileItem
	localIdx   int

	remoteDir   string
	remoteItems []FileItem
	remoteIdx   int

	width  int
	height int

	statusMsg    string
	isBusy       bool
	spinner      spinner.Model
	err          error
	initialized  bool
	progressChan chan sftpProgressMsg
	lastProgress sftpProgressMsg

	terminalSession *ssh.Session
	terminalIn      io.WriteCloser
	terminalEmu     *vt.SafeEmulator
	terminalRedraw  chan struct{}

	deleteConfirm bool
	deleteItem    FileItem
	deletePath    string

	terminalHeightPct int
	localWidthPct     int
}

// Msg types for SFTP
type sftpProgressMsg struct {
	Message string
	Percent float64
	Done    bool
	Err     error
}
type sftpConnectedMsg struct {
	sshClient       *ssh.Client
	sftpClient      *sftp.Client
	localDir        string
	remoteDir       string
	localItems      []FileItem
	remoteItems     []FileItem
	terminalSession *ssh.Session
	terminalIn      io.WriteCloser
	terminalEmu     *vt.SafeEmulator
	redrawChan      chan struct{}
}

type terminalRedrawMsg struct{}

func listenToTerminalRedraw(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-ch
		if !ok {
			return nil
		}
		return terminalRedrawMsg{}
	}
}

type sftpErrorMsg struct {
	err error
}

type sftpListMsg struct {
	panel Panel
	dir   string
	items []FileItem
}

type sftpTransferCompletedMsg struct {
	message string
	err     error
}

// Helpers for remote paths (Unix/VPS style)
func remoteDirUp(dir string) string {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return "/"
	}
	idx := strings.LastIndex(dir, "/")
	if idx <= 0 {
		return "/"
	}
	return dir[:idx]
}

func remoteJoin(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return strings.TrimSuffix(dir, "/") + "/" + name
}

// Format bytes to human readable format
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// List local files
func getLocalItems(dir string) ([]FileItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var items []FileItem
	// Add parent directory item if not at root
	parent := filepath.Dir(dir)
	if parent != dir {
		items = append(items, FileItem{Name: "..", IsDir: true})
	}

	var dirs []FileItem
	var files []FileItem

	for _, entry := range entries {
		info, err := entry.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}

		item := FileItem{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Size:  size,
		}

		if entry.IsDir() {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	items = append(items, dirs...)
	items = append(items, files...)

	return items, nil
}

// List remote files
func getRemoteItems(client *sftp.Client, dir string) ([]FileItem, error) {
	entries, err := client.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var items []FileItem
	// Add parent directory item if not at root
	if dir != "/" && dir != "." && dir != "" {
		items = append(items, FileItem{Name: "..", IsDir: true})
	}

	var dirs []FileItem
	var files []FileItem

	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}

		item := FileItem{
			Name:  name,
			IsDir: entry.IsDir(),
			Size:  entry.Size(),
		}

		if entry.IsDir() {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	items = append(items, dirs...)
	items = append(items, files...)

	return items, nil
}

// progress wrapper for reader
type progressReader struct {
	r          io.Reader
	onProgress func(n int)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.onProgress(n)
	}
	return n, err
}

// progress wrapper for writer
type progressWriter struct {
	w          io.Writer
	onProgress func(n int)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 {
		pw.onProgress(n)
	}
	return n, err
}

func countLocalFiles(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var totalSize int64
	err = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	return totalSize, err
}

func countRemoteFiles(client *sftp.Client, path string) (int64, error) {
	info, err := client.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var totalSize int64
	w := client.Walk(path)
	for w.Step() {
		if err := w.Err(); err != nil {
			return 0, err
		}
		stat := w.Stat()
		if stat != nil && !stat.IsDir() {
			totalSize += stat.Size()
		}
	}
	return totalSize, nil
}

type fileTransferTask struct {
	LocalPath     string
	RemotePath    string
	Size          int64
	IsDir         bool
	IsSymlink     bool
	SymlinkTarget string
}

func discoverUploadTasks(client *sftp.Client, localPath, remotePath string) ([]fileTransferTask, error) {
	info, err := os.Lstat(localPath)
	if err != nil {
		return nil, err
	}
	return discoverUploadTasksInternal(localPath, remotePath, info)
}

func discoverUploadTasksInternal(localPath, remotePath string, info os.FileInfo) ([]fileTransferTask, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(localPath)
		if err != nil {
			return nil, err
		}
		return []fileTransferTask{{
			LocalPath:     localPath,
			RemotePath:    remotePath,
			Size:          0,
			IsSymlink:     true,
			SymlinkTarget: target,
		}}, nil
	}

	if info.IsDir() {
		tasks := []fileTransferTask{{
			LocalPath:  localPath,
			RemotePath: remotePath,
			IsDir:      true,
		}}

		entries, err := os.ReadDir(localPath)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			subLocal := filepath.Join(localPath, entry.Name())
			subRemote := remoteJoin(remotePath, entry.Name())
			subInfo, err := entry.Info()
			if err != nil {
				return nil, err
			}
			subTasks, err := discoverUploadTasksInternal(subLocal, subRemote, subInfo)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, subTasks...)
		}
		return tasks, nil
	}

	return []fileTransferTask{{
		LocalPath:  localPath,
		RemotePath: remotePath,
		Size:       info.Size(),
	}}, nil
}

func discoverDownloadTasks(client *sftp.Client, remotePath, localPath string) ([]fileTransferTask, error) {
	info, err := client.Lstat(remotePath)
	if err != nil {
		return nil, err
	}
	return discoverDownloadTasksInternal(client, remotePath, localPath, info)
}

func discoverDownloadTasksInternal(client *sftp.Client, remotePath, localPath string, info os.FileInfo) ([]fileTransferTask, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := client.ReadLink(remotePath)
		if err != nil {
			return nil, err
		}
		return []fileTransferTask{{
			LocalPath:     localPath,
			RemotePath:    remotePath,
			Size:          0,
			IsSymlink:     true,
			SymlinkTarget: target,
		}}, nil
	}

	if info.IsDir() {
		tasks := []fileTransferTask{{
			LocalPath:  localPath,
			RemotePath: remotePath,
			IsDir:      true,
		}}

		entries, err := client.ReadDir(remotePath)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			subRemote := remoteJoin(remotePath, name)
			subLocal := filepath.Join(localPath, name)
			subTasks, err := discoverDownloadTasksInternal(client, subRemote, subLocal, entry)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, subTasks...)
		}
		return tasks, nil
	}

	return []fileTransferTask{{
		LocalPath:  localPath,
		RemotePath: remotePath,
		Size:       info.Size(),
	}}, nil
}

func getLocalSHA256(localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func getRemoteSHA256(sshClient *ssh.Client, remotePath string) (string, error) {
	if sshClient == nil {
		return "", fmt.Errorf("ssh client not available")
	}
	session, err := sshClient.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	escapedPath := strings.ReplaceAll(remotePath, "'", "'\\''")
	cmd := fmt.Sprintf("sha256sum '%s'", escapedPath)

	output, err := session.Output(cmd)
	if err != nil {
		// Fallback to shasum if sha256sum doesn't exist
		session2, err2 := sshClient.NewSession()
		if err2 != nil {
			return "", err
		}
		defer session2.Close()
		cmd = fmt.Sprintf("shasum -a 256 '%s'", escapedPath)
		output, err = session2.Output(cmd)
		if err != nil {
			return "", err
		}
	}

	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return "", fmt.Errorf("invalid sha256sum output: %s", string(output))
	}
	return fields[0], nil
}

func uploadSingleTask(sshClient *ssh.Client, sftpClient *sftp.Client, task fileTransferTask, sendProgress func(int64)) error {
	if task.IsSymlink {
		_ = sftpClient.Remove(task.RemotePath)
		err := sftpClient.Symlink(task.SymlinkTarget, task.RemotePath)
		sendProgress(0)
		return err
	}

	localStat, err := os.Stat(task.LocalPath)
	if err != nil {
		return err
	}

	// 1. Fast check: see if remote file exists and has the same size
	remoteStat, err := sftpClient.Stat(task.RemotePath)
	if err == nil && remoteStat.Size() == task.Size {
		// 2. Check modtime (within 1 second)
		timeDiff := remoteStat.ModTime().Sub(localStat.ModTime())
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}
		if timeDiff <= time.Second {
			// Size and modtime match, skip transfer
			sendProgress(task.Size)
			return nil
		}

		// 3. Fallback: check SHA256 if modtimes differ but sizes match
		localHash, errHash := getLocalSHA256(task.LocalPath)
		if errHash == nil {
			remoteHash, errHash := getRemoteSHA256(sshClient, task.RemotePath)
			if errHash == nil && localHash == remoteHash {
				// Hashes match, update remote modtime to match local and skip transfer
				_ = sftpClient.Chtimes(task.RemotePath, localStat.ModTime(), localStat.ModTime())
				sendProgress(task.Size)
				return nil
			}
		}
	}

	src, err := os.Open(task.LocalPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := sftpClient.Create(task.RemotePath)
	if err != nil {
		return err
	}
	defer dest.Close()

	pr := &progressReader{
		r: src,
		onProgress: func(n int) {
			sendProgress(int64(n))
		},
	}

	if _, err = io.Copy(dest, pr); err != nil {
		return err
	}

	// Preserve modification time
	_ = sftpClient.Chtimes(task.RemotePath, localStat.ModTime(), localStat.ModTime())
	return nil
}

func downloadSingleTask(sshClient *ssh.Client, sftpClient *sftp.Client, task fileTransferTask, sendProgress func(int64)) error {
	if task.IsSymlink {
		_ = os.Remove(task.LocalPath)
		err := os.Symlink(task.SymlinkTarget, task.LocalPath)
		sendProgress(0)
		return err
	}

	remoteStat, err := sftpClient.Stat(task.RemotePath)
	if err != nil {
		return err
	}

	// 1. Fast check: see if local file exists and has the same size
	localStat, err := os.Stat(task.LocalPath)
	if err == nil && localStat.Size() == task.Size {
		// 2. Check modtime (within 1 second)
		timeDiff := localStat.ModTime().Sub(remoteStat.ModTime())
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}
		if timeDiff <= time.Second {
			// Size and modtime match, skip transfer
			sendProgress(task.Size)
			return nil
		}

		// 3. Fallback: check SHA256 if modtimes differ but sizes match
		localHash, errHash := getLocalSHA256(task.LocalPath)
		if errHash == nil {
			remoteHash, errHash := getRemoteSHA256(sshClient, task.RemotePath)
			if errHash == nil && localHash == remoteHash {
				// Hashes match, update local modtime to match remote and skip transfer
				_ = os.Chtimes(task.LocalPath, remoteStat.ModTime(), remoteStat.ModTime())
				sendProgress(task.Size)
				return nil
			}
		}
	}

	src, err := sftpClient.Open(task.RemotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := os.Create(task.LocalPath)
	if err != nil {
		return err
	}
	defer dest.Close()

	pw := &progressWriter{
		w: dest,
		onProgress: func(n int) {
			sendProgress(int64(n))
		},
	}

	if _, err = io.Copy(pw, src); err != nil {
		return err
	}

	// Preserve modification time
	_ = os.Chtimes(task.LocalPath, remoteStat.ModTime(), remoteStat.ModTime())
	return nil
}

func uploadTasksConcurrent(sshClient *ssh.Client, sftpClient *sftp.Client, tasks []fileTransferTask, ch chan sftpProgressMsg, rootName string) error {
	// 1. Separate directory tasks and file/symlink tasks
	var dirTasks []fileTransferTask
	var fileTasks []fileTransferTask
	for _, task := range tasks {
		if task.IsDir {
			dirTasks = append(dirTasks, task)
		} else {
			fileTasks = append(fileTasks, task)
		}
	}

	// 2. Process directory creations first
	sort.Slice(dirTasks, func(i, j int) bool {
		return len(dirTasks[i].RemotePath) < len(dirTasks[j].RemotePath)
	})

	for _, task := range dirTasks {
		err := sftpClient.Mkdir(task.RemotePath)
		if err != nil {
			// Ignore if it already exists
			if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "Failure") {
				// Some servers might fail or return permission/etc, but we can try to proceed
			}
		}
	}

	totalBytes := int64(0)
	for _, task := range fileTasks {
		totalBytes += task.Size
	}

	var bytesCopied int64
	var lastUpdate time.Time = time.Now()
	var updateMutex sync.Mutex

	sendProgress := func(n int64) {
		updateMutex.Lock()
		bytesCopied += n
		now := time.Now()
		if now.Sub(lastUpdate) > 100*time.Millisecond || bytesCopied == totalBytes {
			lastUpdate = now
			var pct float64
			if totalBytes > 0 {
				pct = float64(bytesCopied) / float64(totalBytes)
			} else {
				pct = 1.0
			}
			updateMutex.Unlock()

			select {
			case ch <- sftpProgressMsg{
				Message: fmt.Sprintf("Uploading %s: %s / %s", rootName, formatSize(bytesCopied), formatSize(totalBytes)),
				Percent: pct,
			}:
			default:
			}
		} else {
			updateMutex.Unlock()
		}
	}

	sendProgress(0)

	// If there are no file tasks, we are done!
	if len(fileTasks) == 0 {
		return nil
	}

	taskChan := make(chan fileTransferTask, len(fileTasks))
	for _, task := range fileTasks {
		taskChan <- task
	}
	close(taskChan)

	var wg sync.WaitGroup
	numWorkers := 8
	if len(fileTasks) < numWorkers {
		numWorkers = len(fileTasks)
	}

	var errs []error
	var errMutex sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				errMutex.Lock()
				if len(errs) > 0 {
					errMutex.Unlock()
					continue
				}
				errMutex.Unlock()

				err := uploadSingleTask(sshClient, sftpClient, task, sendProgress)
				if err != nil {
					errMutex.Lock()
					errs = append(errs, err)
					errMutex.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func downloadTasksConcurrent(sshClient *ssh.Client, sftpClient *sftp.Client, tasks []fileTransferTask, ch chan sftpProgressMsg, rootName string) error {
	// 1. Separate directory tasks and file/symlink tasks
	var dirTasks []fileTransferTask
	var fileTasks []fileTransferTask
	for _, task := range tasks {
		if task.IsDir {
			dirTasks = append(dirTasks, task)
		} else {
			fileTasks = append(fileTasks, task)
		}
	}

	// 2. Process directory creations first locally
	sort.Slice(dirTasks, func(i, j int) bool {
		return len(dirTasks[i].LocalPath) < len(dirTasks[j].LocalPath)
	})

	for _, task := range dirTasks {
		err := os.MkdirAll(task.LocalPath, 0755)
		if err != nil {
			return err
		}
	}

	totalBytes := int64(0)
	for _, task := range fileTasks {
		totalBytes += task.Size
	}

	var bytesCopied int64
	var lastUpdate time.Time = time.Now()
	var updateMutex sync.Mutex

	sendProgress := func(n int64) {
		updateMutex.Lock()
		bytesCopied += n
		now := time.Now()
		if now.Sub(lastUpdate) > 100*time.Millisecond || bytesCopied == totalBytes {
			lastUpdate = now
			var pct float64
			if totalBytes > 0 {
				pct = float64(bytesCopied) / float64(totalBytes)
			} else {
				pct = 1.0
			}
			updateMutex.Unlock()

			select {
			case ch <- sftpProgressMsg{
				Message: fmt.Sprintf("Downloading %s: %s / %s", rootName, formatSize(bytesCopied), formatSize(totalBytes)),
				Percent: pct,
			}:
			default:
			}
		} else {
			updateMutex.Unlock()
		}
	}

	sendProgress(0)

	// If there are no file tasks, we are done!
	if len(fileTasks) == 0 {
		return nil
	}

	taskChan := make(chan fileTransferTask, len(fileTasks))
	for _, task := range fileTasks {
		taskChan <- task
	}
	close(taskChan)

	var wg sync.WaitGroup
	numWorkers := 8
	if len(fileTasks) < numWorkers {
		numWorkers = len(fileTasks)
	}

	var errs []error
	var errMutex sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				errMutex.Lock()
				if len(errs) > 0 {
					errMutex.Unlock()
					continue
				}
				errMutex.Unlock()

				err := downloadSingleTask(sshClient, sftpClient, task, sendProgress)
				if err != nil {
					errMutex.Lock()
					errs = append(errs, err)
					errMutex.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func connectSFTP(server config.Server, width, height int) tea.Cmd {
	return func() tea.Msg {
		sshClient, err := sshcmd.GetSSHClient(server)
		if err != nil {
			return sftpErrorMsg{err: fmt.Errorf("SSH connection failed: %w", err)}
		}

		sftpClient, err := sftp.NewClient(sshClient)
		if err != nil {
			sshClient.Close()
			return sftpErrorMsg{err: fmt.Errorf("SFTP session failed: %w", err)}
		}

		// Initial directories
		localDir, err := os.Getwd()
		if err != nil {
			localDir, _ = os.UserHomeDir()
		}

		// If a server project path exists and is valid, use it for local dir
		if server.ProjectPath != "" {
			if info, err := os.Stat(server.ProjectPath); err == nil && info.IsDir() {
				localDir = server.ProjectPath
			}
		}

		remoteDir := "."
		// Get absolute remote home
		if pwd, err := sftpClient.Getwd(); err == nil {
			remoteDir = pwd
		}

		localItems, err := getLocalItems(localDir)
		if err != nil {
			return sftpErrorMsg{err: fmt.Errorf("failed to read local dir: %w", err)}
		}

		remoteItems, err := getRemoteItems(sftpClient, remoteDir)
		if err != nil {
			return sftpErrorMsg{err: fmt.Errorf("failed to read remote dir: %w", err)}
		}

		// Calculate initial terminal height and width
		availHeight := height - 6
		if availHeight < 10 {
			availHeight = 10
		}
		termHeight := availHeight * 35 / 100
		if termHeight < 6 {
			termHeight = 6
		}
		if termHeight > 15 {
			termHeight = 15
		}
		termWidth := width - 4
		if termWidth < 20 {
			termWidth = 20
		}

		var termSession *ssh.Session
		var termIn io.WriteCloser
		var termEmu *vt.SafeEmulator
		redrawChan := make(chan struct{}, 10)

		session, err := sshClient.NewSession()
		if err == nil {
			modes := ssh.TerminalModes{
				ssh.ECHO:          1,
				ssh.TTY_OP_ISPEED: 14400,
				ssh.TTY_OP_OSPEED: 14400,
			}
			err = session.RequestPty("xterm-256color", termHeight, termWidth, modes)
			if err == nil {
				stdinPipe, err := session.StdinPipe()
				stdoutPipe, err := session.StdoutPipe()
				stderrPipe, err := session.StderrPipe()
				if err == nil {
					err = session.Shell()
					if err == nil {
						termSession = session
						termIn = stdinPipe
						termEmu = vt.NewSafeEmulator(termWidth, termHeight)

						// Start background goroutine to read remote shell stdout and update emulator
						go func(out io.Reader, emu *vt.SafeEmulator, rc chan struct{}) {
							buf := make([]byte, 4096)
							for {
								n, err := out.Read(buf)
								if err != nil {
									break
								}
								emu.Write(buf[:n])
								select {
								case rc <- struct{}{}:
								default:
								}
							}
						}(stdoutPipe, termEmu, redrawChan)

						// Start background goroutine to read remote shell stderr and update emulator
						go func(out io.Reader, emu *vt.SafeEmulator, rc chan struct{}) {
							buf := make([]byte, 4096)
							for {
								n, err := out.Read(buf)
								if err != nil {
									break
								}
								emu.Write(buf[:n])
								select {
								case rc <- struct{}{}:
								default:
								}
							}
						}(stderrPipe, termEmu, redrawChan)
					}
				}
			}
		}

		return sftpConnectedMsg{
			sshClient:       sshClient,
			sftpClient:      sftpClient,
			localDir:        localDir,
			remoteDir:       remoteDir,
			localItems:      localItems,
			remoteItems:     remoteItems,
			terminalSession: termSession,
			terminalIn:      termIn,
			terminalEmu:     termEmu,
			redrawChan:      redrawChan,
		}
	}
}

func listenToProgress(ch chan sftpProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (b *SFTPBrowser) startUpload(localPath, remotePath, filename string) tea.Cmd {
	b.progressChan = make(chan sftpProgressMsg, 100)
	b.lastProgress = sftpProgressMsg{}

	go func() {
		b.progressChan <- sftpProgressMsg{
			Message: "Scanning local directory...",
			Percent: 0,
		}

		tasks, err := discoverUploadTasks(b.sftpClient, localPath, remotePath)
		if err != nil {
			b.progressChan <- sftpProgressMsg{Done: true, Err: err}
			return
		}

		totalBytes := int64(0)
		for _, t := range tasks {
			totalBytes += t.Size
		}

		if len(tasks) == 0 {
			b.progressChan <- sftpProgressMsg{
				Done:    true,
				Message: "No files to upload.",
				Percent: 1.0,
			}
			return
		}

		err = uploadTasksConcurrent(b.sshClient, b.sftpClient, tasks, b.progressChan, filename)
		if err != nil {
			b.progressChan <- sftpProgressMsg{Done: true, Err: err}
			return
		}

		b.progressChan <- sftpProgressMsg{
			Done:    true,
			Message: fmt.Sprintf("Successfully uploaded %s (%s)", filename, formatSize(totalBytes)),
			Percent: 1.0,
		}
	}()

	return listenToProgress(b.progressChan)
}

func (b *SFTPBrowser) startDownload(remotePath, localPath, filename string) tea.Cmd {
	b.progressChan = make(chan sftpProgressMsg, 100)
	b.lastProgress = sftpProgressMsg{}

	go func() {
		b.progressChan <- sftpProgressMsg{
			Message: "Scanning remote directory...",
			Percent: 0,
		}

		tasks, err := discoverDownloadTasks(b.sftpClient, remotePath, localPath)
		if err != nil {
			b.progressChan <- sftpProgressMsg{Done: true, Err: err}
			return
		}

		totalBytes := int64(0)
		for _, t := range tasks {
			totalBytes += t.Size
		}

		if len(tasks) == 0 {
			b.progressChan <- sftpProgressMsg{
				Done:    true,
				Message: "No files to download.",
				Percent: 1.0,
			}
			return
		}

		err = downloadTasksConcurrent(b.sshClient, b.sftpClient, tasks, b.progressChan, filename)
		if err != nil {
			b.progressChan <- sftpProgressMsg{Done: true, Err: err}
			return
		}

		b.progressChan <- sftpProgressMsg{
			Done:    true,
			Message: fmt.Sprintf("Successfully downloaded %s (%s)", filename, formatSize(totalBytes)),
			Percent: 1.0,
		}
	}()

	return listenToProgress(b.progressChan)
}

// Initialise the browser
func NewSFTPBrowser(server config.Server) *SFTPBrowser {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return &SFTPBrowser{
		Server:            server,
		activePanel:       LocalPanel,
		spinner:           s,
		isBusy:            true,
		statusMsg:         "Connecting via SFTP...",
		terminalHeightPct: 35,
		localWidthPct:     50,
	}
}

// removeRemoteAll recursively deletes a file or folder on the remote server
func removeRemoteAll(client *sftp.Client, sshClient *ssh.Client, remotePath string) error {
	if sshClient != nil {
		session, err := sshClient.NewSession()
		if err == nil {
			defer session.Close()
			return session.Run(fmt.Sprintf("rm -rf %q", remotePath))
		}
	}

	// Fallback to SFTP recursion if SSH fails
	stat, err := client.Lstat(remotePath)
	if err != nil {
		// If it doesn't exist, we're done
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if stat.IsDir() {
		entries, err := client.ReadDir(remotePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			subPath := remoteJoin(remotePath, name)
			if err := removeRemoteAll(client, sshClient, subPath); err != nil {
				return err
			}
		}
		return client.RemoveDirectory(remotePath)
	}

	return client.Remove(remotePath)
}
