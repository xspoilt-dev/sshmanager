package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// SSHCommand implements tea.ExecCommand to support running a live terminal
// inside the Bubble Tea execution lifecycle.
type SSHCommand struct {
	Host     string
	Port     int
	User     string
	Password string
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
}

func NewSSHCommand(server Server) *SSHCommand {
	return &SSHCommand{
		Host:     server.Host,
		Port:     server.Port,
		User:     server.User,
		Password: server.Password,
	}
}

func (s *SSHCommand) SetStdin(r io.Reader)  { s.stdin = r }
func (s *SSHCommand) SetStdout(w io.Writer) { s.stdout = w }
func (s *SSHCommand) SetStderr(w io.Writer) { s.stderr = w }

func (s *SSHCommand) Run() error {
	config := &ssh.ClientConfig{
		User: s.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(s.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("failed to dial: %w", err)
	}
	defer client.Close()

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
func GetSSHClient(server Server) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: server.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(server.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(server.Host, fmt.Sprintf("%d", server.Port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, err
	}
	return client, nil
}
