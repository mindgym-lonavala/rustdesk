# Manashakti Support

Manashakti Support is the remote IT support tool of Manashakti Research Centre (Lonavala, India),
used by its IT staff to help the organisation's own computers and the people who ask them for help.
It is built from this fork of [RustDesk](https://github.com/rustdesk/rustdesk) and runs entirely on
the organisation's own server; no third-party relay is used.

Everything here is open source:

| Component | Path | License | Output |
|---|---|---|---|
| Branded Windows client (RustDesk fork) | repository root, `branding/manashakti/` | AGPL-3.0 | `ManashaktiSupport-<version>-x86_64.exe` (portable), `.msi` (installer) |
| Invite service and setup helper | [`manashakti/invite/`](invite/) | MIT | `invite-server.exe`, `invite-helper.exe` |

Builds run only on GitHub Actions from this repository
([`manashakti-windows.yml`](../.github/workflows/manashakti-windows.yml),
[`manashakti-invite.yml`](../.github/workflows/manashakti-invite.yml)).

## What the programs do

- **Client**: remote desktop with end-to-end encryption. On the helped computer it always shows a
  window while someone is connected; attended sessions must be accepted by the user. Unattended
  access exists only after the computer's user or administrator explicitly installs it and
  confirms the Windows elevation prompt, and can be removed in Windows Settings → Apps.
- **Invite helper**: downloaded by a person from a one-time invite link sent by IT. It either starts
  the portable client for one help session, or (after the Windows elevation prompt) installs the
  client for permanent support and registers the computer with the organisation's server.
- **Invite service**: issues those one-time links (valid 24 hours) and adds registered computers to
  the IT team's address book.

## Code signing policy

Free code signing provided by [SignPath.io](https://about.signpath.io/), certificate by
[SignPath Foundation](https://signpath.org/).

- Only executables built by the GitHub Actions workflows above, from source in this repository,
  are signed. No locally built or third-party binaries are signed.
- Committers and reviewers: [mindgym-lonavala](https://github.com/mindgym-lonavala) (Manashakti Research Centre IT)
- Approvers: [mindgym-lonavala](https://github.com/mindgym-lonavala)
- Every signing request is approved manually by an approver.

### Privacy policy

These programs connect only to the server operated by Manashakti Research Centre
(`support.manashakti.org`), and only to provide the remote support the user or the computer's
administrator requested. They do not send data to any other networked system. The server stores
the computer's support ID, name and, for permanent access, its access password; screen content is
not recorded. Contact: developers@manashakti.org
