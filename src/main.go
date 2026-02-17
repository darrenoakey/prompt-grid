package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"gioui.org/app"

	"prompt-grid/src/config"
	"prompt-grid/src/discord"
	"prompt-grid/src/gui"
	"prompt-grid/src/ipc"
	"prompt-grid/src/memwatch"
	"prompt-grid/src/tmux"
)

const daemonEnvVar = "CLAUDE_TERM_DAEMON"

func main() {
	// Start as main daemon directly (for auto/launchd)
	if findArg("--daemon") >= 0 {
		runDaemon()
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
	// Single-instance guard: acquire exclusive file lock (race-free via kernel)
	lockPath := filepath.Join(tmux.GetSocketDir(), "daemon.lock")
	os.MkdirAll(filepath.Dir(lockPath), 0755)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating lock file: %v\n", err)
		os.Exit(1)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Fprintln(os.Stderr, "Another prompt-grid daemon is already running, exiting")
		lockFile.Close()
		os.Exit(0)
	}
	// Keep lockFile open for process lifetime (lock released on exit)

	// Ensure tmux is available
	if err := tmux.EnsureInstalled(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Load config (before creating App so colors can be restored)
	cfgPath := config.DefaultConfigPath()
	cfg, cfgErr := config.LoadDefault()
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "Config not loaded: %v (using defaults)\n", cfgErr)
		cfg = &config.Config{}
	}

	// Create application with config
	application := gui.NewApp(cfg, cfgPath)

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
	}

	// Ensure bot cleanup on exit
	if bot != nil {
		defer bot.Disconnect()
	}

	// Set bot reference in app for status display
	// Only set if non-nil; a nil *discord.Bot passed as DiscordStatus interface
	// is not nil (Go interface semantics), causing a panic in IsConnected()
	if bot != nil {
		application.SetDiscordBot(bot)
		application.AddSessionObserver(bot)
	}

	// Start memory watchdog (monitors heap, crashes at 2GB with dump)
	memwatch.Start(func() map[string]int {
		result := make(map[string]int)
		for _, name := range application.ListSessions() {
			if state := application.GetSession(name); state != nil {
				result[name] = state.Scrollback().Count()
			}
		}
		return result
	})

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

	// Set up graceful shutdown handler to flush logs on SIGTERM/SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		// Flush all PTY logs before exit
		application.FlushAllLogs()
		os.Exit(0)
	}()

	// Run Gio event loop - this keeps the daemon alive
	app.Main()

	// Prevent GC from finalizing lockFile (which would release the flock)
	runtime.KeepAlive(lockFile)
}

func printUsage() {
	fmt.Println(`prompt-grid - Terminal emulator with multi-view support

Usage:
  prompt-grid <session-name>              Create a new local session
  prompt-grid ssh <host> [session-name]   Create an SSH session

Examples:
  prompt-grid "My Project"
  prompt-grid ssh user@host "Remote Work"
  prompt-grid ssh myserver`)
}

func findArg(name string) int {
	for i, arg := range os.Args {
		if arg == name {
			return i
		}
	}
	return -1
}
