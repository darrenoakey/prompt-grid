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
- `src/pty/` - PTY session management, SSH support
- `src/emulator/` - ANSI parser, screen buffer, scrollback
- `src/render/` - Renderer interface, Gio renderer, image renderer for PNG
- `src/gui/` - Gio windows, widgets, control center
- `src/discord/` - Bot, slash commands, screenshot streaming
- `src/config/` - Config loading, keyring access
- `src/logging/` - JSONL logging with dated directories

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
- `src/ipc/` - IPC server/client for session requests

## Testing
74 tests covering emulator, PTY, rendering, GUI app logic.
