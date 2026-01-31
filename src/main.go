package main

import (
	"fmt"
	"os"

	"gioui.org/app"

	"claude-term/src/gui"
)

func main() {
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

	// Create application
	application := gui.NewApp()

	// Create session
	state, err := application.NewSession(sessionName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating session: %v\n", err)
		os.Exit(1)
	}

	// Start session
	if sshHost != "" {
		err = state.Session().StartSSH(sshHost)
	} else {
		err = state.Session().Start()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting session: %v\n", err)
		os.Exit(1)
	}

	// Create windows
	go func() {
		termWin, err := application.CreateTerminalWindow(sessionName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating terminal window: %v\n", err)
			return
		}

		// Run terminal window
		if err := termWin.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Terminal window error: %v\n", err)
		}

		// Close session when window closes
		application.CloseSession(sessionName)
		os.Exit(0)
	}()

	// Create and run control window
	go func() {
		controlWin := application.CreateControlWindow()
		if err := controlWin.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Control window error: %v\n", err)
		}
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
