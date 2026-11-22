# Google Drive: create the Google access

Users of your Fylvo can connect their Google Drive and see it as a folder next to their own
files. For this, you as the operator create one access (an OAuth client) at Google, once per
installation, and enter it in Fylvo. It takes about 15 minutes.

Checked against Google's pages on 2026-11-22. Google renames menus now and then; if a label
differs, look for the closest match. Steps marked *not confirmed* could not be checked on an
official page.

## Before you start

- A Google account. It becomes the owner of the access; any account works.
- Your Fylvo runs with https under a domain name, for example `https://cloud.example.com`.
  Google accepts no plain `http` and no IP address such as `192.168.1.10` as return address.
- Your return address (redirect URI). It is your Fylvo address followed by
  `/api/v4/fylvo/connected/callback`:

  ```
  https://cloud.example.com/api/v4/fylvo/connected/callback
  ```

  Fylvo also shows it in the admin panel, ready to copy.

**Cost:** nothing. No credit card, no billing account. Google plans to charge for use far
above the free Drive API quota from later in 2026 (with 90 days notice); a private
installation stays far below it.

## 1. Create a project

1. Open [console.cloud.google.com](https://console.cloud.google.com) and sign in.
2. Click the project picker at the top, then **New project**.
3. **Project name:** `Fylvo`. **Location** (parent resource): **No organization**.
   Click **Create**.
4. Make sure the new project is selected in the project picker at the top.

## 2. Turn on the Google Drive API

1. Menu (top left) > **APIs & Services** > **Library**.
2. Search for `Google Drive API`, open it, click **Enable**.

You land on the API's overview page. Without this step, Google offers the Drive scope in
step 4 not at all.

## 3. Set up the consent screen

1. Menu > **Google Auth Platform** (or open
   [console.cloud.google.com/auth/overview](https://console.cloud.google.com/auth/overview)).
   Click **Get started**.
2. **App information:** app name `Fylvo` (your users see it when they connect), **User
   support email:** your address. **Next**.
3. **Audience:** **External**. **Next**.
   - Only you will ever connect? **Personal mode** is the better choice: no warning page, no
     user limit, but only the Google account that created the access can connect. You cannot
     switch between Personal and External later.
   - **Internal** exists only for Google Workspace organisations.
4. **Contact information:** your address. **Next**.
5. Accept the Google API services user data policy, click **Continue**, then **Create**.

You see the Google Auth Platform overview with the sections **Branding**, **Audience**,
**Clients** and **Data Access** in the menu on the left.

Optional, under **Branding**: home page and privacy policy links. Google needs them only to
verify an app, which you do not need. If Google refuses to publish in step 5 without them,
enter your Fylvo address for both. *Not confirmed whether Google requires them before
publishing.*

## 4. Allow access to Drive

1. **Data Access** > **Add or remove scopes**.
2. Find `.../auth/drive` ("See, edit, create and delete all of your Google Drive files"), tick
   it. If the list does not show it, paste `https://www.googleapis.com/auth/drive` into
   **Manually add scopes** and click **Add to table**.
3. Click **Update**, then **Save** at the bottom of the page.

The scope now appears under **Your restricted scopes**. Fylvo needs this full scope: the
narrower Drive scopes only show files that Fylvo created itself, not your existing drive.

## 5. Publish the app

1. **Audience** > under **Publishing status** click **Publish app**, and confirm.
2. The status reads **In production**.

Do not stay in **Testing**: there, Google ends every connection after 7 days, and only test
users you list by hand can connect. Google shows that verification is needed. Ignore it; the
app works without verification (see [The warning page](#the-warning-page)). Skip this step
in Personal mode.

## 6. Create the client

1. **Clients** > **Create client**.
2. **Application type:** **Web application**. **Name:** `Fylvo`.
3. **Authorized JavaScript origins:** leave empty.
4. **Authorized redirect URIs** > **Add URI**: your return address, exactly as above.
5. Click **Create**.

A window shows the **Client ID** (ends in `.apps.googleusercontent.com`) and the **Client
secret** (starts with `GOCSPX-`). Copy both now or click **Download JSON**: Google shows the
secret only this once. Keep it like a password.

## 7. Enter it in Fylvo

1. Sign in to Fylvo as admin, open the admin panel, section **Connected storage**.
2. Under **Google Drive**, paste the client ID and the client secret, and save.

To test, open your own settings, tab **Connected storage**, and connect a Google account.

## The warning page

Your app is not verified by Google, so every user sees a page "Google hasn't verified this
app" once, before they allow access. It means only that Google has not checked who runs the
app. The user clicks **Advanced** > **Go to Fylvo (unsafe)** and then allows access.
*The labels on this page are not confirmed on an official Google page.*

Limits of an unverified app:

- At most **100 users** in total, for the life of the project. Users who disconnect still
  count; the number cannot be reset.
- Connections do not expire by themselves. They end when the user removes the access, when
  the secret is deleted, or after 6 months without use.

Verification removes these limits, but for the Drive scope Google requires a paid security
audit. Not worth it for a private installation.

## Renew the secret

The secret does not expire. Renew it when it may have leaked:

1. **Clients** > your client > **Add secret**. Copy the new secret (shown only once).
2. Enter it in Fylvo (step 7).
3. Back at Google, **Disable** the old secret, then delete it.

Existing connections keep working.

## Remove the access again

- **One user** disconnects in Fylvo. This also removes the access at Google. A user can
  also remove it in their Google account:
  [myaccount.google.com/linkedapps](https://myaccount.google.com/linkedapps) > Fylvo >
  **See details** > **Remove access**.
- **The whole access:** remove the values in Fylvo, then at Google **Clients** > tick the
  client > **Delete**. To remove everything, **IAM & Admin** > **Settings** > **Shut down**
  the project; Google deletes it after 30 days.

Google also deletes a client by itself after 6 months without any use, and sends an email
30 days before. You can restore it within about 30 days.

## Common errors

| What you see | Cause | Fix |
| --- | --- | --- |
| `Error 400: redirect_uri_mismatch` | The return address at Google differs from the one Fylvo uses, even by `http`/`https`, `www` or a trailing `/` | Copy the address from the Fylvo admin panel into the client (step 6). Changes take a few minutes. |
| "Access blocked: Fylvo has not completed the Google verification process" | App is still in **Testing** and the user is not a test user | Publish the app (step 5) |
| Connections stop working after 7 days | App was in **Testing** when the user connected | Publish the app, then let users reconnect |
| `Error 401: invalid_client` | Client ID or secret wrong or deleted | Enter both again, or create a new secret |
| Fylvo reports that Drive is not enabled, or `accessNotConfigured` | Step 2 skipped | Enable the Google Drive API in the same project |
| `Error 403: access_denied` | The user clicked **Cancel**, or in Personal mode another account tried to connect | Try again with the right account |
| `invalid_grant` when Fylvo uses a connection | The user removed the access at Google, or it was unused for 6 months | Reconnect in Fylvo |
| Only a few files or no folders are visible | Wrong scope, for example `drive.file` | Add the full `drive` scope (step 4) and reconnect |
| "This app has reached its user limit" *(not confirmed)* | 100 users reached | Create a new project, or verify the app |
