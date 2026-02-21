# TokenMan Installation Guide

TokenMan is a local reverse proxy for LLM API calls. It sits between Claude Code and the Anthropic API to provide caching, token compression, PII redaction, budget enforcement, and full observability.

```
Claude Code  -->  TokenMan (localhost:7677)  -->  Anthropic API
                         |
                   Dashboard (localhost:7678)
```

---

## Install

### 1. Move the binary to your PATH

```bash
# Copy to a standard location
sudo cp ./tokenman /usr/local/bin/tokenman
sudo chmod +x /usr/local/bin/tokenman

# Verify it works
tokenman version
```

Or keep it anywhere and reference it by full path.

### 2. Run initial setup

```bash
# Generate the default config at ~/.tokenman/tokenman.toml
tokenman init-config

# Store your Anthropic API key in the OS keychain
tokenman keys set anthropic
# (paste your key at the prompt — it won't be echoed)
```

### 3. Start TokenMan

```bash
# Run in the foreground (logs to terminal)
tokenman start --foreground
```

You should see:

```
  TokenMan is running!
  Proxy:     http://localhost:7677
  Dashboard: http://localhost:7678
```

To run in the background, omit `--foreground`:

```bash
tokenman start
```

Use `tokenman status` to check on it and `tokenman stop` to shut it down.

---

## Connect Claude Code to TokenMan

You have two options. Pick one.

### Option A: Environment variable (per-session)

Set the variable before launching Claude Code:

```bash
export ANTHROPIC_BASE_URL=http://localhost:7677
```

Add it to your shell profile (`~/.zshrc`, `~/.bashrc`, etc.) to make it persistent:

```bash
echo 'export ANTHROPIC_BASE_URL=http://localhost:7677' >> ~/.zshrc
source ~/.zshrc
```

### Option B: Claude Code settings file (persistent, all sessions)

Edit (or create) `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:7677"
  }
}
```

This applies automatically to every Claude Code session. No shell changes needed.

### Verify the connection

With TokenMan running and the base URL configured, start Claude Code normally. Make a request and check the TokenMan dashboard at [http://localhost:7678](http://localhost:7678) — you should see the request logged there. You can also look for the `X-Tokenman-Cache` header in responses (`HIT` or `MISS`).

---

## Configuration

The config file lives at `~/.tokenman/tokenman.toml`. Key settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `server.proxy_port` | `7677` | Port Claude Code connects to |
| `server.dashboard_port` | `7678` | Web dashboard port |
| `providers.anthropic.timeout` | `30` | Per-provider timeout in seconds |
| `security.budget.enabled` | `false` | Enable spend limits |
| `security.budget.daily_limit` | `0` | Daily spend cap in cents |
| `compression.dedup.enabled` | `true` | Deduplicate repeated content |
| `compression.history.window_size` | `10` | Conversation turns to keep |

See the full example config:

```bash
tokenman config-export tokenman-full.toml
```

Changes to the config file are picked up automatically (hot-reload) for supported settings like log level, rate limits, and cache TTL. Other changes require a restart.

---

## Useful commands

```bash
tokenman start --foreground   # Start with log output
tokenman start                # Start as background daemon
tokenman stop                 # Stop the daemon
tokenman status               # Show status and live stats
tokenman keys list            # Show which providers have keys stored
tokenman keys set openai      # Add an OpenAI key (for multi-provider routing)
tokenman keys delete anthropic # Remove a stored key
tokenman config-export out.toml  # Export current config
tokenman config-import in.toml   # Import config from file
tokenman install-service      # Install as a launchd service (macOS)
```

---

## Uninstall

### 1. Disconnect Claude Code

Remove the base URL override so Claude Code talks directly to the Anthropic API again.

**If you used an environment variable:**

Remove the `ANTHROPIC_BASE_URL` line from your shell profile (`~/.zshrc`, `~/.bashrc`, etc.), then reload:

```bash
# Edit your profile and delete the ANTHROPIC_BASE_URL line, then:
source ~/.zshrc

# Or unset it for the current session immediately:
unset ANTHROPIC_BASE_URL
```

**If you used the Claude Code settings file:**

Edit `~/.claude/settings.json` and remove the `ANTHROPIC_BASE_URL` entry:

```json
{
  "env": {}
}
```

Or delete the `"env"` block entirely if it only contained that variable.

### 2. Stop TokenMan

```bash
tokenman stop
```

### 3. Remove the launchd service (if installed)

```bash
launchctl unload ~/Library/LaunchAgents/com.tokenman.plist 2>/dev/null
rm -f ~/Library/LaunchAgents/com.tokenman.plist
```

### 4. Remove data and config

```bash
rm -rf ~/.tokenman
```

This deletes the config file, SQLite database, logs, and PID file.

### 5. Remove the binary

```bash
sudo rm -f /usr/local/bin/tokenman
```

After these steps, Claude Code will connect directly to the Anthropic API as it did before TokenMan was installed.
