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

Gunkly listens on port 4865. For anything beyond localhost, put a TLS reverse proxy in front and set `GUNKLY_TRUSTED_PROXIES` to its address (the admin page warns if you forget; see [Behind a reverse proxy](../README.md#behind-a-reverse-proxy)); Web Push and installing the app need HTTPS (or `localhost`).

## 2. Create the admin

On first start Gunkly prints a one-time setup code. Find it:

```sh
docker compose -f compose.sqlite.yml logs gunkly | grep setup_code
```

Open `GUNKLY_BASE_URL`. You land on the setup screen: enter the code, choose a username and password and create the instance admin. The code stops working once the account exists (restart the container for a new one if you missed it).

Optional: turn on authenticator-app two-factor under Account.

## 3. Create a household

Choose "New household" in the header (or the menu on a phone), give it a name and a time zone (it defaults to your browser's). Areas are off by default; to group chores by area, turn on "Group chores by area" in the household's Settings, General, where you can also add starter areas such as Kitchen and Bathroom. Reminder times use the household time zone.

To share it, open the household's part of Settings, People and create an invite link (choose a role: member, or guest for view-only). Send the link; the other person signs up or signs in and accepts it. Open sign-up is off by default on a self-hosted instance, so create their account under Administration, Users first, or turn sign-up on under Administration, Sign-up & sign-in.

## 4. Add chores

Use "Add" and either start from a template or fill in the form: name, assignees, first due date (and an area, if your household uses areas), how it repeats (every N days, weeks or months, or a calendar rule such as "every April and October") a reminder preset and the time of day to be reminded ("Remind me at"). Select two or more assignees and a "Take turns" option appears: the people take turns in the order shown (use the arrows to change it), only the person whose turn it is gets reminders and sees the chore under "Mine", and after someone completes it the turn passes to the next person. The chore page shows whose turn it is and lets you change it. "More options" holds notes, a link and the advanced settings. Completing a chore from Home schedules the next due date; Undo is shown right after.

## 5. Get reminders

New households remind on the due date at 09:00 ("Gentle"); change it per chore, or for the whole household in its Settings. Reminders are optional and each person chooses how to get theirs. Everyone, including non-technical family members, can do this in under a minute:

1. **On this phone or computer:** open Gunkly, and on Home tap "Turn on" in "Get reminders on this device?" (or Settings, Notifications, "This device", "Turn on reminders on this device"), then allow notifications. It needs HTTPS. On an iPhone or iPad, first open Gunkly in Safari, tap Share, choose "Add to Home Screen", then open Gunkly from the Home Screen and turn reminders on there.
2. **By email** (when the server sends email): Settings, Profile & email, add and confirm your email address, then switch on "Email me reminders". Each email has a link to mark the chore as done.
3. **For the whole household:** an owner or member can add a household channel in the household's part of Settings, Notifications, for example your family Telegram or Discord group; reminders then reach everyone without anyone setting anything up.

Advanced: Settings, Notifications, "Messaging apps (advanced)" holds personal channels. Choose "Add channel", pick a service (Discord, Slack, Telegram, Pushover, Pushbullet, Google Chat, ntfy.sh; the admin can enable more) and paste its [Shoutrrr](https://containrrr.dev/shoutrrr/) URL. Use "Send test" to check it. The URL is stored encrypted and never shown again.

Set quiet hours, the household default preset and per-chore presets as needed. Reminder messages contain chore names and dates, so only use services you trust.

Optional: email (verification, password reset, email codes). Set `GUNKLY_EMAIL_ENABLED=true` with the `GUNKLY_SMTP_*` settings and restart.

## 6. Install the app (PWA)

Open Gunkly over HTTPS in a supported browser and use its "Install" or "Add to Home Screen" option. The installed app has no offline mode.

## 7. Subscribe to the calendar feed

Open the household's part of Settings, Connections, "Calendar feeds", "Create feed" and copy the address (also reachable from "Subscribe from your own calendar app" on the Calendar screen). Add it as a subscription calendar in Google Calendar, Apple Calendar or Outlook. The feed is read-only and lists chore names, dates and, if areas are on, areas only. "New address" replaces the link and "Revoke" turns it off.

## Next steps

- Connect an AI assistant such as Claude: [AI assistants](ai-assistants.md).
- Back up: `docker compose -f compose.sqlite.yml exec gunkly /gunkly backup - > backup.db` (see [Backups](../README.md#backups)).
- Monitoring and upgrades: [README](../README.md).
