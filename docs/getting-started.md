# Getting started

A first-run walkthrough for a self-hoster. Reference material is in the [README](../README.md) and [`.env.example`](../.env.example).

## 1. Start the container

You need Docker with Compose and a copy of this repository (or just the compose file and `.env.example`).

```sh
cp .env.example .env
echo "GUNKLY_SECRET_KEYS=1:$(openssl rand -base64 32)"   # put this line in .env
```

Edit `.env`: set `GUNKLY_SECRET_KEYS` and `GUNKLY_BASE_URL` (the address you will open, `scheme://host[:port]`, no path). Back up the secret key; Gunkly will not start without it.

```sh
docker compose -f compose.sqlite.yml up -d      # or compose.postgres.yml (also set POSTGRES_PASSWORD)
```

Gunkly listens on port 8080. For anything beyond localhost, put a TLS reverse proxy in front and set `GUNKLY_TRUSTED_PROXIES` to its address; Web Push and installing the app need HTTPS (or `localhost`).

## 2. Create the admin

On first start Gunkly prints a one-time setup code. Find it:

```sh
docker compose -f compose.sqlite.yml logs gunkly | grep setup_code
```

Open `GUNKLY_BASE_URL`. You land on the setup screen: enter the code, choose a username and password and create the instance admin. The code stops working once the account exists (restart the container for a new one if you missed it).

Optional: turn on authenticator-app two-factor under Account.

## 3. Create a household

Choose "New household" in the header (or the menu on a phone), give it a name and a time zone (it defaults to your browser's) and keep "starter areas" if you want Kitchen, Bathroom and similar areas to begin with. Reminder times use the household time zone.

To share it, open the household's Settings and create an invite link (choose a role: member, or guest for view-only). Send the link; the other person signs up or signs in and accepts it. Open sign-up is off by default on a self-hosted instance, so create their account under Admin, Users first, or turn sign-up on in the admin settings.

## 4. Add chores

Use "Add" and either start from a template or fill in the form: name, area, assignees, first due date, how it repeats (every N days, weeks or months, or a calendar rule such as "every April and October") and a reminder preset. "More options" holds notes, a link and the advanced settings. Completing a chore from Home schedules the next due date; Undo is shown right after.

## 5. Connect a notification channel

Reminders are optional and go to services you choose.

- **Browser or phone (Web Push):** Account, "Notifications on this device", "Enable notifications on this device" (needs HTTPS and a browser with push support).
- **A messaging service:** Account (personal) or the household's Settings (shared), "Add channel", pick a service (Discord, Slack, Telegram, Pushover, Pushbullet, Google Chat, ntfy.sh; the admin can enable more) and paste its [Shoutrrr](https://containrrr.dev/shoutrrr/) URL. Use "Send test" to check it. The URL is stored encrypted and never shown again.

Then set a reminder preset on a chore, or a household default and quiet hours in the household's Settings. Reminder messages contain chore names and dates, so only use services you trust.

Optional: email (verification, password reset, email codes). Set `GUNKLY_EMAIL_ENABLED=true` with the `GUNKLY_SMTP_*` settings and restart.

## 6. Install the app (PWA)

Open Gunkly over HTTPS in a supported browser and use its "Install" or "Add to Home Screen" option. The installed app has no offline mode.

## 7. Subscribe to the calendar feed

Open the household's Settings, "Calendar feeds", "Create feed" and copy the address (also reachable from "Subscribe from your own calendar app" on the Calendar screen). Add it as a subscription calendar in Google Calendar, Apple Calendar or Outlook. The feed is read-only and lists chore names, areas and dates only. "New address" replaces the link and "Revoke" turns it off.

## Next steps

- Back up: `docker compose -f compose.sqlite.yml exec gunkly /gunkly backup - > backup.db` (see [Backups](../README.md#backups)).
- Monitoring and upgrades: [README](../README.md); rollout and restore: [operations.md](operations.md).
