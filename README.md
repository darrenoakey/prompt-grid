![](banner.jpg)

# prompt-grid

A macOS multi-session terminal emulator built in Go with a Gio GUI. Sessions are backed by tmux and survive both app restarts and machine reboots — scrollback history is replayed from disk, working directories are tracked continuously, and Claude/Codex conversations resume where they left off.

## Features

- **Reboot-safe sessions** — tmux-backed sessions recreated on startup; full scrollback replayed from PTY log
- **CWD tracking** — current working directory polled every 30s via tmux; restored exactly on restart
- **Claude/Codex continuity** — sessions restart with `--continue`/`--resume` to restore the prior conversation
- **Tabbed sidebar** — colored session tabs in a left panel; right-click for context menu
- **Terminal windows** — pop-out any session into its own floating window
- **SSH sessions** — native SSH support
- **Discord remote control** — bot integration for screenshots, running commands, and session management
- **Session persistence** — colors, window sizes, and metadata all saved across restarts
- **Single-instance** — IPC socket routes new session requests to the running instance

## Prerequisites

- macOS (uses Cocoa via Gio)
- Go 1.21+
- tmux (`brew install tmux` — auto-installed if missing)
- Optional: `claude` CLI for Claude sessions
- Optional: Discord bot token for remote control

## Build

```bash
git clone https://github.com/darrenoakey/prompt-grid
cd prompt-grid
./run build              # outputs to output/prompt-grid
cp output/prompt-grid ~/bin/prompt-grid
```

## Usage

```bash
# Open a named session (creates it if it doesn't exist)
prompt-grid "My Project"

# Run as a background daemon (subsequent calls route to it)
prompt-grid --daemon
```

**Session management** — right-click anywhere on the left tab panel:

| Location | Options |
|----------|---------|
| Empty space | New Session, New Claude ▸ (submenu by directory) |
| Session tab | Rename, New Color, Pop Out, Bring to Front, Close |

**Keyboard shortcuts** in terminal:
- `Cmd+C` — copy selection
- `Cmd+V` — paste
- Mouse drag — select text (auto-copied on release)
- Scroll wheel — browse scrollback history

## Discord Setup

1. Create a Discord bot at the [Discord Developer Portal](https://discord.com/developers/applications)
2. Add the bot token to the macOS keychain:
   ```bash
   security add-generic-password -s prompt-grid -a discord_bot_token -w "YOUR_TOKEN"
   ```
3. Set your server ID and authorized user IDs in `~/.config/prompt-grid/config.json`

**Discord commands** (`/term ...`):

| Command | Description |
|---------|-------------|
| `/term list` | List all active sessions |
| `/term new <name>` | Create a new session |
| `/term screenshot <name>` | Capture and send a terminal screenshot |
| `/term run <name> <cmd>` | Run a command in a session |
| `/term connect <name>` | Start streaming a session to its Discord channel |
| `/term disconnect <name>` | Stop streaming |
| `/term focus <name>` | Bring session window to front |
| `/term close <name>` | Close a session |

Each session automatically gets a dedicated Discord channel; output streams there when connected.

## Architecture

Sessions are managed by a dedicated tmux server (`tmux -L prompt-grid`) that outlives the app process. On startup, `discoverSessions()` reconnects to live tmux sessions and recreates any that died (e.g., after reboot) from saved config metadata. PTY output is logged continuously to `~/.config/prompt-grid/sessions/<name>.ptylog`; on reconnect or recreation this log is replayed through the ANSI parser to rebuild the scrollback buffer before the live PTY attaches. The current working directory is polled from tmux every 30 seconds and saved to config, so recreated sessions land exactly where you left off. Claude and Codex sessions are recreated with `--continue`/`--resume` to restore the prior conversation.

## Configuration

Config lives at `~/.config/prompt-grid/config.json`. PTY logs at `~/.config/prompt-grid/sessions/`. Both are created automatically on first run.

## License

MIT
