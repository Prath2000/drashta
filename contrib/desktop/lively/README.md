# contrib/desktop/lively: drashta as a Windows wallpaper (Lively)

This is an optional extra, and Windows support is new and early. drashta works fully without it.

[Lively Wallpaper](https://github.com/rocksdanister/lively) is a free Windows app that can use a web page as your desktop wallpaper.

## Set it up

- Start drashta first (it must be running for the page to load). See "Start at login" below.
- Open Lively, click **Add wallpaper** (the `+` button), and enter the URL `http://localhost:4455/?desktop=1`. Confirm, then set it as your wallpaper.
- The `?desktop=1` part gives the quiet desktop layout.

## Wallpaper vs browse

- **Wallpaper (read-only):** Lively shows the page behind your windows and mouse clicks go to your desktop. This is drashta's quiet readout.
- **Browse (clickable):** turn on Lively's wallpaper input setting (mouse and keyboard input to the wallpaper), so you can click projects on the desktop. Lively's own settings control this; drashta does not.
- **Blank:** use the URL `http://localhost:4455/blank` for an empty transparent page.
- For the normal dashboard, open `http://localhost:4455/` in a browser as usual.

## What does not work on Windows

- The mode buttons in drashta's top bar and the `plash-mode` script are macOS-only. They drive Plash. To change mode on Windows, change the URL or input setting in Lively.
- Karabiner key shortcuts are macOS-only.

## Start at login (Task Scheduler)

Example, run from a normal PowerShell (adjust the path to your `drashta.exe`):

```powershell
schtasks /Create /TN "drashta" /TR "C:\tools\drashta\drashta.exe" /SC ONLOGON /RL LIMITED
```

- Remove it with `schtasks /Delete /TN "drashta" /F`.
- Task Scheduler may show a console window. If you do not want one, wrap the command in a shortcut or script that starts it hidden.

## Not verified

- These steps were written from Lively's documented behaviour. They have not been tested on a real Windows machine with Lively.
- Lively's menu names may differ between versions. If a label does not match, look for the equivalent setting.
- Whether the page keeps its read-only look under every Lively input setting is untested.
- The `schtasks` line is an untested example.
