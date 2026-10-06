#!/usr/bin/env python3
"""Write contrib/desktop/karabiner-rule.json: tap Option, then B / W / N runs plash-mode.

Edit the settings here and run `python3 contrib/desktop/karabiner-rule.py [path/to/plash-mode]`, then
merge the JSON into ~/.config/karabiner/karabiner.json (contrib/desktop/README.md).
The path of plash-mode is worked out at run time (the plash-mode next to this file,
or the path given as the first argument), so no user name is stored in the repo.
The generated karabiner-rule.json is machine specific and git-ignored.

How the rule works:
- Tapping Option alone sets the variable drashta_leader to 1. A delayed action sets
  it back to 0 after DELAY_MS if no key is pressed.
- B, W or N (no modifier) while it is 1 runs plash-mode and clears it.
- Any other key while it is 1 clears it and still types normally. Without this, a
  stray Option tap left the flag set and a later B/W/N misfired. `to_if_canceled`
  cannot do this job: Karabiner runs it before the letter is matched, so it broke
  the letters too (tried 2026-10-01).
"""
import json
import os
import shlex
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.abspath(os.path.expanduser(sys.argv[1])) if len(sys.argv) > 1 else os.path.join(HERE, "plash-mode")
MODES = {"b": "browse", "w": "wallpaper", "n": "blank"}
LEADERS = ["left_option", "right_option"]
TAP_MS = 500     # how long Option may be held and still count as a tap
DELAY_MS = 1000  # how long after the tap the letter is accepted

# Every key that should clear a pending tap (B, W, N included: with a modifier they
# fall through to here). Modifiers are left out: they do not count as "another key".
OTHER_KEYS = (
    [chr(c) for c in range(ord("a"), ord("z") + 1)]
    + list("1234567890")
    + ["hyphen", "equal_sign", "open_bracket", "close_bracket", "backslash",
       "semicolon", "quote", "grave_accent_and_tilde", "comma", "period", "slash",
       "non_us_backslash", "spacebar", "return_or_enter", "tab", "delete_or_backspace",
       "delete_forward", "escape", "left_arrow", "right_arrow", "up_arrow", "down_arrow",
       "home", "end", "page_up", "page_down"]
    + [f"f{i}" for i in range(1, 13)]
)
BUTTONS = ["button1", "button2", "button3"]

IS_ON = [{"type": "variable_if", "name": "drashta_leader", "value": 1}]
OFF = {"set_variable": {"name": "drashta_leader", "value": 0}}
ANY = {"optional": ["any"]}


def mode(key, name):
    return {"type": "basic", "conditions": IS_ON, "from": {"key_code": key},
            "to": [OFF, {"shell_command": f"{shlex.quote(SCRIPT)} {name}"}]}


def clear(kind, key):
    return {"type": "basic", "conditions": IS_ON, "from": {kind: key, "modifiers": ANY},
            "to": [OFF, {kind: key}]}


def leader(key):
    return {"type": "basic", "from": {"key_code": key, "modifiers": ANY},
            "to": [{"key_code": key}],
            "to_if_alone": [{"set_variable": {"name": "drashta_leader", "value": 1}}],
            "to_delayed_action": {"to_if_invoked": [OFF]},
            "parameters": {"basic.to_if_alone_timeout_milliseconds": TAP_MS,
                           "basic.to_delayed_action_delay_milliseconds": DELAY_MS}}


rule = {
    # The merge snippet finds the old copy by this prefix: keep "drashta desktop".
    "description": "drashta desktop: tap Option, then B browse / W wallpaper / N normal wallpaper (within 1 s)",
    # Order matters: Karabiner uses the first manipulator that matches.
    "manipulators": [mode(k, n) for k, n in MODES.items()]
    + [clear("key_code", k) for k in OTHER_KEYS]
    + [clear("pointing_button", b) for b in BUTTONS]
    + [leader(k) for k in LEADERS],
}

out = os.path.join(HERE, "karabiner-rule.json")
with open(out, "w") as f:
    json.dump(rule, f, indent=2)
    f.write("\n")
print(out, len(rule["manipulators"]), "manipulators")
