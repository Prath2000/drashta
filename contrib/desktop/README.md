# contrib/desktop: Plash desktop modes (optional, personal setup)

This is an optional extra. drashta works fully without it.

## What it is

- Three exact modes for the drashta wallpaper, driven by keys or by the buttons in drashta's top bar:
  - `browse`: drashta on the desktop, clickable (Plash Browsing Mode on).
  - `wallpaper`: drashta as a quiet read-only readout (Browsing Mode off).
  - `blank`: an empty transparent page (`/blank`), so the normal macOS wallpaper shows.
- Files:
  - `plash-mode`: the script. Reads Plash's saved state and toggles only what differs. Logs to `~/.drashta/plash-mode.log` and writes the last mode to `~/.drashta/plash-mode.state`.
  - `karabiner-rule.py`: generates `karabiner-rule.json` (tap Option, then B / W / N). The JSON holds a machine-specific path, so it is generated, git-ignored, and not stored in the repo.

## Warnings

- macOS only. It needs [Plash](https://sindresorhus.com/plash) and, for the keys, [Karabiner-Elements](https://karabiner-elements.pqrs.org/).
- It is a personal setup. It assumes Plash has exactly two websites: `drashta` (`http://localhost:4455/?desktop=1`, first in the list) and `blank` (`http://localhost:4455/blank`). It was written for one Mac and may need edits for yours.
- Karabiner owns the keyboard while it runs. macOS modifier-key remaps (System Settings, Keyboard, Modifier Keys) stop applying, so re-create any such remap (for example Caps Lock to Escape) inside Karabiner.

## Turn it on

1. Add the two Plash websites listed above.
2. Set the script in `~/.drashta/config.toml` (it must be inside `projects_dir`), then restart drashta:
   ```toml
   desktop_script = "~/path/to/drashta/contrib/desktop/plash-mode"
   ```
   The top-bar mode buttons appear only when `desktop_script` is set and valid.
3. Optional keys. From the repo root:
   ```bash
   python3 contrib/desktop/karabiner-rule.py      # writes contrib/desktop/karabiner-rule.json with your real path
   python3 - <<'PY'
   import json, os
   p = os.path.expanduser("~/.config/karabiner/karabiner.json")
   rule = json.load(open("contrib/desktop/karabiner-rule.json"))
   d = json.load(open(p))
   rules = d["profiles"][0]["complex_modifications"]["rules"]
   rules[:] = [r for r in rules if not r.get("description", "").startswith("drashta desktop")]
   rules.append(rule)
   json.dump(d, open(p, "w"), indent=2, ensure_ascii=False)
   PY
   ```
   Karabiner reloads the file by itself. Pass a path to `karabiner-rule.py` to point the rule at a copy of `plash-mode` elsewhere. Timing and letters are settings at the top of `karabiner-rule.py`.

## Use

- Tap Option (alone, under 0.5 s), then press B, W or N within 1 s.
- Or click the buttons in the top bar.
- Or run the script: `contrib/desktop/plash-mode browse|wallpaper|blank`.

## Undo

- Stop the keys only: in Karabiner-Elements, Complex Modifications, remove the rule "drashta desktop: tap Option ...", or delete it from `~/.config/karabiner/karabiner.json`.
- Give the keyboard back to macOS: quit Karabiner-Elements.
- Hide the buttons: remove `desktop_script` from `~/.drashta/config.toml` and restart drashta.
