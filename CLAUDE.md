# Claude-Term Project Notes

## Project Overview
A Go terminal emulator with multi-view support using Gio for GUI, Discord integration for remote control.

## Build & Test
```bash
./run build    # Build to output/claude-term
./run test     # Run all tests
~/bin/claude-term "Session Name"  # Launch (runs in background via nohup)
```

## Key Architecture

### Package Structure
- `src/session/` - Session daemon (PTY owner), client, protocol
- `src/pty/` - PTY session management, SSH support (used by session daemon)
- `src/emulator/` - ANSI parser, screen buffer, scrollback
- `src/render/` - Renderer interface, Gio renderer, image renderer for PNG
- `src/gui/` - Gio windows, widgets, control center
- `src/discord/` - Bot, slash commands, screenshot streaming
- `src/config/` - Config loading, keyring access
- `src/logging/` - JSONL logging with dated directories
- `src/ipc/` - IPC server/client for session requests

### Session Daemon Architecture (Survives Restart)
Each terminal session runs in its own daemon process:
- **Main process**: GUI, Discord bot, IPC server
- **Session daemons**: One per terminal, owns the PTY

```
Main Process (GUI/Discord/IPC)
    │
    ├── connects to ──► Session Daemon "Work" (PTY owner)
    │                   └── /tmp/claude-term-sessions/Work.sock
    │
    └── connects to ──► Session Daemon "Server" (PTY owner)
                        └── /tmp/claude-term-sessions/Server.sock
```

Key behaviors:
- Session daemons survive main process restart
- On startup, main process discovers existing daemons and reconnects
- Socket files in `/tmp/claude-term-sessions/`
- Info files (`.json`) store PID and start time for validation
- History buffer (256KB) replayed on reconnect for screen reconstruction
- `session.Client` replaces direct PTY access in GUI code
- Close session = terminate daemon; close window = disconnect (daemon survives)

Protocol (`src/session/protocol.go`):
- `MsgData` (0x01): PTY data bidirectional
- `MsgResize` (0x02): Terminal resize
- `MsgInfo` (0x03/0x04): Session metadata
- `MsgHistory` (0x05): History replay complete marker
- `MsgClose` (0x06): Terminate session

Stale session detection:
- Check info file PID still running
- Verify process start time matches
- Try socket connection with timeout

### Gio GUI Notes
- Use `new(app.Window)` then `win.Option()` separately (not `app.NewWindow()`)
- Font loading: `text.NewShaper(text.WithCollection(collection))` - allows system font fallback for unicode
- Embedded fonts via `//go:embed fonts/*.ttf`
- `app.Position` doesn't exist - windows stack by default
- Keyboard: `key.Filter{Optional: key.ModShift|key.ModCtrl|key.ModCommand}` for all keys
- Handle both `key.Event` and `key.EditEvent` - EditEvent has proper text, Event has key names
- Key names are uppercase (e.g., "A" for 'a' key) - must handle shift for proper case
- Clipboard: `clipboard.WriteCmd`/`clipboard.ReadCmd` with `transfer.TargetFilter` for paste events
- Click-to-focus requires handling `pointer.Filter` events manually

### Session Colors
- 128 pre-generated colors in HSV space (render/palette.go)
- Each session gets random color assignment
- Light backgrounds get dark text, dark backgrounds get light text
- Control center tabs match session colors exactly (bg + fg)

### Text Selection
- Mouse drag selects text (pointer.Press/Drag/Release)
- Selected cells rendered with inverted fg/bg colors
- Selection auto-copied to clipboard on mouse release
- Cmd+C copies selection, Cmd+V pastes

### UTF-8 Support
- Parser buffers multi-byte UTF-8 sequences
- Properly decodes box-drawing and unicode characters
- Split bytes across Parse() calls handled correctly

### Shell Setup
- Start shell with `-l -i` flags for interactive login shell
- Loads user's .zshrc/.bashrc for proper prompt

### Single Instance Architecture
- Uses Unix socket at `/tmp/claude-term.sock` for IPC
- First instance becomes primary, listens on socket
- Subsequent invocations send session request to primary and exit
- All sessions managed by single app with one control center
- Daemonization: re-exec with `CLAUDE_TERM_DAEMON=1` env var, parent exits immediately
- Session daemons spawned with `--session-daemon` flag (internal)

### Gio Event Handling Gotchas
- Widget state must persist across frames for events to match targets
- Creating new objects each frame breaks event routing (e.g., tab clicks)
- Use persistent maps keyed by stable IDs (e.g., session names)
- `event.Op(gtx.Ops, target)` registers target - target must be same object each frame
- Right-click: check `e.Buttons.Contain(pointer.ButtonSecondary)` or control+click
- After state changes, call `window.Invalidate()` to trigger redraw
- Cross-window operations (e.g., raising another window) must be async via goroutine to avoid deadlock

### Discord Bot
- Auto-reconnects with exponential backoff (1s to 10 min) on disconnect
- Daemon stays running when control window closes (for Discord-only operation)
- Commands: `/term list`, `/term new`, `/term screenshot`, `/term run`, `/term connect`, `/term disconnect`, `/term focus`, `/term close`
- Token stored in macOS keyring (`claude-term/discord_bot_token`)
- Logs to `~/.config/claude-term/discord.log`

## Testing
90 tests covering emulator, PTY, rendering, session daemon, GUI app logic.
