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
- Font loading: `text.NewShaper(text.NoSystemFonts(), text.WithCollection(collection))`
- Embedded fonts via `//go:embed fonts/*.ttf`
- `app.Position` doesn't exist - windows stack by default
- Keyboard input: Use `key.Filter{}` without Focus to receive all keys
- Click-to-focus requires handling `pointer.Filter` events manually

### Session Colors
- 128 pre-generated colors in HSV space (render/palette.go)
- Each session gets random color assignment
- Light backgrounds get dark text, dark backgrounds get light text
- Color bar indicator on sidebar tabs

### Known Issues to Fix
- Keyboard input not working - Gio key event handling needs investigation
- May need `key.InputOp` or different event registration approach

## Testing
72 tests covering emulator, PTY, rendering, GUI app logic.
