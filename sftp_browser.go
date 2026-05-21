package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
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
	Server      Server
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

func uploadItemWithProgress(client *sftp.Client, localPath, remotePath string, bytesCopied *int64, lastUpdate *time.Time, totalBytes int64, ch chan sftpProgressMsg, rootName string) error {
	info, err := os.Lstat(localPath)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(localPath)
		if err != nil {
			return err
		}
		_ = client.Remove(remotePath)
		return client.Symlink(target, remotePath)
	}

	if info.IsDir() {
		if stat, err := client.Stat(remotePath); err == nil {
			if !stat.IsDir() {
				return fmt.Errorf("remote path %s exists but is not a directory", remotePath)
			}
		} else {
			err = client.Mkdir(remotePath)
			if err != nil {
				if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "Failure") {
					return err
				}
			}
		}

		entries, err := os.ReadDir(localPath)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			subLocal := filepath.Join(localPath, entry.Name())
			subRemote := remoteJoin(remotePath, entry.Name())
			if err := uploadItemWithProgress(client, subLocal, subRemote, bytesCopied, lastUpdate, totalBytes, ch, rootName); err != nil {
				return err
			}
		}
		return nil
	}

	// File upload
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	defer dest.Close()

	pr := &progressReader{
		r: src,
		onProgress: func(n int) {
			*bytesCopied += int64(n)
			if time.Since(*lastUpdate) > 100*time.Millisecond || *bytesCopied == totalBytes {
				*lastUpdate = time.Now()
				var pct float64
				if totalBytes > 0 {
					pct = float64(*bytesCopied) / float64(totalBytes)
				} else {
					pct = 1.0
				}
				select {
				case ch <- sftpProgressMsg{
					Message: fmt.Sprintf("Uploading %s: %s / %s", rootName, formatSize(*bytesCopied), formatSize(totalBytes)),
					Percent: pct,
				}:
				default:
				}
			}
		},
	}

	_, err = io.Copy(dest, pr)
	return err
}

func downloadItemWithProgress(client *sftp.Client, remotePath, localPath string, bytesCopied *int64, lastUpdate *time.Time, totalBytes int64, ch chan sftpProgressMsg, rootName string) error {
	info, err := client.Lstat(remotePath)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := client.ReadLink(remotePath)
		if err != nil {
			return err
		}
		_ = os.Remove(localPath)
		return os.Symlink(target, localPath)
	}

	if info.IsDir() {
		err = os.MkdirAll(localPath, 0755)
		if err != nil {
			return err
		}

		entries, err := client.ReadDir(remotePath)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			subRemote := remoteJoin(remotePath, name)
			subLocal := filepath.Join(localPath, name)
			if err := downloadItemWithProgress(client, subRemote, subLocal, bytesCopied, lastUpdate, totalBytes, ch, rootName); err != nil {
				return err
			}
		}
		return nil
	}

	// File download
	src, err := client.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dest.Close()

	pw := &progressWriter{
		w: dest,
		onProgress: func(n int) {
			*bytesCopied += int64(n)
			if time.Since(*lastUpdate) > 100*time.Millisecond || *bytesCopied == totalBytes {
				*lastUpdate = time.Now()
				var pct float64
				if totalBytes > 0 {
					pct = float64(*bytesCopied) / float64(totalBytes)
				} else {
					pct = 1.0
				}
				select {
				case ch <- sftpProgressMsg{
					Message: fmt.Sprintf("Downloading %s: %s / %s", rootName, formatSize(*bytesCopied), formatSize(totalBytes)),
					Percent: pct,
				}:
				default:
				}
			}
		},
	}

	_, err = io.Copy(pw, src)
	return err
}

func connectSFTP(server Server, width, height int) tea.Cmd {
	return func() tea.Msg {
		sshClient, err := GetSSHClient(server)
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
		totalBytes, err := countLocalFiles(localPath)
		if err != nil {
			b.progressChan <- sftpProgressMsg{Done: true, Err: err}
			return
		}

		var bytesCopied int64
		var lastUpdate time.Time
		err = uploadItemWithProgress(b.sftpClient, localPath, remotePath, &bytesCopied, &lastUpdate, totalBytes, b.progressChan, filename)
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
		totalBytes, err := countRemoteFiles(b.sftpClient, remotePath)
		if err != nil {
			b.progressChan <- sftpProgressMsg{Done: true, Err: err}
			return
		}

		var bytesCopied int64
		var lastUpdate time.Time
		err = downloadItemWithProgress(b.sftpClient, remotePath, localPath, &bytesCopied, &lastUpdate, totalBytes, b.progressChan, filename)
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
func NewSFTPBrowser(server Server) *SFTPBrowser {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return &SFTPBrowser{
		Server:      server,
		activePanel: LocalPanel,
		spinner:     s,
		isBusy:      true,
		statusMsg:   "Connecting via SFTP...",
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
