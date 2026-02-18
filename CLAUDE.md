# prompt-grid Project Notes

## Project Overview
A Go terminal emulator with multi-view support using Gio for GUI, Discord integration for remote control.

## Build & Test
```bash
./run build    # Build to output/prompt-grid
./run test     # Run all tests
~/bin/prompt-grid "Session Name"  # Launch (runs in background via nohup)
```

## Key Architecture

### Package Structure
- `src/tmux/` - tmux CLI wrapper (session lifecycle via `tmux -L prompt-grid`)
- `src/pty/` - PTY session management (runs `tmux attach` inside a PTY)
- `src/ptylog/` - PTY output logging and replay for session persistence
- `src/emulator/` - ANSI parser, screen buffer, scrollback
- `src/render/` - Renderer interface, Gio renderer, image renderer for PNG
- `src/gui/` - Gio windows, widgets, control center
- `src/discord/` - Bot, slash commands, screenshot streaming
- `src/config/` - Config loading, keyring access, session metadata
- `src/logging/` - JSONL logging with dated directories
- `src/ipc/` - IPC server/client for session requests
- `src/memwatch/` - Memory watchdog (2GB crash with diagnostic dump)

### tmux-Based Session Architecture (Survives Restart)
Sessions are managed by tmux via a dedicated server (`tmux -L prompt-grid`):
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
- Close session (`CloseSession`) = kill tmux session + close PTY + close window + delete log/metadata
- Close window (`detachSession`) = clear window ref only; PTY and tmux session stay alive
- SSH sessions: `tmux new-session` with `ssh host` as the initial command

### Session Persistence Across Reboots
Sessions survive reboots via PTY output logging and config metadata:
- **PTY log**: Raw PTY output bytes continuously written to `~/.config/prompt-grid/sessions/<name>.ptylog`
- **Session metadata**: `config.SessionInfo` (type, workDir, sshHost) saved in `config.json` under `sessions` map
- **On startup**: `discoverSessions()` reconnects live tmux sessions AND recreates dead ones from config
- **Replay**: Log bytes fed through ANSI parser to restore scrollback before connecting live PTY
- **Sequence**: Create emulator → replay log → connect PTY (live output overwrites screen, scrollback retained)

PTY log writer (`src/ptylog/`):
- Buffered writes: 2s timer or 64KB threshold triggers flush
- Auto-truncation: When file exceeds 10MB, keeps last 5MB (seeks to safe boundary: `\n` or ESC)
- Lifecycle: Created on session start, closed on session close/rename, deleted on session close
- Rename: Old writer closed, file renamed, new writer opened

Session types persisted in config:
- `"shell"` — default local sessions (recreated with saved workDir)
- `"ssh"` — SSH sessions (recreated with `ssh <host>`)
- `"claude"` — Claude sessions (recreated with `claude --continue` to resume last conversation)
- `"codex"` — Codex sessions (recreated with `codex --resume`)

### Working Directory Tracking (CWD Persistence)
- `workDir` in `SessionInfo` is updated every 30s via `App.updateAllCWDs()`
- Uses `tmux.GetPaneCurrentPath(name)` → `tmux display-message -p -t <name> "#{pane_current_path}"`
- Only non-SSH sessions are tracked (SSH CWD is remote, meaningless locally)
- Goroutine started in `NewApp()` via `startCWDUpdater()` — lives for app lifetime
- Tests call `app.updateAllCWDs()` directly instead of waiting 30s
- **macOS path symlink gotcha**: tmux reports resolved paths (`/private/var` not `/var`). Tests must use `filepath.EvalSymlinks` when comparing expected vs actual workDir.
- `CLAUDE_BINARY_PATH` env var overrides the claude binary path — used in tests to point at a fake script

tmux wrapper (`src/tmux/tmux.go`):
- `ServerName()` - realm-aware tmux server name
- `NewSession(name, workDir string, cols, rows uint16, cmd ...string)` - create detached tmux session with optional working directory and initial command
- `AttachArgs(name)` - returns cmd/args for `pty.StartCommand()`
- `SendKeys(name string, keys ...string)` - send keystrokes to a tmux session
- `GetPaneCurrentPath(name)` - current working directory of active pane via `display-message`
- `ListSessions()` / `HasSession()` / `KillSession()` / `RenameSession()`
- `KillServer()` - for test cleanup
- `GetSocketDir()` - IPC socket directory (realm-aware)
- `EnsureInstalled()` - check for tmux, brew install if missing
- Configuration (status off, prefix None, unbind all) combined into single tmux invocation using ";" command separator (2 subprocesses instead of 4)

### SessionState Fields
- `pty *pty.Session` - PTY running tmux attach
- `name string` - session name
- `sshHost string` - SSH host (empty for local sessions)
- `ptyLog *ptylog.Writer` - PTY output logger for persistence
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
- **CRITICAL (clipboard MIME type)**: Gio uses `"application/text"` NOT `"text/plain"` for clipboard operations. Using `"text/plain"` in `clipboard.WriteCmd` or `transfer.TargetFilter` will silently fail — data never arrives.
- Click-to-focus requires handling `pointer.Filter` events manually

### Session Colors
- 128 pre-generated colors in HSV space (render/palette.go)
- Colors persisted in `~/.config/prompt-grid/config.json` as `session_colors` map (name → palette index)
- On session create/reconnect: lookup saved index, or assign random + save
- On session close: delete color mapping from config
- On session rename: move mapping old→new name
- "New Color" context menu: assigns new random palette index, persists, deletes stale termWidget for recreation
- Light backgrounds get pure black text, dark backgrounds get pure white text
- ANSI indexed colors adjusted for contrast via `AdjustForContrast()` (luminance threshold 0.5, shift ±77/255)
- Control center tabs match session colors exactly (bg + fg)

### Tab Panel Context Menu
Right-click anywhere on the left tab panel:
- **On empty space**: Shows "New Session", "New Claude ▸" (submenu)
- **On a tab**: Shows "New Session", "New Claude ▸", "Rename", "New Color", "Pop Out"/"Bring to Front"/"Call Back", "Close"

New session auto-naming:
- Sessions created via context menu get names "Session 1", "Session 2", etc.
- Uses the lowest available number (if Session 1 and Session 3 exist, next is Session 2)

New Claude Session:
- "New Claude ▸" shows a hover submenu listing directories under ~/src
- Submenu auto-wraps into multiple columns when items exceed screen height
- Clicking a directory creates a session named after the directory, running `claude` in it
- Uses `tmux.SendKeys` to send "claude\nEnter" to the shell after session creation
- Duplicate names get `-2`, `-3` etc. suffixes

### Working Directory (Initial)
- All new sessions (via context menu or IPC) default to `~/src` as working directory
- SSH sessions skip the working directory (they run on the remote host)
- `AddClaudeSession(name, dir)` creates a session in a specific directory and runs `claude`
- CWD is tracked continuously — see "Working Directory Tracking" section above

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
- Uses Unix socket at `/tmp/prompt-grid-sessions/ipc.sock` for IPC
- First instance becomes primary, listens on socket
- Subsequent invocations send session request to primary and exit
- All sessions managed by single app with one control center
- Daemonization: re-exec with `CLAUDE_TERM_DAEMON=1` env var, parent exits immediately
- Single-instance guard: `syscall.Flock` on `/tmp/prompt-grid-sessions/daemon.lock`
- **CRITICAL**: `runtime.KeepAlive(lockFile)` required after flock -- Go GC will finalize unreferenced `*os.File`, silently releasing the lock

### Deployment
- `./run build` outputs to `output/prompt-grid`
- `auto` service runs `~/bin/prompt-grid --daemon` -- must copy binary there after build
- Deploy: `cp output/prompt-grid ~/bin/prompt-grid && ~/bin/auto restart prompt-grid`

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
- **CRITICAL (focus fight)**: When embedding a widget with `skipKeyboard=true` (parent handles keyboard), the widget must NOT call `gtx.Execute(key.FocusCmd{Tag: w})` on pointer.Press. Otherwise it steals focus from the parent's keyboard handler, making clipboard (Cmd+C/V) and all key input stop working.
- **CRITICAL (scroll events)**: `pointer.Filter` requires `ScrollY` (and/or `ScrollX`) bounds for scroll events to be delivered. Default `{Min: 0, Max: 0}` silently rejects all scroll events. Set bounds based on scrollable content size.
- **CRITICAL (Tab key)**: Gio intercepts Tab as a `SystemEvent` for focus navigation. Catch-all `key.Filter` (empty `Name`) skips SystemEvents. To receive Tab in a terminal widget, add an explicit `key.Filter{Name: key.NameTab}` alongside the catch-all filter. Without this, Tab is consumed by Gio and never reaches the widget's event handler.
- **CRITICAL (macOS deadlock)**: NEVER call blocking operations (subprocess, cross-window `window.Option()`) synchronously from within a Gio frame handler. On macOS, the Cocoa main thread blocks while the frame handler runs. If the frame handler calls `window.Option()` on a different window, it dispatches to the Cocoa main thread → deadlock. All context menu actions (New Session, Close, Rename, Bring to Front) run in goroutines for this reason.
- **Gio image fit**: `widget.Image{Src: logoOp, Fit: widget.Contain}` scales with aspect ratio preserved. Set `gtx.Constraints.Max` to the bounding box before Layout. Font weight: `font.Bold` from `gioui.org/font` package, NOT `text.Bold`.
- **Embedded PNG decode**: Need `_ "image/png"` import AND `image.Decode(strings.NewReader(string(bytes)))` — do not use `png.Decode()` directly as `image.Image` interface type won't be inferred.
- **Anchored header layout**: For logo-left / search-center / status-right, use `op.Offset()` with calculated pixel coordinates, NOT Flex+spacers. Flex spacers don't reliably center when siblings have unequal widths.
- **Status bar placement**: Place per-panel status bars inside that panel's Flex column (terminal column), not the outer window Flex, to avoid spanning the sidebar.

### Discord Bot
- Auto-reconnects with exponential backoff (1s to 10 min) on disconnect
- Daemon stays running when control window closes (for Discord-only operation)
- Commands: `/term list`, `/term new`, `/term screenshot`, `/term run`, `/term connect`, `/term disconnect`, `/term focus`, `/term close`
- Token stored in macOS keyring (`prompt-grid/discord_bot_token`)
- Logs to `~/.config/prompt-grid/discord.log`

#### Session Lifecycle Integration
- Auto-creates Discord channels for each terminal session in a dedicated category
- Session lifecycle handlers: `SessionAdded`, `SessionRenamed`, `SessionClosed`
- Maintains session-to-channel mapping (`sessionChannels`, `channelSessions`)
- Channel names sanitized from session names (alphanumeric + hyphens, fallback to "session")
- Orphan channel cleanup: removes Discord channels when sessions are deleted
- Orphan session cleanup: stops streaming to channels that no longer exist
- Each session gets dedicated Streamer that auto-streams terminal output to its Discord channel
- Claude UI footer (prompt/status lines) stripped from streamed output via `stripClaudeFooter()`

### Memory Safeguards
- **Scrollback cap**: 100K lines per session, oldest chunks trimmed (`src/emulator/scrollback.go`)
- **Parser caps**: intermediate string (64B), OSC string (64KB) — resets to ground on overflow
- **PTY readLoop**: passes `buf[:n]` directly to parser (no per-read allocation)
- **Gio themes**: `material.NewTheme()` called once at construction, stored as persistent field on ControlWindow and TerminalWidget — NOT per-frame
- **Discord streamer**: `lastScreenshot` nilled after send to release PNG bytes
- **IPC server**: 5s deadline on connections to prevent hung goroutines
- **termWidgets cleanup**: stale entries cleaned alongside tabStates in control window layout
- **PTY log cap**: 10MB per session, auto-truncated to 5MB at safe boundary (`\n` or ESC byte)
- **Memory watchdog** (`src/memwatch/`): checks HeapAlloc every 10s, logs stats every 5min, crashes with diagnostic dump at 2GB (exit code 2). Dump includes: MemStats, goroutine stacks, heap profile, per-session scrollback counts, allocation rate history. Dump written to `~/.config/prompt-grid/memdump-{timestamp}.log`.

## Testing
170 tests covering emulator, PTY, PTY log persistence, rendering, tmux lifecycle, GUI state/behavior, memory watchdog, rename flow, color contrast, color persistence, pop-out/callback, window sizes, session metadata persistence, session recreation after reboot, CWD tracking, claude --continue on recreate.

### Test Isolation with Realms
- `CLAUDE_TERM_REALM` env var namespaces tmux server name and socket directories
- Tests set unique realm: `test-{pid}-{timestamp}`
- tmux server: `prompt-grid-{realm}` (isolated from production)
- Socket directory: `/tmp/prompt-grid-{realm}/`
- Complete isolation from production instance
- TestMain cleans up via `tmux.KillServer()` and removes realm directory
- **CRITICAL**: TestMain sets `HOME` to temp dir and `SHELL=/bin/bash` to avoid user's slow shell init files (.zshrc/.bashrc) blocking tmux sessions. Without this, the shell inside tmux never becomes interactive during tests.
- **Reboot simulation in tests**: Must call `state.pty.SetOnExit(nil)` before killing the session. In a real reboot the OS kills the process instantly — no callbacks fire and config stays on disk. If you kill tmux without nil-ing OnExit, the callback fires, cleans up config, and recreation never happens.

### Session Exit vs Reboot Detection (`App.startupComplete`)
- `App.startupComplete` is set to `true` after `discoverSessions()` returns in `NewApp()`
- In `SetOnExit`: if `startupComplete == true`, always clean up config (user intentionally exited)
- If `startupComplete == false` (still in startup reconnection), skip cleanup so it can retry
- **Root cause of session resurrection bug**: `tmux.ListSessions()` fails when the last session exits (tmux server shuts down too) — this was being mis-identified as a reboot. The `startupComplete` flag eliminates the ambiguity entirely.

### TestDriver ("Interfaced User" Pattern)
Located in `src/gui/testdriver.go`:
- Input actions: `TypeText`, `SendKeys`, `SendCtrlC`, selection ops
- Scrollback: `ScrollUp`, `ScrollDown`, `ScrollToTop`, `ScrollToBottom`
- State queries: `GetScreenContent`, `GetCursorPosition`, `GetScrollOffset`
- Wait helpers: `WaitForContent`, `WaitForPattern`, `WaitForScrollback`
- Rename: `StartRename`, `TypeInRename`, `ConfirmRename`, `CancelRename`, `IsRenaming`, `GetRenameName`, `GetRenameCursorPos`
- Control window: `EnsureControlWindow`, `GetControlSelected`, `SetControlSelected`, `WaitForSessionName`
- Pop-out/callback: `PopOut`, `CallBack`, `HasWindow`, `WaitForWindow`
