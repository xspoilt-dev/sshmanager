# Contributing to SSH & SFTP Manager

Thank you for your interest in contributing to the project! This guide will help you understand the architecture, package structure, local setup, and style guidelines.

---

## 📂 Project Architecture

The project has been refactored into modular Go packages to keep the codebase maintainable, testable, and clean.

### Package Structure
*   **`main.go`**: The entrypoint of the application. It bootstraps and runs the application by calling `tui.Run()`.
*   **`config/`**: Manages server profiles storage (JSON), AES-256-GCM encryption/decryption of credentials, and atomic file I/O operations.
*   **`ssh/`**: Contains core SSH logic including PTY setup, SSH agent connections, private key parsing, keep-alive heartbeats, and TOFU host key verification.
*   **`tui/`**: Implements the user interface using the Charm Bracelet Bubble Tea framework:
    *   `sftp_browser.go`: Handles concurrent file transfers (upload/download queue and progress).
    *   `styles.go`: Defines the Lipgloss stylesheets, colors, and layout configurations.
    *   `tui.go`: Main UI state machine, layout, keybindings, and form rendering.

---

## 🛠️ Local Development Setup

### Requirements
*   Go 1.22 or higher.

### Steps
1.  **Clone the Repository**:
    ```bash
    git clone https://github.com/your-username/sshmanager.git
    cd sshmanager
    ```
2.  **Install Dependencies**:
    ```bash
    go mod download
    ```
3.  **Run Locally**:
    ```bash
    go run main.go
    ```
4.  **Run Unit Tests**:
    ```bash
    go test -v ./...
    ```

---

## 🎨 UI & Styling Guidelines

We use `lipgloss` for styling our terminal elements. Please adhere to the following design guidelines:
1.  **Color Palette**: Use consistent Catppuccin-inspired color tokens (e.g. Purple for local, Blue for remote, Red for errors).
2.  **No Hardcoded Styles**: Define layout borders, paddings, and colors as Lipgloss styles in `tui/styles.go` instead of embedding them directly in functional components.
3.  **Responsive Layout**: Maintain proper calculations for panel widths and terminal heights using the window size messages (`tea.WindowSizeMsg`).
