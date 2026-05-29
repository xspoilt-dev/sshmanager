package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
)

type AppState int

const (
	StateServerList AppState = iota
	StateServerForm
	StateSFTPBrowser
)

type FormMode int

const (
	ModeAdd FormMode = iota
	ModeEdit
)

type MainModel struct {
	state       AppState
	servers     []Server
	selectedIdx int
	errorMsg    string
	successMsg  string
	cwd         string // current working directory

	// Form fields
	formMode        FormMode
	formInputs      []textinput.Model
	formActiveField int
	editingServer   int // Index of server being edited

	// Confirm delete field
	confirmDelete bool

	// SFTP Browser field
	sftpBrowser *SFTPBrowser

	// Terminal dimensions
	width  int
	height int
}

func (m *MainModel) initForm(mode FormMode, server *Server) {
	m.formMode = mode
	m.formInputs = make([]textinput.Model, 6)
	m.formActiveField = 0

	for i := range m.formInputs {
		t := textinput.New()
		t.CharLimit = 128
		m.formInputs[i] = t
	}

	m.formInputs[0].Placeholder = "my-vps"
	m.formInputs[1].Placeholder = "192.168.1.1"
	m.formInputs[2].Placeholder = "22"
	m.formInputs[3].Placeholder = "root"
	m.formInputs[4].Placeholder = "password"
	m.formInputs[4].EchoMode = textinput.EchoPassword
	m.formInputs[5].Placeholder = "/path/to/my/project"

	if mode == ModeEdit && server != nil {
		m.formInputs[0].SetValue(server.Alias)
		m.formInputs[1].SetValue(server.Host)
		m.formInputs[2].SetValue(strconv.Itoa(server.Port))
		m.formInputs[3].SetValue(server.User)
		m.formInputs[4].SetValue(server.Password)
		m.formInputs[5].SetValue(server.ProjectPath)
	} else {
		m.formInputs[2].SetValue("22")
		m.formInputs[3].SetValue("root")
		m.formInputs[5].SetValue(m.cwd) // Prepopulate with cwd
	}

	m.formInputs[0].Focus()
}

func (m MainModel) Init() tea.Cmd {
	return nil
}

type sshFinishedMsg struct {
	err error
}

func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		if m.state == StateSFTPBrowser && m.sftpBrowser != nil {
			m.sftpBrowser.width = msg.Width
			m.sftpBrowser.height = msg.Height
			m.handleTerminalResize()
		}
		return m, nil

	case sshFinishedMsg:
		if msg.err != nil {
			m.errorMsg = fmt.Sprintf("SSH connection closed with error: %v", msg.err)
		} else {
			m.successMsg = "SSH connection closed successfully."
		}
		return m, nil

	case sftpConnectedMsg:
		if m.state == StateSFTPBrowser && m.sftpBrowser != nil {
			m.sftpBrowser.sshClient = msg.sshClient
			m.sftpBrowser.sftpClient = msg.sftpClient
			m.sftpBrowser.localDir = msg.localDir
			m.sftpBrowser.remoteDir = msg.remoteDir
			m.sftpBrowser.localItems = msg.localItems
			m.sftpBrowser.remoteItems = msg.remoteItems
			m.sftpBrowser.terminalSession = msg.terminalSession
			m.sftpBrowser.terminalIn = msg.terminalIn
			m.sftpBrowser.terminalEmu = msg.terminalEmu
			m.sftpBrowser.terminalRedraw = msg.redrawChan
			m.sftpBrowser.isBusy = false
			m.sftpBrowser.statusMsg = "Connected. Tab to switch local/remote, Ctrl+T to toggle terminal focus."
			m.sftpBrowser.initialized = true

			if msg.redrawChan != nil {
				return m, listenToTerminalRedraw(msg.redrawChan)
			}
		}
		return m, nil

	case terminalRedrawMsg:
		if m.state == StateSFTPBrowser && m.sftpBrowser != nil && m.sftpBrowser.terminalRedraw != nil {
			return m, listenToTerminalRedraw(m.sftpBrowser.terminalRedraw)
		}
		return m, nil

	case sftpErrorMsg:
		if m.state == StateSFTPBrowser && m.sftpBrowser != nil {
			m.sftpBrowser.err = msg.err
			m.sftpBrowser.isBusy = false
			m.sftpBrowser.statusMsg = ""
			m.errorMsg = msg.err.Error()
		}
		return m, nil

	case sftpListMsg:
		if m.state == StateSFTPBrowser && m.sftpBrowser != nil {
			m.sftpBrowser.isBusy = false
			if strings.HasPrefix(m.sftpBrowser.statusMsg, "Reading") {
				m.sftpBrowser.statusMsg = ""
			}
			if msg.panel == LocalPanel {
				m.sftpBrowser.localItems = msg.items
				if m.sftpBrowser.localIdx >= len(msg.items) {
					m.sftpBrowser.localIdx = 0
				}
			} else {
				m.sftpBrowser.remoteItems = msg.items
				if m.sftpBrowser.remoteIdx >= len(msg.items) {
					m.sftpBrowser.remoteIdx = 0
				}
			}
		}
		return m, nil

	case sftpProgressMsg:
		if m.state == StateSFTPBrowser && m.sftpBrowser != nil {
			m.sftpBrowser.lastProgress = msg
			if msg.Done {
				m.sftpBrowser.isBusy = false
				if msg.Err != nil {
					m.sftpBrowser.statusMsg = fmt.Sprintf("Error: %v", msg.Err)
				} else {
					m.sftpBrowser.statusMsg = msg.Message
				}
				return m, m.refreshSFTP()
			} else {
				m.sftpBrowser.statusMsg = msg.Message
				return m, listenToProgress(m.sftpBrowser.progressChan)
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.state == StateSFTPBrowser && m.sftpBrowser != nil {
			m.sftpBrowser.spinner, cmd = m.sftpBrowser.spinner.Update(msg)
			return m, cmd
		}
	}

	// Route based on active state
	switch m.state {
	case StateServerList:
		return m.updateServerList(msg)
	case StateServerForm:
		return m.updateServerForm(msg)
	case StateSFTPBrowser:
		return m.updateSFTPBrowser(msg)
	}

	return m, nil
}

func (m *MainModel) refreshSFTP() tea.Cmd {
	return tea.Batch(
		func() tea.Msg {
			items, err := getLocalItems(m.sftpBrowser.localDir)
			if err != nil {
				return sftpErrorMsg{err: err}
			}
			return sftpListMsg{panel: LocalPanel, dir: m.sftpBrowser.localDir, items: items}
		},
		func() tea.Msg {
			items, err := getRemoteItems(m.sftpBrowser.sftpClient, m.sftpBrowser.remoteDir)
			if err != nil {
				return sftpErrorMsg{err: err}
			}
			return sftpListMsg{panel: RemotePanel, dir: m.sftpBrowser.remoteDir, items: items}
		},
	)
}

func (m MainModel) updateServerList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Clear notifications on keypress
		m.errorMsg = ""
		m.successMsg = ""

		if m.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				m.confirmDelete = false
				if len(m.servers) > 0 {
					alias := m.servers[m.selectedIdx].Alias
					m.servers = append(m.servers[:m.selectedIdx], m.servers[m.selectedIdx+1:]...)
					if err := SaveServers(m.servers); err != nil {
						m.errorMsg = "Failed to save configuration: " + err.Error()
					} else {
						m.successMsg = fmt.Sprintf("Removed server '%s'.", alias)
					}
					if m.selectedIdx >= len(m.servers) && m.selectedIdx > 0 {
						m.selectedIdx = len(m.servers) - 1
					}
				}
			default:
				m.confirmDelete = false
			}
			return m, nil
		}

		switch msg.String() {
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}

		case "down", "j":
			if m.selectedIdx < len(m.servers)-1 {
				m.selectedIdx++
			}

		case "enter", "c", "u":
			if len(m.servers) == 0 {
				return m, nil
			}
			server := m.servers[m.selectedIdx]
			m.sftpBrowser = NewSFTPBrowser(server)
			m.sftpBrowser.width = m.width
			m.sftpBrowser.height = m.height
			m.state = StateSFTPBrowser
			return m, tea.Batch(
				m.sftpBrowser.spinner.Tick,
				connectSFTP(server, m.width, m.height),
			)

		case "a":
			m.state = StateServerForm
			m.initForm(ModeAdd, nil)

		case "e":
			if len(m.servers) == 0 {
				return m, nil
			}
			m.state = StateServerForm
			m.editingServer = m.selectedIdx
			server := m.servers[m.selectedIdx]
			m.initForm(ModeEdit, &server)

		case "d", "backspace":
			if len(m.servers) > 0 {
				m.confirmDelete = true
			}

		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m MainModel) updateServerForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = StateServerList
			return m, nil

		case "tab", "down":
			m.formInputs[m.formActiveField].Blur()
			m.formActiveField = (m.formActiveField + 1) % len(m.formInputs)
			m.formInputs[m.formActiveField].Focus()
			return m, nil

		case "shift+tab", "up":
			m.formInputs[m.formActiveField].Blur()
			m.formActiveField = (m.formActiveField - 1 + len(m.formInputs)) % len(m.formInputs)
			m.formInputs[m.formActiveField].Focus()
			return m, nil

		case "enter":
			// Validate fields
			alias := strings.TrimSpace(m.formInputs[0].Value())
			host := strings.TrimSpace(m.formInputs[1].Value())
			portStr := strings.TrimSpace(m.formInputs[2].Value())
			user := strings.TrimSpace(m.formInputs[3].Value())
			password := m.formInputs[4].Value()
			projectPath := strings.TrimSpace(m.formInputs[5].Value())

			if alias == "" || host == "" || user == "" || password == "" {
				m.errorMsg = "Alias, Host, Username, and Password are required fields."
				return m, nil
			}

			port, err := strconv.Atoi(portStr)
			if err != nil || port <= 0 || port > 65535 {
				m.errorMsg = "Invalid port number. Must be between 1 and 65535."
				return m, nil
			}

			// Add or update
			newServer := Server{
				Alias:       alias,
				Host:        host,
				User:        user,
				Password:    password,
				Port:        port,
				ProjectPath: projectPath,
			}

			if m.formMode == ModeAdd {
				m.servers = append(m.servers, newServer)
				m.selectedIdx = len(m.servers) - 1
				m.successMsg = fmt.Sprintf("Added server '%s' successfully.", alias)
			} else {
				m.servers[m.editingServer] = newServer
				m.selectedIdx = m.editingServer
				m.successMsg = fmt.Sprintf("Updated server '%s' successfully.", alias)
			}

			if err := SaveServers(m.servers); err != nil {
				m.errorMsg = "Failed to save configuration: " + err.Error()
			}

			m.state = StateServerList
			return m, nil
		}
	}

	// Route keys to active textinput
	var cmd tea.Cmd
	m.formInputs[m.formActiveField], cmd = m.formInputs[m.formActiveField].Update(msg)
	return m, cmd
}

func (m MainModel) updateSFTPBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	b := m.sftpBrowser
	if b == nil {
		m.state = StateServerList
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		if b.isBusy || b.deleteConfirm {
			return m, nil
		}

		// Click to focus panels
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			availHeight := m.height - 8
			if availHeight < 10 {
				availHeight = 10
			}
			termHeight := availHeight * b.terminalHeightPct / 100
			if termHeight < 5 {
				termHeight = 5
			}
			if termHeight > availHeight-6 {
				termHeight = availHeight - 6
			}
			panelHeight := availHeight - termHeight
			if panelHeight < 6 {
				panelHeight = 6
			}

			// File panels start around row 5 and have height panelHeight
			terminalStartRow := 5 + panelHeight + 2
			if msg.Y >= terminalStartRow {
				b.activePanel = TerminalPanel
			} else if msg.X < m.width/2 {
				b.activePanel = LocalPanel
			} else {
				b.activePanel = RemotePanel
			}
			return m, nil
		}

		// Mouse scroll
		if msg.Button == tea.MouseButtonWheelUp {
			if b.activePanel == LocalPanel {
				if b.localIdx > 0 {
					b.localIdx--
				}
			} else if b.activePanel == RemotePanel {
				if b.remoteIdx > 0 {
					b.remoteIdx--
				}
			}
			return m, nil
		} else if msg.Button == tea.MouseButtonWheelDown {
			if b.activePanel == LocalPanel {
				if b.localIdx < len(b.localItems)-1 {
					b.localIdx++
				}
			} else if b.activePanel == RemotePanel {
				if b.remoteIdx < len(b.remoteItems)-1 {
					b.remoteIdx++
				}
			}
			return m, nil
		}

	case tea.KeyMsg:
		// Clear transfer status notifications on keypress
		if !b.isBusy && b.activePanel != TerminalPanel {
			if strings.HasPrefix(b.statusMsg, "Successfully") || strings.HasPrefix(b.statusMsg, "Error") {
				b.statusMsg = ""
			}
		}

		if b.deleteConfirm {
			switch msg.String() {
			case "y", "Y":
				b.deleteConfirm = false
				b.isBusy = true
				b.statusMsg = fmt.Sprintf("Deleting %s...", b.deleteItem.Name)
				isLocal := b.activePanel == LocalPanel
				path := b.deletePath
				name := b.deleteItem.Name
				return m, func() tea.Msg {
					var err error
					if isLocal {
						err = os.RemoveAll(path)
					} else {
						err = removeRemoteAll(b.sftpClient, b.sshClient, path)
					}
					if err != nil {
						return sftpProgressMsg{
							Done: true,
							Err:  fmt.Errorf("failed to delete %s: %w", name, err),
						}
					}
					return sftpProgressMsg{
						Done:    true,
						Message: fmt.Sprintf("Successfully deleted %s", name),
					}
				}
			case "n", "N", "esc", "q":
				b.deleteConfirm = false
				return m, nil
			}
			return m, nil
		}

		// Global resize key bindings (available even in terminal panel via ctrl+up/down)
		switch msg.String() {
		case "ctrl+up":
			if b.activePanel == TerminalPanel {
				if b.terminalHeightPct < 80 {
					b.terminalHeightPct += 5
					m.handleTerminalResize()
				}
			} else {
				// Make file panels taller -> make terminal shorter
				if b.terminalHeightPct > 15 {
					b.terminalHeightPct -= 5
					m.handleTerminalResize()
				}
			}
			return m, nil
		case "ctrl+down":
			if b.activePanel == TerminalPanel {
				if b.terminalHeightPct > 15 {
					b.terminalHeightPct -= 5
					m.handleTerminalResize()
				}
			} else {
				// Make file panels shorter -> make terminal taller
				if b.terminalHeightPct < 80 {
					b.terminalHeightPct += 5
					m.handleTerminalResize()
				}
			}
			return m, nil
		case "ctrl+right":
			if b.localWidthPct < 80 {
				b.localWidthPct += 5
			}
			return m, nil
		case "ctrl+left":
			if b.localWidthPct > 20 {
				b.localWidthPct -= 5
			}
			return m, nil
		}

		if b.activePanel == TerminalPanel {
			if msg.Type == tea.KeyCtrlT {
				b.activePanel = LocalPanel
				return m, nil
			}

			inputStr := keyMsgToTerminalInput(msg)
			if inputStr != "" && b.terminalIn != nil {
				b.terminalIn.Write([]byte(inputStr))
			}
			return m, nil
		}

		if b.isBusy {
			// Disable inputs while transfer or connection is running
			if msg.String() == "ctrl+c" {
				// Allow exit
				b.isBusy = false
				if b.sftpClient != nil {
					b.sftpClient.Close()
				}
				if b.sshClient != nil {
					b.sshClient.Close()
				}
				m.state = StateServerList
			}
			return m, nil
		}

		switch msg.String() {
		case "+", "=", "]":
			if b.activePanel == LocalPanel {
				if b.localWidthPct < 80 {
					b.localWidthPct += 5
				}
			} else if b.activePanel == RemotePanel {
				if b.localWidthPct > 20 {
					b.localWidthPct -= 5
				}
			}
			return m, nil

		case "-", "[":
			if b.activePanel == LocalPanel {
				if b.localWidthPct > 20 {
					b.localWidthPct -= 5
				}
			} else if b.activePanel == RemotePanel {
				if b.localWidthPct < 80 {
					b.localWidthPct += 5
				}
			}
			return m, nil

		case "q", "esc":
			if b.sftpClient != nil {
				b.sftpClient.Close()
			}
			if b.sshClient != nil {
				b.sshClient.Close()
			}
			if b.terminalSession != nil {
				b.terminalSession.Close()
			}
			m.state = StateServerList
			return m, nil

		case "ctrl+t":
			if b.terminalEmu != nil {
				b.activePanel = TerminalPanel
			}
			return m, nil

		case "tab", "left", "right":
			if b.activePanel == LocalPanel {
				b.activePanel = RemotePanel
			} else {
				b.activePanel = LocalPanel
			}

		case "up", "k":
			if b.activePanel == LocalPanel {
				if b.localIdx > 0 {
					b.localIdx--
				}
			} else {
				if b.remoteIdx > 0 {
					b.remoteIdx--
				}
			}

		case "down", "j":
			if b.activePanel == LocalPanel {
				if b.localIdx < len(b.localItems)-1 {
					b.localIdx++
				}
			} else {
				if b.remoteIdx < len(b.remoteItems)-1 {
					b.remoteIdx++
				}
			}

		case "backspace":
			if b.activePanel == LocalPanel {
				parent := filepath.Dir(b.localDir)
				if parent != b.localDir {
					b.localDir = parent
					items, err := getLocalItems(b.localDir)
					if err == nil {
						b.localItems = items
						b.localIdx = 0
					}
				}
			} else {
				parent := remoteDirUp(b.remoteDir)
				if parent != b.remoteDir {
					b.remoteDir = parent
					b.isBusy = true
					b.statusMsg = "Reading remote directory..."
					return m, func() tea.Msg {
						items, err := getRemoteItems(b.sftpClient, b.remoteDir)
						if err != nil {
							return sftpErrorMsg{err: err}
						}
						return sftpListMsg{panel: RemotePanel, dir: b.remoteDir, items: items}
					}
				}
			}

		case "enter":
			if b.activePanel == LocalPanel {
				if len(b.localItems) == 0 {
					return m, nil
				}
				item := b.localItems[b.localIdx]
				if item.IsDir {
					if item.Name == ".." {
						b.localDir = filepath.Dir(b.localDir)
					} else {
						b.localDir = filepath.Join(b.localDir, item.Name)
					}
					items, err := getLocalItems(b.localDir)
					if err == nil {
						b.localItems = items
						b.localIdx = 0
					}
				}
			} else {
				if len(b.remoteItems) == 0 {
					return m, nil
				}
				item := b.remoteItems[b.remoteIdx]
				if item.IsDir {
					if item.Name == ".." {
						b.remoteDir = remoteDirUp(b.remoteDir)
					} else {
						b.remoteDir = remoteJoin(b.remoteDir, item.Name)
					}
					b.isBusy = true
					b.statusMsg = "Reading remote directory..."
					return m, func() tea.Msg {
						items, err := getRemoteItems(b.sftpClient, b.remoteDir)
						if err != nil {
							return sftpErrorMsg{err: err}
						}
						return sftpListMsg{panel: RemotePanel, dir: b.remoteDir, items: items}
					}
				}
			}

		case "u": // Upload file from local to remote
			if len(b.localItems) == 0 {
				return m, nil
			}
			item := b.localItems[b.localIdx]
			if item.Name == ".." {
				return m, nil
			}

			localPath := filepath.Join(b.localDir, item.Name)
			remotePath := remoteJoin(b.remoteDir, item.Name)

			b.isBusy = true
			b.statusMsg = fmt.Sprintf("Uploading %s...", item.Name)
			return m, b.startUpload(localPath, remotePath, item.Name)

		case "d": // Download file from remote to local
			if len(b.remoteItems) == 0 {
				return m, nil
			}
			item := b.remoteItems[b.remoteIdx]
			if item.Name == ".." {
				return m, nil
			}

			remotePath := remoteJoin(b.remoteDir, item.Name)
			localPath := filepath.Join(b.localDir, item.Name)

			b.isBusy = true
			b.statusMsg = fmt.Sprintf("Downloading %s...", item.Name)
			return m, b.startDownload(remotePath, localPath, item.Name)

		case "x", "delete":
			if b.activePanel == LocalPanel {
				if len(b.localItems) == 0 {
					return m, nil
				}
				item := b.localItems[b.localIdx]
				if item.Name == ".." {
					return m, nil
				}
				b.deleteConfirm = true
				b.deleteItem = item
				b.deletePath = filepath.Join(b.localDir, item.Name)
			} else {
				if len(b.remoteItems) == 0 {
					return m, nil
				}
				item := b.remoteItems[b.remoteIdx]
				if item.Name == ".." {
					return m, nil
				}
				b.deleteConfirm = true
				b.deleteItem = item
				b.deletePath = remoteJoin(b.remoteDir, item.Name)
			}
			return m, nil
		}
	}

	return m, nil
}

func (m MainModel) View() string {
	switch m.state {
	case StateServerList:
		return m.viewServerList()
	case StateServerForm:
		return m.viewServerForm()
	case StateSFTPBrowser:
		b := m.sftpBrowser
		if b != nil {
			if b.deleteConfirm {
				return m.renderDeleteConfirmModal(b)
			}
			if b.isBusy {
				return m.renderProgressModal(b)
			}
		}
		return m.viewSFTPBrowser()
	}
	return ""
}

func (m MainModel) renderDeleteConfirmModal(b *SFTPBrowser) string {
	content := fmt.Sprintf(
		"%s\n\n%s\n%s\n\n%s",
		styleModalTitle.Render("■ CONFIRM DELETE"),
		styleModalProgressMsg.Render("Are you sure you want to permanently delete:"),
		lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(b.deleteItem.Name),
		lipgloss.NewStyle().Foreground(colorMuted).Render("[y] Yes, Delete      [n/esc] Cancel"),
	)
	modalBox := styleModal.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalBox)
}

func (m MainModel) renderProgressModal(b *SFTPBrowser) string {
	title := "■ SYSTEM BUSY"
	msg := b.statusMsg
	var progressView string

	if strings.HasPrefix(b.statusMsg, "Connecting") {
		title = "■ CONNECTING TO VPS"
		msg = fmt.Sprintf("Establishing connection to %s...\nPlease wait.", b.Server.Alias)
		progressView = fmt.Sprintf("\n%s\n", b.spinner.View())
	} else if strings.HasPrefix(b.statusMsg, "Reading") {
		title = "■ READING DIRECTORY"
		msg = fmt.Sprintf("Fetching remote file list...\nPath: %s", b.remoteDir)
		progressView = fmt.Sprintf("\n%s\n", b.spinner.View())
	} else if strings.HasPrefix(b.statusMsg, "Deleting") {
		title = "■ DELETING ITEM"
		progressView = fmt.Sprintf("\n%s\n", b.spinner.View())
	} else if strings.HasPrefix(b.statusMsg, "Uploading") {
		title = "■ UPLOADING FILE(S)"
		bar := drawProgressBar(b.lastProgress.Percent, 44)
		progressView = fmt.Sprintf("\n%s  %.1f%%\n", lipgloss.NewStyle().Foreground(colorPurple).Render(bar), b.lastProgress.Percent*100)
	} else if strings.HasPrefix(b.statusMsg, "Downloading") {
		title = "■ DOWNLOADING FILE(S)"
		bar := drawProgressBar(b.lastProgress.Percent, 44)
		progressView = fmt.Sprintf("\n%s  %.1f%%\n", lipgloss.NewStyle().Foreground(colorPurple).Render(bar), b.lastProgress.Percent*100)
	} else {
		// Generic busy fallback
		progressView = fmt.Sprintf("\n%s\n", b.spinner.View())
	}

	content := fmt.Sprintf(
		"%s\n\n%s\n%s\n\n%s",
		styleModalTitle.Render(title),
		styleModalProgressMsg.Render(msg),
		progressView,
		lipgloss.NewStyle().Foreground(colorMuted).Render("ctrl+c to cancel / interrupt"),
	)
	modalBox := styleModal.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalBox)
}

func (m MainModel) viewServerList() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("■ SSH MANAGER"))
	b.WriteString(styleSubtitle.Render("Select a server to connect or manage files"))

	// Display notifications
	if m.errorMsg != "" {
		b.WriteString("\n")
		b.WriteString(styleErrorMsg.Render("✖ " + m.errorMsg))
		b.WriteString("\n")
	} else if m.successMsg != "" {
		b.WriteString("\n")
		b.WriteString(styleStatusMsg.Render("✔ " + m.successMsg))
		b.WriteString("\n")
	} else {
		b.WriteString("\n\n")
	}

	if len(m.servers) == 0 {
		b.WriteString(styleHelp.Render("No servers saved yet.\nPress 'a' to add a server, or 'q' to quit."))
		return b.String()
	}

	for i, s := range m.servers {
		var cardContent strings.Builder

		// Determine if server is matching the current folder
		matchTag := ""
		if s.ProjectPath != "" && s.ProjectPath == m.cwd {
			matchTag = styleStatusMsg.Render(" [CURRENT PROJECT]")
		}

		cardContent.WriteString(fmt.Sprintf("%s%s\n", styleServerAlias.Render(s.Alias), matchTag))
		cardContent.WriteString(styleServerDetails.Render(fmt.Sprintf("SSH: %s@%s:%d\n", s.User, s.Host, s.Port)))
		if s.ProjectPath != "" {
			cardContent.WriteString(styleServerDetails.Render("Path: "))
			cardContent.WriteString(styleProjectPath.Render(s.ProjectPath))
		} else {
			cardContent.WriteString(styleServerDetails.Render("Path: (none)"))
		}

		if i == m.selectedIdx {
			b.WriteString(styleServerCardSelected.Render(cardContent.String()))
		} else {
			b.WriteString(styleServerCard.Render(cardContent.String()))
		}
	}

	if m.confirmDelete {
		b.WriteString(styleErrorMsg.Render("\nConfirm deletion of selected server? [y/N]: "))
		b.WriteString("\n")
	} else {
		b.WriteString(styleHelp.Render("enter/c: connect • u: file transfer (sftp) • a: add • e: edit • d/backspace: delete • q: quit"))
	}

	return b.String()
}

func (m MainModel) viewServerForm() string {
	var b strings.Builder

	titleText := "ADD NEW SERVER"
	if m.formMode == ModeEdit {
		titleText = "EDIT SERVER CONFIGURATION"
	}

	var formContent strings.Builder
	formContent.WriteString(styleFormTitle.Render(titleText))
	formContent.WriteString("\n\n")

	labels := []string{"Alias:", "Host / IP:", "Port:", "Username:", "Password:", "Project Path:"}
	for i, input := range m.formInputs {
		label := styleFormLabel.Render(labels[i])
		var inputStr string
		if i == m.formActiveField {
			inputStr = styleFormInputActive.Render(input.View())
		} else {
			inputStr = styleFormInputMuted.Render(input.View())
		}
		formContent.WriteString(fmt.Sprintf("%s %s\n", label, inputStr))
	}

	if m.errorMsg != "" {
		formContent.WriteString("\n")
		formContent.WriteString(styleErrorMsg.Render("✖ " + m.errorMsg))
		formContent.WriteString("\n")
	}

	formContent.WriteString(styleHelp.Render("\ntab/down: next field • up: prev field • enter: save • esc: cancel"))

	b.WriteString(styleFormBorder.Render(formContent.String()))
	return b.String()
}

func (m MainModel) viewSFTPBrowser() string {
	b := m.sftpBrowser
	if b == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(styleTitle.Render(fmt.Sprintf("■ FILE MANAGER & TERMINAL — %s", b.Server.Alias)))

	statusText := b.statusMsg
	if b.isBusy {
		statusText = fmt.Sprintf("%s %s", b.spinner.View(), b.statusMsg)
	}

	if b.err != nil {
		builder.WriteString("\n")
		builder.WriteString(styleErrorMsg.Render("✖ " + b.err.Error()))
		builder.WriteString("\n")
	} else {
		builder.WriteString("\n")
		builder.WriteString(styleStatusMsg.Render(statusText))
		builder.WriteString("\n")
		builder.WriteString("\n")
	}

	// Calculate heights
	availHeight := m.height - 8
	if availHeight < 10 {
		availHeight = 10
	}
	termHeight := availHeight * b.terminalHeightPct / 100
	if termHeight < 5 {
		termHeight = 5
	}
	if termHeight > availHeight-6 {
		termHeight = availHeight - 6
	}
	panelHeight := availHeight - termHeight
	if panelHeight < 6 {
		panelHeight = 6
	}

	// Calculate panel widths based on localWidthPct
	availWidth := m.width - 10
	if availWidth < 40 {
		availWidth = 40
	}
	leftPanelWidth := availWidth * b.localWidthPct / 100
	rightPanelWidth := availWidth - leftPanelWidth

	if leftPanelWidth < 20 {
		leftPanelWidth = 20
	}
	if rightPanelWidth < 20 {
		rightPanelWidth = 20
	}

	// Render Local Panel
	var localLines []string
	localLines = append(localLines, lipgloss.NewStyle().Bold(true).Foreground(colorPurple).Render("■ LOCAL DIRECTORY:"))
	localLines = append(localLines, styleServerDetails.Render(b.localDir))
	localLines = append(localLines, "")

	if b.initialized {
		localLines = append(localLines, renderFileList(b.localItems, b.localIdx, panelHeight)...)
	} else {
		localLines = append(localLines, "  Connecting...")
	}

	localView := lipgloss.JoinVertical(lipgloss.Left, localLines...)
	leftStyle := styleFilePanel.Copy().Width(leftPanelWidth).Height(panelHeight)
	if b.activePanel == LocalPanel {
		leftStyle = styleFilePanelActive.Copy().Width(leftPanelWidth).Height(panelHeight)
	}
	leftPanel := leftStyle.Render(localView)

	// Render Remote Panel
	var remoteLines []string
	remoteLines = append(remoteLines, lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("■ REMOTE VPS DIRECTORY:"))
	remoteLines = append(remoteLines, styleServerDetails.Render(b.remoteDir))
	remoteLines = append(remoteLines, "")

	if b.initialized {
		remoteLines = append(remoteLines, renderFileList(b.remoteItems, b.remoteIdx, panelHeight)...)
	} else {
		remoteLines = append(remoteLines, "  Connecting...")
	}

	remoteView := lipgloss.JoinVertical(lipgloss.Left, remoteLines...)
	rightStyle := styleFilePanel.Copy().Width(rightPanelWidth).Height(panelHeight)
	if b.activePanel == RemotePanel {
		rightStyle = styleFilePanelActive.Copy().Width(rightPanelWidth).Height(panelHeight)
	}
	rightPanel := rightStyle.Render(remoteView)

	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	builder.WriteString(panes)
	builder.WriteString("\n\n")

	// Render Terminal Panel
	var termContent string
	if b.terminalEmu != nil {
		if b.activePanel == TerminalPanel {
			pos := b.terminalEmu.CursorPosition()
			w, h := b.terminalEmu.Width(), b.terminalEmu.Height()
			if pos.X >= 0 && pos.X < w && pos.Y >= 0 && pos.Y < h {
				oldCell := b.terminalEmu.CellAt(pos.X, pos.Y)
				var oldCellClone *uv.Cell
				if oldCell != nil {
					oldCellClone = oldCell.Clone()
				}

				// Create block cursor style
				cursorCell := &uv.Cell{
					Content: " ",
					Width:   1,
				}
				if oldCell != nil {
					if oldCell.Content != "" {
						cursorCell.Content = oldCell.Content
					}
					cursorCell.Style = oldCell.Style
				}
				cursorCell.Style.Bg = color.RGBA{R: 203, G: 166, B: 247, A: 255}
				cursorCell.Style.Fg = color.RGBA{R: 0, G: 0, B: 0, A: 255}
				cursorCell.Style.Attrs &^= uv.AttrReverse

				b.terminalEmu.SetCell(pos.X, pos.Y, cursorCell)
				termContent = b.terminalEmu.Render()
				b.terminalEmu.SetCell(pos.X, pos.Y, oldCellClone)
			} else {
				termContent = b.terminalEmu.Render()
			}
		} else {
			termContent = b.terminalEmu.Render()
		}
	} else {
		termContent = "Initializing remote terminal session..."
	}

	termWidth := m.width - 8
	if termWidth < 20 {
		termWidth = 20
	}

	termStyle := styleTerminalPanel.Copy().Width(termWidth).Height(termHeight)
	if b.activePanel == TerminalPanel {
		termStyle = styleTerminalPanelActive.Copy().Width(termWidth).Height(termHeight)
	}

	termLines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorPurple).Render("■ REMOTE SSH TERMINAL (Ctrl+T to toggle focus):"),
		termContent,
	}
	termView := termStyle.Render(lipgloss.JoinVertical(lipgloss.Left, termLines...))
	builder.WriteString(termView)
	builder.WriteString("\n\n")

	var helpText string
	if b.activePanel == TerminalPanel {
		helpText = "ctrl+t: focus files • ctrl+up/down: resize height • ctrl+c: interrupt • ctrl+d: close • q/esc: disconnect & exit"
	} else {
		helpText = "tab/←/→: switch panel • ctrl+t: focus term • +/-/[]: resize • up/down: select • enter: open • backspace: up • u: upload • d: download • x/del: delete • q/esc: exit"
	}
	builder.WriteString(styleHelp.Render(helpText))

	return builder.String()
}

func renderFileList(items []FileItem, selectedIdx int, height int) []string {
	if len(items) == 0 {
		return []string{"  (empty directory)"}
	}

	maxLines := height - 4
	if maxLines <= 0 {
		maxLines = 10
	}

	start := 0
	if selectedIdx >= maxLines {
		start = selectedIdx - maxLines + 1
	}

	end := start + maxLines
	if end > len(items) {
		end = len(items)
	}

	var lines []string
	for idx := start; idx < end; idx++ {
		item := items[idx]
		name := item.Name
		if item.IsDir && name != ".." {
			name += "/"
		}

		var icon string
		if item.IsDir {
			icon = styleIconDir.Render("■")
		} else {
			icon = styleIconFile.Render("•")
		}

		var rendered string
		if idx == selectedIdx {
			prefix := lipgloss.NewStyle().Foreground(colorPurple).Bold(true).Render("→ ")
			if item.IsDir {
				rendered = prefix + icon + " " + styleFileSelected.Render(name)
			} else {
				rendered = prefix + icon + " " + styleFileSelected.Render(fmt.Sprintf("%s (%s)", name, formatSize(item.Size)))
			}
		} else {
			prefix := "  "
			if item.IsDir {
				rendered = prefix + icon + " " + styleFileDir.Render(name)
			} else {
				rendered = prefix + icon + " " + styleFileRegular.Render(fmt.Sprintf("%s (%s)", name, formatSize(item.Size)))
			}
		}
		lines = append(lines, rendered)
	}
	return lines
}

func main() {
	servers, err := LoadServers()
	if err != nil {
		fmt.Printf("Error loading servers: %v\n", err)
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	initialIdx := 0
	// Try to match current working directory with a server's ProjectPath
	for i, s := range servers {
		if s.ProjectPath != "" && s.ProjectPath == cwd {
			initialIdx = i
			break
		}
	}

	m := MainModel{
		state:       StateServerList,
		servers:     servers,
		selectedIdx: initialIdx,
		cwd:         cwd,
		width:       80,
		height:      24,
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, terminal application failed: %v\n", err)
		os.Exit(1)
	}
}

func drawProgressBar(percent float64, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}

	barWidth := width - 8
	if barWidth <= 0 {
		return ""
	}

	filledLength := int(percent * float64(barWidth))
	emptyLength := barWidth - filledLength

	filled := strings.Repeat("█", filledLength)
	empty := strings.Repeat("░", emptyLength)

	return fmt.Sprintf("[%s%s] %3.0f%%", filled, empty, percent*100)
}

func keyMsgToTerminalInput(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		return string(msg.Runes)
	case tea.KeySpace:
		return " "
	case tea.KeyEnter:
		return "\r"
	case tea.KeyBackspace:
		return "\x7f"
	case tea.KeyTab:
		return "\t"
	case tea.KeyEscape:
		return "\x1b"
	case tea.KeyUp:
		return "\x1b[A"
	case tea.KeyDown:
		return "\x1b[B"
	case tea.KeyRight:
		return "\x1b[C"
	case tea.KeyLeft:
		return "\x1b[D"
	case tea.KeyCtrlA:
		return "\x01"
	case tea.KeyCtrlB:
		return "\x02"
	case tea.KeyCtrlC:
		return "\x03"
	case tea.KeyCtrlD:
		return "\x04"
	case tea.KeyCtrlE:
		return "\x05"
	case tea.KeyCtrlF:
		return "\x06"
	case tea.KeyCtrlG:
		return "\x07"
	case tea.KeyCtrlH:
		return "\x08"
	case tea.KeyCtrlJ:
		return "\x0a"
	case tea.KeyCtrlK:
		return "\x0b"
	case tea.KeyCtrlL:
		return "\x0c"
	case tea.KeyCtrlN:
		return "\x0e"
	case tea.KeyCtrlO:
		return "\x0f"
	case tea.KeyCtrlP:
		return "\x10"
	case tea.KeyCtrlQ:
		return "\x11"
	case tea.KeyCtrlR:
		return "\x12"
	case tea.KeyCtrlS:
		return "\x13"
	case tea.KeyCtrlT:
		return ""
	case tea.KeyCtrlU:
		return "\x15"
	case tea.KeyCtrlV:
		return "\x16"
	case tea.KeyCtrlW:
		return "\x17"
	case tea.KeyCtrlX:
		return "\x18"
	case tea.KeyCtrlY:
		return "\x19"
	case tea.KeyCtrlZ:
		return "\x1a"
	}
	return ""
}

func (m *MainModel) handleTerminalResize() {
	b := m.sftpBrowser
	if b == nil {
		return
	}
	availHeight := m.height - 8
	if availHeight < 10 {
		availHeight = 10
	}
	termHeight := availHeight * b.terminalHeightPct / 100
	if termHeight < 5 {
		termHeight = 5
	}
	if termHeight > availHeight-6 {
		termHeight = availHeight - 6
	}
	termWidth := m.width - 8
	if termWidth < 20 {
		termWidth = 20
	}
	if b.terminalEmu != nil {
		b.terminalEmu.Resize(termWidth, termHeight)
	}
	if b.terminalSession != nil {
		_ = b.terminalSession.WindowChange(termHeight, termWidth)
	}
}
