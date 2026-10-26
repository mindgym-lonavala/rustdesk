# Manashakti Support branding

Custom Windows client built from this fork by `.github/workflows/manashakti-windows.yml`
(Actions → "Manashakti Windows build" → Run workflow). Output: workflow artifact
`manashakti-support-windows-x86_64` with a portable `.exe` and an `.msi`.

## How it works

Uses RustDesk's built-in custom-client mechanism instead of patching code paths:

- `custom.txt` next to the exe is read at startup by `read_custom_client` (`src/common.rs`).
  It sets the app name, server address, server key and default settings.
- It must be signed by the key in `src/common.rs` (`const KEY`). That one line is the
  only source change; upstream's key was replaced with ours.
- The signed `custom.txt` is stored in repository secret `MANASHAKTI_CUSTOM_TXT`, not in git.
- `apply-branding.sh` swaps icons and Windows version-resource strings before the build.
- Logos/icons in `data/flutter_assets/assets/` are picked up by the UI at runtime.

App name is `ManashaktiSupport` (no space): it is also used for the install folder,
Windows service name and the `manashaktisupport://` URL scheme.

## Changing server, key or settings

```bash
python3 make_custom_txt.py sign config.json custom-client-signing.key > custom.txt
python3 make_custom_txt.py verify custom.txt <public key from src/common.rs>
```

Paste the content of `custom.txt` into the `MANASHAKTI_CUSTOM_TXT` secret and re-run the
workflow. `config.json` format:

```json
{
  "app-name": "ManashaktiSupport",
  "override-settings": {
    "custom-rendezvous-server": "<server>",
    "relay-server": "<server>",
    "key": "<id_ed25519.pub of the server>"
  },
  "default-settings": { "verification-method": "use-temporary-password" }
}
```

The signing private key is kept outside the repository (password manager). Losing it
only means generating a new one (`keygen`) and updating `const KEY` in `src/common.rs`.

## Assets

- `wordmark-white.png`: source logo (white on transparent)
- `icon-1024.png`, `icon.png`, `icon.ico`, `tray-icon.ico`: blue tile; sizes ≤32px use "म" only
- `logo_light.png` / `logo_dark.png`: header logo for light / dark theme

## Upstream merges

Conflicts are limited to `src/common.rs` (one line). After merging, re-check the env
versions and Windows x64 steps in `flutter-build.yml` against `manashakti-windows.yml`.
