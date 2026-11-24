# Connect an AI assistant

Gunkly can talk to AI assistants such as Claude. Once connected you can ask "What chores are overdue?" or say "Mark 'Water the plants' as done", and the assistant does it for you.

The assistant only gets what you give it: the households you pick, and the actions you allow. You can cut off its access at any time.

## Set it up

1. Sign in to Gunkly and open **Settings**, then **AI**.
2. In the card **Connect an AI assistant**, press **Connect an assistant**.
3. Choose your assistant: Claude Code, Claude Desktop, Gemini CLI, Other MCP app, ChatGPT or Claude (connector). The last two are explained in [ChatGPT and Claude connectors](#chatgpt-and-claude-connectors); they need no access key.
4. Choose which households it may use (all of yours are ticked at first).
5. Choose what it may do. Seeing your chores is always on. "Mark chores as done, snooze or skip" is on by default; "Create and edit chores, manage areas" is off by default.
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

### ChatGPT and Claude connectors

ChatGPT custom connectors and the "Add custom connector" option of Claude (claude.ai and the desktop apps) do not take an access key. They connect by address and ask you to sign in, using OAuth. Gunkly supports this, but it is **off by default** and an instance admin has to turn it on:

1. **Admin:** open Administration, **Connections**, tick **Let apps such as ChatGPT sign in with Gunkly (OAuth)** and save. This needs `GUNKLY_BASE_URL` to be an `https://` address (for development `http://localhost` also works), and because these services connect from their own servers, Gunkly must be reachable on the public internet at that address. Without it the option cannot be turned on. While it is off, every `/oauth/...` address and the `/.well-known/oauth-...` documents answer 404 and nothing about OAuth is advertised.
2. **You:** in Settings, AI, press **Connect an assistant**, choose **ChatGPT** or **Claude (connector)** and copy the address shown (ending in `/mcp`).
3. In ChatGPT (Settings, Connectors, with Developer mode on if asked) or Claude (Settings, Connectors, Add custom connector), add a connector with that address. Leave any client ID or secret fields empty: the app registers itself.
4. The app sends you to Gunkly. Sign in if asked, read who is asking, choose the households and actions it may use (seeing chores is always on; "Mark chores done" and "Create and edit chores, manage areas" are separate boxes), and press **Allow**.

The consent page says **Unverified app**: any app can register under any name, and Gunkly cannot check who made it, so only approve a connection you just started yourself, and check the address it will send you back to (shown in large type; Gunkly adds a warning when you have never approved an app returning to that address). Gunkly asks you to confirm it is you before it lets you allow a connection, unless you confirmed it in the last 15 minutes: with your password (and your authenticator, emailed or recovery code if you use one), with **Confirm with a passkey** if you have a passkey, or, if your account signs in through single sign-on, with **Confirm with** your identity provider (so an account without a password can approve an app too); **Deny** never asks. The app then holds a token that lasts an hour and renews itself while you use it (a connection unused for 30 days, or older than 90 days, asks you to approve again). It works for the `/mcp` address only, never for the rest of Gunkly's API, and it follows your current role in each household.

Only apps that run on a server or on your device can connect: browser-based clients (web pages that would call Gunkly from the visitor's browser, which needs CORS) are not supported, because Gunkly sends no CORS headers.

Apps you approved are listed under **Connected apps** in Settings, AI, with **Disconnect**. Changing your password or removing a second factor disconnects all of them.

## Things to try

- "What chores are overdue?"
- "Mark 'Water the plants' as done"
- "What's coming up this week?"
- "Move the bins to every 3 weeks and remind me at 8" (needs the "edit" permission)
- "Archive the gutter chore", "Put 'Dishes' in the Kitchen area", "Add an area called Garage"

## Privacy

- The assistant can see the names, due dates, repeat rules and notes of chores in the households you chose, and who they are assigned to (only whether it is you). It cannot see email addresses, other members' details, or anything outside those households.
- What it reads is sent to the company that runs your assistant (for example Anthropic or Google), under their privacy terms. Choose read-only access if you are unsure.
- It can never change members, settings, notification channels or your account, and it cannot delete chores or areas. With the "Create and edit chores, manage areas" permission it can create, edit and archive chores and create or rename areas (archiving hides a chore and stops its reminders, and the assistant can restore it; areas can only be deleted in Gunkly). It never does more than you could yourself: a guest's access stays read-only.
- Everything it does is recorded in your household's activity log.

## Take access away

Open **Settings**, **Integrations**, find the entry under **Access tokens** (for example "Claude Code (MCP)") and press **Revoke**. It stops working at once. Changing your password or removing a second factor also revokes all access keys and disconnects all connected apps.

## Troubleshooting

- **401 or "unauthorized":** the key was revoked, expired (they last a year) or was copied incorrectly. Create new access.
- **"Origin not allowed":** open Gunkly from the address your administrator configured and make the setup with that address.
- **"Not found" or card says access is switched off:** the administrator turned AI assistant access off (Admin, Settings).
- **Claude Desktop shows no tools:** check that Node.js is installed (run `node --version` in a terminal), that the settings file is valid, and that you fully quit and reopened Claude Desktop.
- **Assistant says it cannot do something:** it may lack the permission (for example editing chores). Create new access with the permission ticked.
