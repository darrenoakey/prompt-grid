package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"gioui.org/app"

	"claude-term/src/gui"
	"claude-term/src/ipc"
)

const daemonEnvVar = "CLAUDE_TERM_DAEMON"

func main() {
	// Check if we're the daemon process
	if os.Getenv(daemonEnvVar) == "1" {
		runDaemon()
		return
	}

	// Regular invocation - parse args and either connect or spawn daemon
	args := os.Args[1:]

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	// Parse command line
	var sessionName string
	var sshHost string

	if args[0] == "ssh" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: ssh requires a host argument")
			printUsage()
			os.Exit(1)
		}
		sshHost = args[1]
		if len(args) >= 3 {
			sessionName = args[2]
		} else {
			sessionName = sshHost
		}
	} else {
		sessionName = args[0]
	}

	// Try to connect to existing instance
	req := ipc.Request{
		SessionName: sessionName,
		SSHHost:     sshHost,
	}

	connected, err := ipc.TryConnect(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if connected {
		// Successfully sent to existing instance - exit immediately
		os.Exit(0)
	}

	// No existing instance - spawn daemon and send request
	spawnDaemon()

	// Wait briefly for daemon to start, then connect
	for i := 0; i < 50; i++ { // Try for up to 5 seconds
		connected, err = ipc.TryConnect(req)
		if connected {
			os.Exit(0)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintln(os.Stderr, "Error: failed to connect to daemon")
	os.Exit(1)
}

func spawnDaemon() {
	// Re-exec ourselves as a detached daemon
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command(executable)
	cmd.Env = append(os.Environ(), daemonEnvVar+"=1")

	// Detach from parent process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Redirect stdout/stderr to /dev/null
	devNull, _ := os.Open(os.DevNull)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Stdin = devNull

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
		os.Exit(1)
	}

	// Don't wait - let the daemon run independently
}

func runDaemon() {
	// Create application
	application := gui.NewApp()

	// Create IPC server
	server, err := ipc.NewServer(func(req ipc.Request) error {
		return application.AddSession(req.SessionName, req.SSHHost)
	})
	if err != nil {
		os.Exit(1)
	}
	defer server.Close()

	// Run IPC server in background
	go server.Run()

	// Create and run control window
	go func() {
		controlWin := application.CreateControlWindow()
		if err := controlWin.Run(); err != nil {
			// Control window closed
		}
		// When control window closes, exit app
		os.Exit(0)
	}()

	// Run Gio event loop
	app.Main()
}

func printUsage() {
	fmt.Println(`claude-term - Terminal emulator with multi-view support

Usage:
  claude-term <session-name>              Create a new local session
  claude-term ssh <host> [session-name]   Create an SSH session

Examples:
  claude-term "My Project"
  claude-term ssh user@host "Remote Work"
  claude-term ssh myserver`)
}
