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
- `src/tmux/` - tmux CLI wrapper (session lifecycle via `tmux -L claude-term`)
- `src/pty/` - PTY session management (runs `tmux attach` inside a PTY)
- `src/emulator/` - ANSI parser, screen buffer, scrollback
- `src/render/` - Renderer interface, Gio renderer, image renderer for PNG
- `src/gui/` - Gio windows, widgets, control center
- `src/discord/` - Bot, slash commands, screenshot streaming
- `src/config/` - Config loading, keyring access
- `src/logging/` - JSONL logging with dated directories
- `src/ipc/` - IPC server/client for session requests
- `src/memwatch/` - Memory watchdog (2GB crash with diagnostic dump)

### tmux-Based Session Architecture (Survives Restart)
Sessions are managed by tmux via a dedicated server (`tmux -L claude-term`):
- **Main process**: GUI, Discord bot, IPC server
- **tmux server**: Owns all terminal sessions, survives GUI restarts

```
Main Process (GUI/Discord/IPC)
    │
    ├── PTY running ──► tmux attach -t "Work"    ──► tmux server
    │                                                  └── session "Work" (shell)
    │
    └── PTY running ──► tmux attach -t "Server"  ──► tmux server
                                                       └── session "Server" (shell)
```

Key behaviors:
- tmux sessions survive main process restart
- On startup, main process discovers existing tmux sessions via `tmux list-sessions`
- Each session gets a PTY running `tmux attach-session -t <name>`
- ANSI parser reads PTY output identically to before -- rendering pipeline unchanged
- tmux configured to be invisible: status bar off, prefix key disabled, all keybindings unbound
- Close session (`CloseSession`) = kill tmux session + close PTY + close window
- Close window (`detachSession`) = clear window ref only; PTY and tmux session stay alive
- SSH sessions: `tmux new-session` with `ssh host` as the initial command

tmux wrapper (`src/tmux/tmux.go`):
- `ServerName()` - realm-aware tmux server name
- `NewSession(name, sshHost, cols, rows)` - create detached tmux session
- `AttachArgs(name)` - returns cmd/args for `pty.StartCommand()`
- `ListSessions()` / `HasSession()` / `KillSession()` / `RenameSession()`
- `KillServer()` - for test cleanup
- `GetSocketDir()` - IPC socket directory (realm-aware)
- `EnsureInstalled()` - check for tmux, brew install if missing

### SessionState Fields
- `pty *pty.Session` - PTY running tmux attach
- `name string` - session name
- `sshHost string` - SSH host (empty for local sessions)
- Accessors: `PTY()`, `Name()`, `IsSSH()`, `SSHHost()`

### Gio GUI Notes
- Use `new(app.Window)` then `win.Option()` separately (not `app.NewWindow()`)
- Font loading: `text.NewShaper(text.WithCollection(collection))` - allows system font fallback for unicode
- Embedded fonts via `//go:embed fonts/*.ttf`
- `app.Position` doesn't exist - windows stack by default
- Keyboard: `key.Filter{Optional: key.ModShift|key.ModCtrl|key.ModCommand}` for all keys — but Tab requires explicit `key.Filter{Name: key.NameTab}` (see gotcha below)
- Handle both `key.Event` and `key.EditEvent` - EditEvent has proper text, Event has key names
- Key names are uppercase (e.g., "A" for 'a' key) - must handle shift for proper case
- Clipboard: `clipboard.WriteCmd`/`clipboard.ReadCmd` with `transfer.TargetFilter` for paste events
- Click-to-focus requires handling `pointer.Filter` events manually

### Session Colors
- 128 pre-generated colors in HSV space (render/palette.go)
- Each session gets random color assignment
- Light backgrounds get pure black text, dark backgrounds get pure white text
- ANSI indexed colors adjusted for contrast via `AdjustForContrast()` (luminance threshold 0.5, shift ±77/255)
- Control center tabs match session colors exactly (bg + fg)

### Tab Panel Context Menu
Right-click anywhere on the left tab panel:
- **On empty space**: Shows "New Session" only
- **On a tab**: Shows "New Session", "Rename", "Bring to Front", "Close"

New session auto-naming:
- Sessions created via context menu get names "Session 1", "Session 2", etc.
- Uses the lowest available number (if Session 1 and Session 3 exist, next is Session 2)

Rename sessions:
- Right-click tab → "Rename" opens inline text editor
- Enter confirms, Escape cancels
- Updates both control center and terminal window title
- Arrow keys, Home/End, Delete/Backspace work in rename editor

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
- tmux runs the user's default shell in each session
- SSH sessions: tmux runs `ssh host` as the initial command

### Single Instance Architecture
- Uses Unix socket at `/tmp/claude-term-sessions/ipc.sock` for IPC
- First instance becomes primary, listens on socket
- Subsequent invocations send session request to primary and exit
- All sessions managed by single app with one control center
- Daemonization: re-exec with `CLAUDE_TERM_DAEMON=1` env var, parent exits immediately
- Single-instance guard: `syscall.Flock` on `/tmp/claude-term-sessions/daemon.lock`
- **CRITICAL**: `runtime.KeepAlive(lockFile)` required after flock -- Go GC will finalize unreferenced `*os.File`, silently releasing the lock

### Deployment
- `./run build` outputs to `output/claude-term`
- `auto` service runs `~/bin/claude-term --daemon` -- must copy binary there after build
- Deploy: `cp output/claude-term ~/bin/claude-term && ~/bin/auto restart claude-term`

### Scrollback Viewing
- Mouse wheel scrolls through terminal history
- `scrollOffset` in SessionState tracks view position (0 = live view)
- Scroll up = increase offset (view history), scroll down = decrease (toward live)
- Any new output auto-resets to live view (scrollOffset = 0)
- Rendering blends scrollback buffer + current screen based on offset

### Gio Event Handling Gotchas
- Widget state must persist across frames for events to match targets
- Creating new objects each frame breaks event routing (e.g., tab clicks)
- Use persistent maps keyed by stable IDs (e.g., session names)
- `event.Op(gtx.Ops, target)` registers target - target must be same object each frame
- Right-click: check `e.Buttons.Contain(pointer.ButtonSecondary)` or control+click
- After state changes, call `window.Invalidate()` to trigger redraw
- Cross-window operations (e.g., raising another window) must be async via goroutine to avoid deadlock
- **CRITICAL**: All pointer event types (Press, Drag, Release, Scroll) must be in ONE filter - separate filters don't work
- **CRITICAL**: When switching keyboard input between handlers (e.g., rename input vs terminal), explicitly request focus with `gtx.Execute(key.FocusCmd{Tag: target})`. Without it, `key.EditEvent` (typed characters) won't be delivered to the new handler. Also disable competing handlers during the switch to prevent event stealing.
- **CRITICAL (scroll events)**: `pointer.Filter` requires `ScrollY` (and/or `ScrollX`) bounds for scroll events to be delivered. Default `{Min: 0, Max: 0}` silently rejects all scroll events. Set bounds based on scrollable content size.
- **CRITICAL (Tab key)**: Gio intercepts Tab as a `SystemEvent` for focus navigation. Catch-all `key.Filter` (empty `Name`) skips SystemEvents. To receive Tab in a terminal widget, add an explicit `key.Filter{Name: key.NameTab}` alongside the catch-all filter. Without this, Tab is consumed by Gio and never reaches the widget's event handler.
- **CRITICAL (macOS deadlock)**: NEVER call blocking operations (subprocess, cross-window `window.Option()`) synchronously from within a Gio frame handler. On macOS, the Cocoa main thread blocks while the frame handler runs. If the frame handler calls `window.Option()` on a different window, it dispatches to the Cocoa main thread → deadlock. All context menu actions (New Session, Close, Rename, Bring to Front) run in goroutines for this reason.

### Discord Bot
- Auto-reconnects with exponential backoff (1s to 10 min) on disconnect
- Daemon stays running when control window closes (for Discord-only operation)
- Commands: `/term list`, `/term new`, `/term screenshot`, `/term run`, `/term connect`, `/term disconnect`, `/term focus`, `/term close`
- Token stored in macOS keyring (`claude-term/discord_bot_token`)
- Logs to `~/.config/claude-term/discord.log`

### Memory Safeguards
- **Scrollback cap**: 100K lines per session, oldest chunks trimmed (`src/emulator/scrollback.go`)
- **Parser caps**: intermediate string (64B), OSC string (64KB) — resets to ground on overflow
- **PTY readLoop**: passes `buf[:n]` directly to parser (no per-read allocation)
- **Gio themes**: `material.NewTheme()` called once at construction, stored as persistent field on ControlWindow and TerminalWidget — NOT per-frame
- **Discord streamer**: `lastScreenshot` nilled after send to release PNG bytes
- **IPC server**: 5s deadline on connections to prevent hung goroutines
- **termWidgets cleanup**: stale entries cleaned alongside tabStates in control window layout
- **Memory watchdog** (`src/memwatch/`): checks HeapAlloc every 10s, logs stats every 5min, crashes with diagnostic dump at 2GB (exit code 2). Dump includes: MemStats, goroutine stacks, heap profile, per-session scrollback counts, allocation rate history. Dump written to `~/.config/claude-term/memdump-{timestamp}.log`.

## Testing
126 tests covering emulator, PTY, rendering, tmux lifecycle, GUI state/behavior, memory watchdog, rename flow, color contrast.

### Test Isolation with Realms
- `CLAUDE_TERM_REALM` env var namespaces tmux server name and socket directories
- Tests set unique realm: `test-{pid}-{timestamp}`
- tmux server: `claude-term-{realm}` (isolated from production)
- Socket directory: `/tmp/claude-term-{realm}/`
- Complete isolation from production instance
- TestMain cleans up via `tmux.KillServer()` and removes realm directory

### TestDriver ("Interfaced User" Pattern)
Located in `src/gui/testdriver.go`:
- Input actions: `TypeText`, `SendKeys`, `SendCtrlC`, selection ops
- Scrollback: `ScrollUp`, `ScrollDown`, `ScrollToTop`, `ScrollToBottom`
- State queries: `GetScreenContent`, `GetCursorPosition`, `GetScrollOffset`
- Wait helpers: `WaitForContent`, `WaitForPattern`, `WaitForScrollback`
- Rename: `StartRename`, `TypeInRename`, `ConfirmRename`, `CancelRename`, `IsRenaming`, `GetRenameName`, `GetRenameCursorPos`
- Control window: `EnsureControlWindow`, `GetControlSelected`, `SetControlSelected`, `WaitForSessionName`
