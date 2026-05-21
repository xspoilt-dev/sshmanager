# ⚡ SSH & SFTP Manager TUI ⚡

A modern, secure, and beautiful Terminal User Interface (TUI) for managing SSH servers and transferring files (SFTP). Built using Go and the Charm Bracelet TUI framework (`bubbletea`, `lipgloss`, `bubbles`).

## Key Features

1. **Secure Encrypted Storage**: Servers are stored in `~/.config/sshmanager/servers.json`. Passwords, hosts, and usernames are encrypted using AES-256-GCM. A unique secure key is generated and saved locally with restricted permissions (`0600`) so only the owner can read them.
2. **Unified Dashboard View**: Connecting to any server automatically opens a premium dashboard featuring a dual-pane file browser on top and an active remote SSH terminal on the bottom!
3. **Double-Pane SFTP File Browser**: A Midnight Commander/termscp style split pane interface (Local directory on the left, Remote directory on the right) to browse and select files.
4. **Interactive Embedded Terminal**: The bottom pane runs a live SSH shell (PTY session) using Go's `crypto/ssh` and a high-fidelity virtual terminal renderer (`github.com/charmbracelet/x/vt`), supporting dynamic resizing (SIGWINCH), standard bash command input, command outputs, color codes, and diagnostics.
5. **Universally Compatible ANSI Styling**: 
   - Uses zero emojis in file listings and layout headers to guarantee 100% font support on any standard terminal or SSH client.
   - Folders are represented with a solid yellow square (`■`) and files are represented with a clean blue bullet (`•`).
   - Replaced solid block background highlights in the files list with clean foreground text highlights and a bright pointer indicator (`→ `), avoiding jagged overlay rendering artifacts.
6. **Non-Blocking Async Transfers**: Uploading (`u`) and downloading (`d`) files/folders run asynchronously in the background using Go goroutines and channel notifications.
7. **Real-time Progress Indicator**: Includes a premium-styled progress bar displaying live percentage completion, bytes transferred, and total size for files and full directories.
8. **Project Path Integration**: Assign a local project directory to any server. When you run `sshmanager` from that project directory, the TUI automatically highlights that server! You just have to hit `Enter` to connect immediately.

## Installation & Setup

### Prerequisites
- Go 1.26+

### Build from Source
To compile the project and generate the executable binary:
```bash
go build -o sshmanager
```

To install the binary globally (to `~/go/bin`):
```bash
go install
```

## Keybindings

### Main Server Menu
- `↑ / ↓` or `k / j`: Navigate server list.
- `enter` or `c` or `u`: Connect to the selected server and open the unified dashboard.
- `a`: Add a new server.
- `e`: Edit the selected server configuration.
- `d` or `backspace`: Delete the selected server (will prompt for confirmation).
- `q` or `Ctrl+C`: Quit the application.

### Server Form (Add/Edit)
- `tab` or `↓`: Move to the next input field.
- `shift+tab` or `↑`: Move to the previous input field.
- `enter`: Save the configuration.
- `esc`: Cancel and go back to the server list.

### Unified Dashboard (File Browser & Terminal)
- **Toggling Focus**: Press `Ctrl+T` to switch focus between the **File Browser** (top) and the **Terminal** (bottom).
- **In File Browser Mode**:
  - `tab` or `← / →`: Switch between the **Local** (left) and **Remote** (right) panels.
  - `↑ / ↓` or `k / j`: Navigate files.
  - `enter`: Enter the selected directory (or go up if `..` is selected).
  - `backspace`: Go up to the parent directory.
  - `u`: Upload the selected file/folder from Local to Remote.
  - `d`: Download the selected file/folder from Remote to Local.
  - `q` or `esc`: Disconnect and return to the main server menu.
- **In Terminal Mode**:
  - Type directly to send keys to the active SSH shell (supports tab completion, backspace, enter, arrow keys).
  - `Ctrl+C`: Send interrupt (SIGINT) to the remote process.
  - `Ctrl+D`: Close session.
  - `Ctrl+T`: Toggle focus back to the file browser.
  - `q` or `esc`: Disconnect and return to the main server menu.
