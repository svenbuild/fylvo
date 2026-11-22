# OneDrive: create the Microsoft access

Users of your Fylvo can connect their OneDrive and see it as a folder next to their own files.
For this, you as the operator register Fylvo once at Microsoft (an app registration) and enter
its ID and secret in Fylvo. It takes about 30 minutes, most of it for the Azure sign-up.

Checked against Microsoft's pages on 2026-11-22. Microsoft renames menus now and then; if a
label differs, look for the closest match. Steps marked *not confirmed* could not be checked
on an official page.

## Before you start

- Your Fylvo runs with https under a domain name, for example `https://cloud.example.com`.
  Microsoft accepts plain `http` only for `localhost`.
- Your return address (redirect URI). It is your Fylvo address followed by
  `/api/v4/fylvo/connected/callback`:

  ```
  https://cloud.example.com/api/v4/fylvo/connected/callback
  ```

  Fylvo also shows it in the admin panel, ready to copy.
- The Fylvo server finds rclone (version 1.74.3 or newer): it uses `rclone` from `PATH`, or
  the program named in the environment variable `FYLVO_RCLONE_PATH`. Without it, the admin
  panel shows connected storage as not available. If you limit the memory of the container,
  allow 640 MB instead of 512 MB: rclone needs about 65 to 130 MB while it is in use.
- A Microsoft directory (Entra ID tenant). Since June 2024, Microsoft only accepts new app
  registrations inside a directory; a plain personal Microsoft account is not enough. If you
  have none, step 1 creates one.

**Cost:** the app registration is free. The Azure free account in step 1 asks for a phone
number, an address and a credit card. Microsoft books a temporary authorization of about
1 USD that is given back, and charges nothing unless you upgrade to pay-as-you-go yourself.
After 30 days the Azure services of the trial switch off. The directory and the app
registration are not Azure services and should keep working. *Not confirmed on an official
page.* Microsoft may block directories that stay unused for a long time.

## 1. Get a directory

Skip this if you already manage a Microsoft Entra directory (for example through Microsoft 365
for business, or an earlier Azure account) and may register apps in it. The free account is
only for new Azure customers.

1. Open [azure.microsoft.com/free](https://azure.microsoft.com/free) and click **Try Azure for
   free** (the button name changes now and then).
2. Sign in with a Microsoft account, or create one (for example `user@example.com`).
3. Enter and confirm your phone number, enter your address and a credit card, accept the
   terms, finish.

Azure creates a directory named **Default Directory** for you, with you as its admin.

## 2. Register the app

1. Open [entra.microsoft.com](https://entra.microsoft.com) and sign in with the same account.
   Microsoft asks for a second sign-in factor here (for example the Microsoft Authenticator
   app); set it up when asked. If you have several directories, pick the right one with the **Settings** icon (gear) at the
   top.
2. Menu on the left: **Entra ID** > **App registrations** > **New registration**.
   (In the Azure portal: **Microsoft Entra ID** > **App registrations**.)
3. **Name:** `Fylvo` (your users see it when they connect).
4. **Supported account types:** **Any Entra ID Tenant + Personal Microsoft accounts**.
   In older wording: "Accounts in any organizational directory (Any Microsoft Entra ID tenant -
   Multitenant) and personal Microsoft accounts". This is the only choice that lets both
   private OneDrive accounts and work or school accounts connect.
5. Click **Register**.

You see the app's **Overview**. Copy the **Application (client) ID**: this is the client ID for
Fylvo.

## 3. Add the return address

1. In the app: **Manage** > **Authentication**.
2. Tab **Redirect URI configuration** > **Add Redirect URI** > tile **Web**.
3. **Redirect URI:** your return address, exactly as above. Click **Configure**.

The address is listed under **Web**. Leave "Allow public client flows" and the implicit grant
(access tokens, ID tokens) off.

## 4. Create the secret

1. **Manage** > **Certificates & secrets** > tab **Client secrets** > **New client secret**.
2. **Description:** `Fylvo`. **Expires:** at most 24 months. Microsoft recommends less than
   12; a longer time means renewing less often.
3. Click **Add**.
4. Copy the **Value** at once. Not the **Secret ID** next to it. Microsoft shows the value only
   now; after leaving the page you see just its first characters.

Note the expiry date shown in the list in your calendar, a few weeks earlier. See
[Renew the secret](#renew-the-secret).

## 5. Add the permissions

1. **Manage** > **API permissions**. `User.Read` is already listed.
2. **Add a permission** > **Microsoft Graph** > **Delegated permissions**.
3. Search and tick `Files.ReadWrite` and `offline_access`. Click **Add permissions**.

The list now shows `Files.ReadWrite`, `offline_access` and `User.Read`, all of type
**Delegated**. You do not need **Grant admin consent**: each user approves these permissions
when connecting, as far as their organisation allows it (see below). Do not add the `.All`
permissions such as `Files.ReadWrite.All`: Fylvo does not need them, and since 2025
Microsoft's default settings let only an admin approve them.

## 6. Enter it in Fylvo

1. Sign in to Fylvo as admin, open the admin panel, section **Connected storage**.
2. Under **OneDrive**, paste the client ID (step 2) and the secret value (step 4), and save.

To test, open your own settings, tab **Connected storage**, and connect a Microsoft account.

## What users see

A Microsoft sign-in page, then a list of what Fylvo may do: sign in and read the profile, full
access to the user's files, and keep this access. The user clicks **Accept**. The app is marked
as unverified: Microsoft only verifies publishers who are members of its partner program,
which a private operator is not.

- **Personal accounts** (outlook.com, hotmail.com, live.com) are supported.
- **Work or school accounts** of other organisations: many organisations let users approve
  only verified apps, or none at all, and Microsoft's default settings can also ask for an
  admin for a new unverified app. Then the user sees "Need admin approval" and their IT has
  to approve Fylvo in their directory. There is nothing you can change in Fylvo for this.

A connection ends after 90 days without any use (each use renews it); the user then
reconnects.

## Renew the secret

The secret runs out after the time you chose, at most 24 months. Then every OneDrive
connection stops working until a new secret is in Fylvo. Renew it before:

1. **Certificates & secrets** > **New client secret**, as in step 4. Copy the new value.
2. Enter it in Fylvo (step 6). Connect a Microsoft account anew in Fylvo, to test the new
   secret.
3. Back at Microsoft, delete the old secret (bin icon in its row).

Existing connections keep working, users do not need to reconnect.

## Remove the access again

- **One user** disconnects in Fylvo. Fylvo deletes its key at once, but Microsoft has no way
  for Fylvo to remove its access there: Fylvo stays listed in the user's account until the
  user removes it:
  - personal account: [account.microsoft.com/privacy/app-access](https://account.microsoft.com/privacy/app-access)
    > Fylvo > **Edit** > **Remove these permissions** (on newer pages **Details** > **Don't
    allow**). *Labels not confirmed.*
  - work or school account: [myapplications.microsoft.com](https://myapplications.microsoft.com)
    > point at the Fylvo tile > **...** > **Manage your application** > **Revoke Permissions**.
    If there is no Fylvo tile, or an admin approved Fylvo for the organisation, only that
    admin can remove it.
- **The whole access:** remove the values in Fylvo, then at Microsoft **App registrations** >
  Fylvo > **Delete**. For 30 days you can still restore it under the tab **Deleted
  applications**. Organisations whose admin approved Fylvo keep an entry for it in their own
  directory; only their admin can remove it.

## Common errors

| What you see | Cause | Fix |
| --- | --- | --- |
| `AADSTS50011` "The redirect URI ... does not match" | The return address at Microsoft differs from the one Fylvo uses, even by `http`/`https`, `www` or a trailing `/` | Copy the address from the Fylvo admin panel into **Authentication** (step 3) |
| `AADSTS50194` | The app accepts only accounts of your own directory | Change the app's supported account types to **Any Entra ID Tenant + Personal Microsoft accounts** ([how](https://learn.microsoft.com/en-us/entra/identity-platform/howto-modify-supported-accounts)), or register a new app (step 2) and enter its values |
| `AADSTS50020` | The browser signed in with another account than intended, or the account type is wrong | Try again in a private window with the right account. If that fails, see `AADSTS50194`. |
| `AADSTS7000215` "Invalid client secret" | The **Secret ID** was entered instead of the **Value**, or a typo | Create a new secret and enter its value (step 4) |
| `AADSTS7000222` "client secret keys ... are expired" | Secret older than its expiry date | [Renew the secret](#renew-the-secret) |
| `AADSTS65001` | The user has not approved Fylvo, or their organisation requires an admin | Reconnect and click **Accept**; for work accounts the IT may have to approve |
| "Need admin approval" | Work account whose organisation blocks user approval | The user's IT has to approve Fylvo, or the user connects a personal account |
| `AADSTS700016` "Application ... was not found" | Client ID wrong, or app deleted | Copy the **Application (client) ID** again, or restore the app |
| `AADSTS70008`, `AADSTS700082` or `invalid_grant` when Fylvo uses a connection | Unused for 90 days, sessions revoked by the user or an admin, or access removed | Reconnect in Fylvo |
