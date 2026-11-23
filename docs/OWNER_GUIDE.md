# RivoSnap owner guide

Start with **one group and one test photo**. Setup is complete when you can open that photo in both RivoSnap’s private Gallery and the group’s album in your own Google Photos account.

## Before you start

Throughout this guide, `APP_URL` means the public HTTPS address your administrator chose. Substitute that address wherever you see `${APP_URL}`; do not paste the placeholder into a browser or provider console.

- **If you manage the server:** follow the [deployment guide](DOCKER_DEPLOYMENT.md) to start the services and configure DNS and HTTPS. Follow [Google Cloud setup](GOOGLE_CLOUD_SETUP.md) to enable the Photos Library API, create a web OAuth client and configure its credentials on the server. Register this exact Google redirect URI: `${APP_URL}/api/v1/google/callback`.
- **If someone else manages the server:** ask them to complete those steps and give you the owner setup key privately. You do not need Docker access or the Google client secret to use the gallery.
- Have access to your personal Google Photos account, your LINE Official Account’s Messaging API channel, and a group whose participants agree to collection.

## Set up your first group

After sign-in, follow **Dashboard → Setup pipeline**. To revisit the checklist after your first sync, open `${APP_URL}/?setup`.

1. **Claim your account.** Open your deployment’s `APP_URL`. Enter your email, a password of at least 12 characters, and the setup key (`BOOTSTRAP_SECRET`). Select **Create owner account**. If an owner already exists, use **Sign in** instead. Use the HTTPS address even on your LAN; the published HTTP port (`WEB_PORT`, default `8080`) is for the reverse proxy.
2. **Connect LINE.** In LINE Developers, open the Messaging API channel for your Official Account. Allow the bot to join group chats, issue a channel access token, and get the channel secret from **Basic settings**. In RivoSnap, open **Settings → LINE Messaging API**, enter both values, and select **Save credentials**. If Settings already shows **Configured**, skip entering credentials (use **Update credentials** only when rotating tokens) and verify the webhook in the next step. If you do not have a channel yet, start with [LINE setup](LINE_SETUP.md).
3. **Verify delivery.** In LINE Developers, set the webhook URL to `${APP_URL}/api/webhooks/line`. Select **Verify**, then enable **Use webhook** and **Webhook redelivery**. Expect **Verify** to succeed. An empty verification request does not register a group, so the dashboard webhook step can remain incomplete until RivoSnap discovers one.
4. **Connect Google.** In RivoSnap **Settings → Google Photos**, select **Connect Google Photos**. Choose the personal account that should own the albums and grant all requested permissions. After returning to RivoSnap, select **Validate connection** and check for **CONNECTED**. If Google says the app is in testing, ask the server administrator to add your account as a test user.
5. **Authorize the group.** Tell participants where their photos will be stored, then invite the Official Account to the LINE group. In RivoSnap, open **LINE groups**, find that group, and select **Enable collection**. Confirm the group shows **COLLECTING**. If it shows **LEFT**, invite the bot back before enabling it. Repeat for each group you want to collect from.
6. **Send and verify one photo.** Send a **new image message** to the enabled group. Open **Upload queue** to follow unfinished work. A fast upload may leave the queue before you see it. Open **Gallery**, filter by group and **SYNCED**, and open the thumbnail. In Google Photos, confirm the same image is in **LINE - {Group Name}** (or **LINE - Group {short ID}** if the name was unavailable). The first upload creates the album; inviting the bot alone does not. Old messages and direct messages are not imported; videos are skipped unless **Collect videos** is enabled in **Settings → LINE Messaging API**.

## Secure your owner account

Open **Profile** (on mobile: **More → Profile**). The two sign-in methods are independent:

- **Authenticator app:** select **Enable authenticator**, verify your identity, then scan the QR code in your authenticator or enter the manual key. Enter its six-digit code, save the eight recovery codes somewhere private and offline, and confirm. Your password now requires a fresh authenticator code or one unused recovery code. If you just used a code, wait for the next one.
- **Passwordless passkey:** select **Add passkey**, give the device a recognizable name, verify your identity, and confirm using the device prompt. Next time select **Sign in with a passkey** before entering any email or password. A passkey does not enable MFA for password login. If you cancel the prompt, you can still choose the password form.
- **Lost device:** use another registered passkey, or your password plus a recovery code if authenticator MFA is enabled. Remove the lost passkey from Profile. Use **Replace recovery codes** to invalidate the old set only after you save and confirm its replacement. To replace an authenticator, use **Replace authenticator**. Your existing app and recovery codes remain active until the replacement is confirmed. The initial setup key cannot unlock an existing account.

Security changes require fresh verification and sign out all sessions. Keep an independent sign-in method before removing a device. Use the same public HTTPS hostname across devices.

## Manage collection

| When you want to… | What to do |
|---|---|
| Check that collection is working | Open **Dashboard** for synced, pending and failed counts and storage warnings. If work stops progressing, check **System health** in the sidebar (or tap **Health** in the mobile top bar / **More → System health**) to verify Database, Redis, Worker and Disk. |
| Pause or resume a group | In **LINE groups**, select **Pause collection** or **Enable collection**. Pausing stops new ingestion; it does not cancel accepted uploads or delete photos. Photos sent while paused are not backfilled. |
| Refresh a renamed LINE group | In **LINE groups**, select **Refresh name** on the group card. RivoSnap fetches the latest group title from LINE and updates the mapped app-created Google Photos album title (`LINE - {Group Name}`). |
| Find a photo | Use **Albums** to browse by group or **Gallery** to filter by group, date or upload status. Open a thumbnail for the larger view. Dates use Bangkok time. |
| Restore Google access | In **Settings**, select **Reconnect** with the **same Google account**, then select **Validate connection**. Return to **Upload queue** to retry failed items. Disconnecting does not delete Google Photos originals. |
| Change local retention | In **Settings**, adjust the retention periods and select **Save retention**. By default, synced originals are removed locally after 24 hours, thumbnails expire after 30 days, and failed or uncertain originals reach a review deadline after 7 days. The deadline does **not** automatically delete them. |

## If a photo does not appear

1. **No queue entry and no gallery image:** confirm the group was enabled **before** the photo was sent. Check webhook verification and send one new test image.
2. **RETRY_PENDING:** wait for the scheduled retry. **FAILED:** read the error, fix its cause, then select **Retry upload**. A full album will not roll over to a second album automatically.
3. **NEEDS_REVIEW:** Google may already have created the photo. Select **Reconcile with Google** to search the mapped album without uploading again. Do not resend the photo merely to clear this state.
4. **UNRECOVERABLE:** read the reason. If LINE content expired, send a new copy. If the format or size was rejected, send a supported image within the configured limit rather than retrying the rejected file.
5. **Disk warning:** resolve stalled uploads or ask the administrator to expand storage. **Discard expired original** is irreversible: if Google has no copy, the photo may be lost. Do not use discard as a retry step.

For expired LINE content, uncertain album creation, proxy errors or recovery procedures, use [Troubleshooting](TROUBLESHOOTING.md). Never include passwords, setup keys or provider tokens in a support request.
