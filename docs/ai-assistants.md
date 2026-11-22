# Connect an AI assistant

Gunkly can talk to AI assistants such as Claude. Once connected you can ask "What chores are overdue?" or say "Mark 'Water the plants' as done", and the assistant does it for you.

The assistant only gets what you give it: the households you pick, and the actions you allow. You can cut off its access at any time.

## Set it up

1. Sign in to Gunkly and open **Account**, then **Integrations**.
2. In the card **Connect an AI assistant**, press **Connect an assistant**.
3. Choose your assistant: Claude Code, Claude Desktop, Gemini CLI or Other MCP app.
4. Choose which households it may use (all of yours are ticked at first).
5. Choose what it may do. Seeing your chores is always on. "Mark chores as done, snooze or skip" is on by default; "Create chores" is off by default.
6. Press **Create access** and confirm your password.
7. Follow the numbered steps shown. Your address and access key are already filled in; use the Copy buttons.

The access key is shown only once and works like a password. Do not share it. If you lose it, revoke it and make a new one.

### Claude Code

Paste the copied command into a terminal, then start Claude Code. It looks like this:

```
claude mcp add --transport http gunkly https://your-gunkly/mcp --header "Authorization: Bearer gk_..."
```

### Claude Desktop

1. In Claude Desktop open **Settings**, **Developer**, **Edit Config**. This opens `claude_desktop_config.json`.
2. Paste the copied settings (if the file already has an `mcpServers` section, add the `gunkly` part inside it) and save.
3. Quit Claude Desktop completely and open it again.

This needs [Node.js](https://nodejs.org) installed; it runs a small helper called `mcp-remote`. The settings pass the key through an environment variable because a space inside a header value breaks on some systems.

### Gemini CLI

Add the copied settings to `.gemini/settings.json` in your home folder and restart Gemini CLI.

### Other apps

Add an MCP server with the address shown (ending in `/mcp`), transport "Streamable HTTP", and the header `Authorization` with the value `Bearer gk_...`. VS Code (Copilot) and Cursor work this way too.

ChatGPT is not supported for now: its custom connectors need an OAuth sign-in, which Gunkly does not offer yet (it is planned).

## Things to try

- "What chores are overdue?"
- "Mark 'Water the plants' as done"
- "What's coming up this week?"

## Privacy

- The assistant can see the names, due dates, repeat rules and notes of chores in the households you chose, and who they are assigned to (only whether it is you). It cannot see email addresses, other members' details, or anything outside those households.
- What it reads is sent to the company that runs your assistant (for example Anthropic or Google), under their privacy terms. Choose read-only access if you are unsure.
- It can never change members, settings, notification channels or your account, and it cannot delete chores.
- Everything it does is recorded in your household's activity log.

## Take access away

Open **Account**, **Integrations**, find the entry (for example "Claude Code (MCP)") and press **Revoke**. It stops working at once. Changing your password or removing a second factor also revokes all access keys.

## Troubleshooting

- **401 or "unauthorized":** the key was revoked, expired (they last a year) or was copied incorrectly. Create new access.
- **"Origin not allowed":** open Gunkly from the address your administrator configured and make the setup with that address.
- **"Not found" or card says access is switched off:** the administrator turned AI assistant access off (Admin, Settings).
- **Claude Desktop shows no tools:** check that Node.js is installed (run `node --version` in a terminal), that the settings file is valid, and that you fully quit and reopened Claude Desktop.
- **Assistant says it cannot do something:** it may lack the permission (for example creating chores). Create new access with the permission ticked.
