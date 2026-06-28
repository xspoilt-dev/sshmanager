package ssh

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"

	"sshmanager/config"
)

// SSHCommand implements tea.ExecCommand to support running a live terminal
// inside the Bubble Tea execution lifecycle.
type SSHCommand struct {
	Host           string
	Port           int
	User           string
	Password       string
	PrivateKeyPath string
	UseSSHAgent    bool
	stdin          io.Reader
	stdout         io.Writer
	stderr         io.Writer
}

func NewSSHCommand(server config.Server) *SSHCommand {
	return &SSHCommand{
		Host:           server.Host,
		Port:           server.Port,
		User:           server.User,
		Password:       server.Password,
		PrivateKeyPath: server.PrivateKeyPath,
		UseSSHAgent:    server.UseSSHAgent,
	}
}

func (s *SSHCommand) SetStdin(r io.Reader)  { s.stdin = r }
func (s *SSHCommand) SetStdout(w io.Writer) { s.stdout = w }
func (s *SSHCommand) SetStderr(w io.Writer) { s.stderr = w }

func (s *SSHCommand) Run() error {
	auths := []ssh.AuthMethod{}

	// 1. Try SSH Agent
	if s.UseSSHAgent {
		if agentClient, err := connectSSHAgent(); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// 2. Try Private Key
	if s.PrivateKeyPath != "" {
		signer, err := loadPrivateKey(s.PrivateKeyPath, s.Password)
		if err == nil {
			auths = append(auths, ssh.PublicKeys(signer))
		} else {
			return fmt.Errorf("failed to load private key: %w", err)
		}
	}

	// 3. Fallback to Password
	if s.Password != "" && (s.PrivateKeyPath == "" || len(auths) == 0) {
		auths = append(auths, ssh.Password(s.Password))
	}

	configData := &ssh.ClientConfig{
		User:            s.User,
		Auth:            auths,
		HostKeyCallback: mustGetHostKeyCallback(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	client, err := ssh.Dial("tcp", addr, configData)
	if err != nil {
		return fmt.Errorf("failed to dial: %w", err)
	}
	defer client.Close()

	// Start Keep-Alive to maintain connection stability
	startSSHKeepAlive(client)

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Get file descriptor for stdin to set terminal to raw mode
	var fd int
	if f, ok := s.stdin.(*os.File); ok {
		fd = int(f.Fd())
	} else {
		fd = int(os.Stdin.Fd())
	}

	// Make raw terminal state so keys are sent directly to SSH
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to make raw: %w", err)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
	}()

	width, height, err := term.GetSize(fd)
	if err != nil {
		width, height = 80, 40
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // Enable echo
		ssh.TTY_OP_ISPEED: 14400, // input speed
		ssh.TTY_OP_OSPEED: 14400, // output speed
	}

	termEnv := os.Getenv("TERM")
	if termEnv == "" {
		termEnv = "xterm-256color"
	}

	if err := session.RequestPty(termEnv, height, width, modes); err != nil {
		return fmt.Errorf("failed to request pty: %w", err)
	}

	// Route session pipes
	session.Stdin = s.stdin
	session.Stdout = s.stdout
	session.Stderr = s.stderr

	// Handle window resizing (SIGWINCH)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	defer signal.Stop(sigChan)

	go func() {
		for range sigChan {
			w, h, err := term.GetSize(fd)
			if err == nil {
				_ = session.WindowChange(h, w)
			}
		}
	}()

	// Start remote shell
	if err := session.Shell(); err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Wait for connection to terminate
	if err := session.Wait(); err != nil {
		// Ignore EOF or standard exit exit codes
		if err.Error() != "EOF" {
			return err
		}
	}

	return nil
}

// GetSSHClient establishes an SSH client connection (useful for SFTP).
func GetSSHClient(server config.Server) (*ssh.Client, error) {
	auths := []ssh.AuthMethod{}

	// 1. Try SSH Agent
	if server.UseSSHAgent {
		if agentClient, err := connectSSHAgent(); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// 2. Try Private Key
	if server.PrivateKeyPath != "" {
		signer, err := loadPrivateKey(server.PrivateKeyPath, server.Password)
		if err == nil {
			auths = append(auths, ssh.PublicKeys(signer))
		} else {
			return nil, fmt.Errorf("failed to load private key: %w", err)
		}
	}

	// 3. Fallback to Password
	if server.Password != "" && (server.PrivateKeyPath == "" || len(auths) == 0) {
		auths = append(auths, ssh.Password(server.Password))
	}

	configData := &ssh.ClientConfig{
		User:            server.User,
		Auth:            auths,
		HostKeyCallback: mustGetHostKeyCallback(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(server.Host, fmt.Sprintf("%d", server.Port))
	client, err := ssh.Dial("tcp", addr, configData)
	if err != nil {
		return nil, err
	}

	// Start Keep-Alive to maintain connection stability
	startSSHKeepAlive(client)

	return client, nil
}

// connectSSHAgent connects to the system's ssh-agent daemon.
func connectSSHAgent() (agent.Agent, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, errors.New("SSH_AUTH_SOCK environment variable not set")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	return agent.NewClient(conn), nil
}

// loadPrivateKey loads a private key from disk with optional passphrase decryption.
func loadPrivateKey(path string, passphrase string) (ssh.Signer, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	keyBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(passphrase))
		if err == nil {
			return signer, nil
		}
		// Fallback to unencrypted key in case a passphrase was provided but key is not encrypted
	}
	return ssh.ParsePrivateKey(keyBytes)
}

// getHostKeyCallback returns a secure host key verifier that implements TOFU (Trust On First Use).
func getHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, err
	}
	knownHostsPath := filepath.Join(sshDir, "known_hosts")

	// Ensure the file exists
	f, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		cb, err := knownhosts.New(knownHostsPath)
		if err != nil {
			return err
		}

		err = cb(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) == 0 {
				// Host not found (TOFU behavior) -> Add to known_hosts
				// Ensure existing known_hosts file ends with a newline to avoid formatting corruption
				if data, err := os.ReadFile(knownHostsPath); err == nil && len(data) > 0 {
					if data[len(data)-1] != '\n' {
						if fk, err := os.OpenFile(knownHostsPath, os.O_WRONLY|os.O_APPEND, 0600); err == nil {
							_, _ = fk.WriteString("\n")
							fk.Close()
						}
					}
				}

				line := knownhosts.Line([]string{knownhosts.Normalize(remote.String()), knownhosts.Normalize(hostname)}, key)
				f, err := os.OpenFile(knownHostsPath, os.O_WRONLY|os.O_APPEND, 0600)
				if err != nil {
					return fmt.Errorf("failed to open known_hosts: %w", err)
				}
				defer f.Close()
				if _, err := f.WriteString(line); err != nil {
					return fmt.Errorf("failed to write known_hosts: %w", err)
				}
				return nil
			}
			// Host found but key mismatches -> Potential hijack or MITM
			return fmt.Errorf("SECURITY WARNING: Host key verification failed for %s. Key has changed!", hostname)
		}

		return err
	}, nil
}

// mustGetHostKeyCallback is a helper that falls back to insecure check ONLY if known_hosts cannot be loaded.
func mustGetHostKeyCallback() ssh.HostKeyCallback {
	cb, err := getHostKeyCallback()
	if err != nil {
		return ssh.InsecureIgnoreHostKey()
	}
	return cb
}

// startSSHKeepAlive sends keepalive requests to the server periodically to prevent idle timeouts.
func startSSHKeepAlive(client *ssh.Client) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return
			}
		}
	}()
}
