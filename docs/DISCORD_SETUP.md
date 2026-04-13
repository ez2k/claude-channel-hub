# Discord Channel Setup

Discord support uses the official `discord@claude-plugins-official` plugin.
The Go harness spawns `claude --channels plugin:discord@claude-plugins-official`
with `DISCORD_BOT_TOKEN` injected into the subprocess environment.

## Plugin Details

- **Plugin**: `discord@claude-plugins-official`
- **Version**: `0.0.4`
- **Source**: `/root/.claude/plugins/marketplaces/claude-plugins-official/external_plugins/discord/`
- **Runtime**: Bun (TypeScript MCP server via `discord.js`)

## Required Environment Variables

| Variable | Description |
|---|---|
| `DISCORD_BOT_TOKEN` | Bot token from the Discord Developer Portal |
| `DISCORD_STATE_DIR` | (optional) Override state directory; default: `~/.claude/channels/discord` |
| `DISCORD_ACCESS_MODE` | (optional) Set to `static` to snapshot access.json at boot and never mutate it |

## State Directory

State is stored in `~/.claude/channels/discord/`:

```
~/.claude/channels/discord/
  access.json          # access control: dmPolicy, allowFrom, groups, pending pairings
  .env                 # fallback token file (format: DISCORD_BOT_TOKEN=MTIz...)
  approved/            # approved pairing records
  inbox/               # inbound message queue
```

The `.env` file is an alternative way to supply the token when the process
does not inherit environment variables. It is chmod 600. Real env wins over file.

## MCP Tools Provided

The plugin exposes these MCP tools to Claude Code:

| Tool | Description |
|---|---|
| `reply` | Send a message to a Discord DM or guild channel |
| `react` | Add an emoji reaction to a Discord message |
| `edit_message` | Edit a previously sent message |
| `download_attachment` | Download a file attachment from a message |
| `fetch_messages` | Fetch recent messages from a channel (lookback only; Discord bots have no search API) |

## Access Control

The plugin has three DM policies (set in `access.json`):

- `pairing` (default): Users must pair via a 5-letter code challenge
- `allowlist`: Only users in `allowFrom` list are accepted
- `disabled`: DMs are not accepted

Guild channel access is configured per channel-ID in the `groups` map,
with optional `requireMention` and per-channel `allowFrom`.

Manage access via the `/discord:access` skill in Claude Code.

## Discord Bot Permissions

The bot requires these Gateway Intents in the Discord Developer Portal:

- **DirectMessages** + **Partials.Channel** — receive DMs
- **Guilds** + **GuildMessages** + **MessageContent** — receive guild messages

Enable "Message Content Intent" in the Bot settings (Privileged Gateway Intent).

## Installation Steps

### 1. Install plugin dependencies

```bash
cd /root/.claude/plugins/marketplaces/claude-plugins-official/external_plugins/discord
bun install
```

### 2. Register plugin in installed_plugins.json

Add the following entry to `/root/.claude/plugins/installed_plugins.json` under `"plugins"`:

```json
"discord@claude-plugins-official": [
  {
    "scope": "user",
    "installPath": "/root/.claude/plugins/marketplaces/claude-plugins-official/external_plugins/discord",
    "version": "0.0.4",
    "installedAt": "2026-04-13T07:49:51.981Z",
    "lastUpdated": "2026-04-13T07:49:51.981Z"
  }
]
```

### 3. Create a Discord bot

1. Go to https://discord.com/developers/applications
2. Create a new Application, then add a Bot
3. Enable **Message Content Intent** under Bot > Privileged Gateway Intents
4. Copy the bot token

### 4. Set the token

Either export it in your shell environment:

```bash
export DISCORD_BOT_TOKEN=your-token-here
```

Or write it to the state `.env` file (plugin reads this as fallback):

```bash
mkdir -p ~/.claude/channels/discord
echo "DISCORD_BOT_TOKEN=your-token-here" > ~/.claude/channels/discord/.env
chmod 600 ~/.claude/channels/discord/.env
```

### 5. Enable the bot in configs/channels.yaml

Uncomment the discord-bot section and set `enabled: true`:

```yaml
  - id: discord-bot
    type: discord
    name: "디스코드 봇"
    enabled: true
    token: "${DISCORD_BOT_TOKEN}"
    plugin: "discord"
    plugin_marketplace: "claude-plugins-official"
```

## How the Harness Wires the Token

`internal/bot/process.go` switches on `Config.Type`:

```go
case "discord":
    env = append(env, fmt.Sprintf("DISCORD_BOT_TOKEN=%s", p.bot.Config.Token))
```

The token from `configs/channels.yaml` (after env-var substitution) is passed
directly to the Claude subprocess environment. `ANTHROPIC_API_KEY` is stripped
so the harness uses subscription auth, not API key billing.
