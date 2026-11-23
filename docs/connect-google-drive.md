# Google Drive: create the Google access

Users of your Fylvo can connect their Google Drive and see it as a folder next to their own
files. For this, you as the operator create one access (an OAuth client) at Google, once per
installation, and enter it in Fylvo. It takes about 15 minutes.

This setup is meant for a private installation: fewer than 100 users, all people you know.
A public service open to anyone must have its app verified by Google first.

Checked against Google's pages on 2026-11-22. Google renames menus now and then; if a label
differs, look for the closest match. Steps marked *not confirmed* could not be checked on an
official page.

## Before you start

- A Google account that may create Google Cloud projects. A private account always may; a
  school or company account only if its admin allows it.
- Your Fylvo runs with https under a public domain name, for example `https://cloud.example.com`
  (see [self-hosting, step 1](self-hosting.md#1-https-with-a-reverse-proxy)).
  Google accepts no plain `http`, no IP address such as `192.168.1.10` and no internal name
  such as `nas.local`.
- A public web page on your domain, readable without signing in, that says what your Fylvo is
  and how it handles Drive data: it reads, changes and stores files only for the signed-in
  user and passes nothing on. Put your privacy policy on it, or link it from there. Google
  requires a home page and a privacy policy for every app in production; the Fylvo sign-in
  page alone does not count.
- Your return address (redirect URI). It is your Fylvo address followed by
  `/api/v4/fylvo/connected/callback`:

  ```
  https://cloud.example.com/api/v4/fylvo/connected/callback
  ```

  Fylvo also shows it in the admin panel, ready to copy (step 8).

**Cost:** standard use of the Google Drive API is free, no credit card or billing account is
needed. Google plans to charge for use above its free
[limits](https://developers.google.com/workspace/drive/api/guides/limits) from later in 2026,
with at least 90 days notice.

## 1. Create a project

1. Open [console.cloud.google.com](https://console.cloud.google.com) and sign in.
2. Click the project picker at the top, then **New project**.
3. **Project name:** `Fylvo`. **Location** (parent resource): **No organization** if offered;
   with a company account, the organization or folder your admin allows.
   Click **Create**.
4. Make sure the new project is selected in the project picker at the top.

## 2. Turn on the Google Drive API

1. Menu (top left) > **APIs & Services** > **Library**.
2. Search for `Google Drive API`, open it, click **Enable**.

You land on the API's overview page. Without this step the Drive scope is missing from the
list in step 5, and Fylvo gets the error `accessNotConfigured`.

## 3. Set up the consent screen

1. Menu > **Google Auth Platform** (or open
   [console.cloud.google.com/auth/overview](https://console.cloud.google.com/auth/overview)).
   Click **Get started**.
2. **App information:** app name `Fylvo`, **User support email:** your address. **Next**.
3. **Audience:** **External**. **Next**. (**Internal** exists only for Google Workspace
   organisations and lets only their own accounts in.)
4. **Contact information:** your address. **Next**.
5. Accept the Google API services user data policy, click **Continue**, then **Create**.

You see the Google Auth Platform overview with the sections **Branding**, **Audience**,
**Clients** and **Data Access** in the menu on the left.

## 4. Fill in the branding

1. **Branding**.
2. **Authorized domains** > **Add domain**: the registered domain of your Fylvo address, and
   of your privacy page if it is elsewhere. For `cloud.example.com` that is `example.com`.
   For a name under a shared suffix such as `name.duckdns.org`, enter the whole name.
3. **Application home page:** your public page, for example `https://example.com/fylvo`.
   **Application privacy policy link:** your privacy policy.
4. Click **Save**.

Without the domain, Google refuses the return address in step 7. Without the home page and
the privacy policy link, the **Publish app** button in step 6 stays grey: Google then says
that an app name, a support email and URLs for the home page and the privacy policy are
required, and sends you back to **Branding**.

## 5. Allow access to Drive

1. **Data Access** > **Add or remove scopes**.
2. Find `.../auth/drive` ("See, edit, create and delete all of your Google Drive files"), tick
   it. If the list does not show it, paste `https://www.googleapis.com/auth/drive` into
   **Manually add scopes** and click **Add to table**.
3. Click **Update**, then **Save** at the bottom of the page.

The scope now appears under **Your restricted scopes**. Fylvo needs this full scope to read
and change the files and folders that are already in the drive.

## 6. Publish the app

1. **Audience** > under **Publishing status** click **Publish app**, and confirm.
2. The status reads **In production**.

Do not stay in **Testing**: there, Google ends every Drive connection after 7 days, and only
test users you list by hand can connect. Google shows that verification is needed. For a
private installation you can leave it; the app works without verification (see
[The warning page](#the-warning-page)).

## 7. Create the client

1. **Clients** > **Create client**.
2. **Application type:** **Web application**. **Name:** `Fylvo`.
3. **Authorized JavaScript origins:** leave empty.
4. **Authorized redirect URIs** > **Add URI**: your return address, exactly as above.
5. Click **Create**.

A window shows the **Client ID** (ends in `.apps.googleusercontent.com`) and the **Client
secret** (starts with `GOCSPX-`). Copy both now: Google shows the secret only this once
(download it as JSON if the window offers it). Keep it like a password.

## 8. Enter it in Fylvo

1. Sign in to Fylvo as admin, open the admin panel, **Settings** > **Connected storages**. The
   **Redirect URI** shown there is the return address to enter at Google.
2. Under Google Drive, paste the **Client ID** and the **Client secret**, and click **Save**.
   The status reads **Ready**. Fylvo stores the secret encrypted and never shows it again; to
   change it, enter a new one.
3. **Groups** > edit each group whose members may connect a storage > switch on **Connect
   storages**.

To test, open your own **Settings** > **Connected storages** > **Connect storage** and choose
Google Drive. Sign in at Google and allow access. You come back to Fylvo, and the drive appears
as a folder next to your own files.

## The warning page

Your app is not verified by Google, so a user who connects sees the page "Google hasn't
verified this app": Fylvo asks for full access to the drive, and Google has not checked the
app. The user clicks **Advanced** > **Go to cloud.example.com (unsafe)** and then allows
access. Until an app is verified, Google shows its domain instead of the app name.

Limits of an unverified app:

- At most **100 users** in total, for the life of the project. Users who disconnect still
  count; the number cannot be reset. **Audience** shows how many are used.
- Connections do not end after 7 days as in **Testing**. They still end when the user removes
  the access at Google, after 6 months without use, or when a Workspace admin limits it; the
  user then reconnects in Fylvo.
- School and company accounts can be blocked by their admin, and accounts with Google's
  Advanced Protection cannot connect at all.

Verification lifts the user limit, but for the Drive scope it includes a security assessment.
Not worth it for a private installation.

## Renew the secret

The secret does not expire. Renew it when it may have leaked:

1. **Clients** > your client > **Add secret**. Copy the new secret (shown only once). A client
   can have at most two secrets.
2. Enter it in Fylvo (step 8). Connect a Google account anew in Fylvo, to test the new secret.
3. Back at Google, **Disable** the old secret, test once more, then delete it. Deleting cannot
   be undone.

Existing connections keep working.

## Remove the access again

- **One user** disconnects in Fylvo. Fylvo deletes its key at once and also removes the
  access at Google, unless another Fylvo account has the same Google account connected. A
  user can also remove it in their Google account:
  [myaccount.google.com/linkedapps](https://myaccount.google.com/linkedapps) > **Access to
  your Google Account** > Fylvo > **See details** > **Remove access** > **Confirm**.
- **The whole access:** remove the values in Fylvo, then at Google **Clients** > tick the
  client > **Delete**. To remove everything, **IAM & Admin** > **Settings** > **Shut down**
  the project; Google deletes it after 30 days.

Google also deletes a client by itself after 6 months without any use, and sends an email
before. You can restore it within about 30 days on the **Deleted credentials** page.

## Common errors

| What you see | Cause | Fix |
| --- | --- | --- |
| `Error 400: redirect_uri_mismatch` | The return address at Google differs from the one Fylvo uses, even by `http`/`https`, `www` or a trailing `/` | Copy the address from the Fylvo admin panel into the client (step 7). Changes can take from 5 minutes to a few hours. |
| **Publish app** is grey and cannot be clicked | Home page or privacy policy link missing under **Branding** | Step 4, then come back to step 6 |
| Google refuses the return address in step 7 | Domain not in **Authorized domains**, or no https | Step 4 |
| "Access blocked: Fylvo has not completed the Google verification process", `Error 403: access_denied` | App is still in **Testing** and the user is not a test user | Publish the app (step 6) |
| Drive connections stop working after 7 days | App was in **Testing** when the user connected | Publish the app, then let users reconnect |
| `Error 401: invalid_client`, or Fylvo says "The provider refused the setup of this server" | Client ID or secret wrong | Enter both again, or create a new secret |
| `deleted_client` | Client deleted, also automatically after 6 months without use | Restore it on the **Deleted credentials** page, or create a new client |
| Fylvo reports `accessNotConfigured` | Step 2 skipped | Enable the Google Drive API in the same project |
| Fylvo says "Access was not allowed at the provider" | The user clicked **Cancel** | Connect again |
| `admin_policy_enforced` | The admin of a school or company account blocks the app | The admin must allow Fylvo, or the user connects a private account |
| Fylvo shows **Reconnect needed** for a connection | The user removed the access at Google, or it was unused for 6 months | **Reconnect** in Fylvo |
| New users cannot connect, existing ones still can *(not confirmed)* | 100 users reached | Check the count under **Audience**. Only verification lifts the limit. |
