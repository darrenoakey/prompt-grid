package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"gioui.org/app"

	"claude-term/src/config"
	"claude-term/src/discord"
	"claude-term/src/gui"
	"claude-term/src/ipc"
	"claude-term/src/session"
)

const daemonEnvVar = "CLAUDE_TERM_DAEMON"

func main() {
	// Start as main daemon directly (for auto/launchd)
	if findArg("--daemon") >= 0 {
		runDaemon()
		return
	}

	// Internal: spawned as a session daemon (PTY holder process)
	if idx := findArg("--session-daemon"); idx >= 0 && idx+1 < len(os.Args) {
		name := os.Args[idx+1]
		cols := uint16(120)
		rows := uint16(24)
		var sshHost string

		if i := findArg("--cols"); i >= 0 && i+1 < len(os.Args) {
			if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
				cols = uint16(v)
			}
		}
		if i := findArg("--rows"); i >= 0 && i+1 < len(os.Args) {
			if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
				rows = uint16(v)
			}
		}
		if i := findArg("--ssh"); i >= 0 && i+1 < len(os.Args) {
			sshHost = os.Args[i+1]
		}

		daemon := session.NewDaemon(name, cols, rows, sshHost)
		daemon.Run()
		return
	}

	// Internal: main daemon process (GUI/Discord/IPC)
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

	// Initialize Discord bot
	var bot *discord.Bot
	cfg, cfgErr := config.LoadDefault()
	if cfgErr == nil {
		bot, err = discord.NewBot(&cfg.Discord, application)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Discord bot creation failed: %v\n", err)
		} else {
			if err := bot.Connect(); err != nil {
				fmt.Fprintf(os.Stderr, "Discord connection failed: %v\n", err)
				bot = nil
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "Config not loaded: %v\n", cfgErr)
	}

	// Ensure bot cleanup on exit
	if bot != nil {
		defer bot.Disconnect()
	}

	// Set bot reference in app for status display
	application.SetDiscordBot(bot)

	// Create and run control window in background
	// The daemon stays running even if control window is closed
	go func() {
		controlWin := application.CreateControlWindow()
		if err := controlWin.Run(); err != nil {
			// Control window closed - that's fine, daemon keeps running
		}
		// Control window closed - daemon continues for Discord bot
		// User can reopen control via IPC or Discord commands
	}()

	// Run Gio event loop - this keeps the daemon alive
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

func findArg(name string) int {
	for i, arg := range os.Args {
		if arg == name {
			return i
		}
	}
	return -1
}
