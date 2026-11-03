# Counterbuild

A tiny click-through overlay for League of Legends that reads your live game and tells you what to build next against the enemy team you are actually facing.

- One 7 MB `.exe`. No installer, no Overwolf, no Electron, no runtime to install.
- About 20 to 30 MB of RAM and well under 1% CPU (one poll and a redraw of a few milliseconds per second).
- Updates itself to the current patch from Riot's Data Dragon.

## What it does

Every second it reads the official Live Client Data API that the game serves on `https://127.0.0.1:2999`, then:

1. **Profiles every enemy**: kit traits (healing, shields, crowd control, burst, auto-attack reliance, tankiness), what they have really bought (armor, magic resist, crit, lifesteal, AD vs AP), their level, and how fed they are. Fed enemies weigh more.
2. **Scores every completed item in the current patch** for your champion and build style:
   - stat values come from the shop's own prices (Long Sword, Amplifying Tome, Ruby Crystal, ...)
   - armor and magic resist are weighted by the enemy damage split, tenacity by enemy crowd control, lifesteal goes down when they own anti-heal
   - penetration uses the real damage formula against each enemy's current armor or magic resist, so it knows when lethality beats % penetration and the other way round
   - amplifiers (Rabadon's, crit damage) scale with what you already have
   - counter effects (anti-heal, anti-shield, anti-crit, stasis, spell shield, lifeline, cleanse, max-health damage, shred) count only as much as the enemy team needs them
3. **Shows the top 3 items with the reason for each**, a boots pick if you have none, and **exactly what to buy with the gold you have right now** (the most expensive useful component toward your next item, using what you already own).
4. **Flags urgent cheap counters**, for example Executioner's Calling or Oblivion Orb against heavy healing, Quicksilver Sash against suppression.
5. **Learns your builds**: it remembers which items you finish on each champion and leans toward them over time. You can also pin a core build per champion (see Settings).

## Requirements

- Windows 10 or 11, 64-bit.
- League set to **Borderless** (Settings, Video, Window Mode). No external overlay can draw over exclusive Fullscreen.

## Run

1. Unzip and run `Counterbuild.exe`. Windows SmartScreen may warn because the file is not code-signed: choose *More info*, then *Run anyway*. You can also build it yourself (see below).
2. A tray icon appears. Start a game and the panel appears on the right side of the screen.

| Action | How |
| --- | --- |
| Show or hide | `Ctrl+Shift+B`, or left-click the tray icon |
| Position, size, start with Windows, exit | Right-click the tray icon |
| Settings file | Tray menu, *Edit settings...* |

The panel is click-through and never takes focus, so it cannot get in the way of your clicks. By default it only shows while the game window is focused.

## Reading the panel

- **Header**: your gold, champion, detected build style and level.
- **Enemy damage**: how much of the damage coming at you is physical (red) vs magic (blue), weighted by threat.
- **Enemy icons**: sorted by threat. A gold border and dot mean that enemy is fed. Border color shows their damage type.
- **Build next**: the 3 best items right now with the reasons. The price is what you still have to pay given the components you own, and it turns teal when you can afford it.
- **Boots**: shown when you have no upgraded boots yet.
- **Buy now / Next component**: what to buy at your next back.
- **Anti-heal now / Cleanse now**: a cheap counter when the enemy team calls for it.

## Settings

`%LOCALAPPDATA%\Counterbuild\config.json`. Changes are picked up within a few seconds, no restart needed.

| Key | Default | Meaning |
| --- | --- | --- |
| `hotkey` | `Ctrl+Shift+B` | Show/hide key. Needs a modifier unless it is an F-key, for example `F8` |
| `position` | `right` | `right`, `top-right`, `left`, `top-left`, `bottom-left` |
| `offsetX`, `offsetY` | `0` | Pixel nudge from the chosen position |
| `scale` | `1` | Size multiplier (the panel already scales with your game resolution) |
| `opacity` | `0.94` | 0.3 to 1 |
| `maxItems` | `3` | How many items to list (1 to 5) |
| `pollMs` | `1000` | How often to read the game, in milliseconds |
| `hideWhenUnfocused` | `true` | Hide when you alt-tab out of the game |
| `core` | `{}` | Items you always want on a champion, ranked first until you own them |

Example `core`:

```json
"core": {
  "Ezreal": ["Trinity Force", "Manamune"],
  "Jinx": ["Yun Tal Wildarrows", "Infinity Edge"]
}
```

## Is it allowed?

Counterbuild only uses Riot's local Live Client Data API, which exists for third-party tools, and Riot's public Data Dragon files. It does not read game memory, inject anything, press keys, or buy items for you. It shows no timers and no information you cannot see in game. It is an ordinary always-on-top window, the same approach other desktop overlays use. Riot's third-party rules prohibit automating decisions and tracking things like enemy ultimate timers; build suggestions are not in that category.

It is a personal tool, not registered with or endorsed by Riot. Use it at your own discretion.

## How it decides, and its limits

It has no win-rate database. Its strength is reacting to the game in front of you: who is fed, what they built, how much they heal or shield, how tanky they are, and what you can afford. For your first item it relies on your champion's class and build style, so for champions with unusual builds, pin a `core` or just play a few games and let it learn. Brand new champions fall back to their class defaults until their traits are added. Arena and Swarm are not supported.

## Files

Everything lives in `%LOCALAPPDATA%\Counterbuild\`:

- `config.json` settings
- `history.json` items you built per champion
- `counterbuild.log` errors, if any
- `ddragon\` cached patch data and icons

To uninstall: turn off *Start with Windows* in the tray menu, exit, then delete the `.exe` and that folder.

## Build from source

Go 1.24 or newer. Dependencies are included under `third_party/`, so no downloads are needed.

```sh
./build.sh        # Linux or macOS, cross-compiles Counterbuild.exe
build.bat         # Windows
go test ./...     # engine, renderer and app tests (use real patch data from engine/testdata)
```

Try it without League:

```sh
go run ./tools/mockgame     # fake game on https://127.0.0.1:2999
# then start Counterbuild.exe, or on Linux render a preview:
go run . -out preview.png
```

## Project layout

| Path | What |
| --- | --- |
| `engine/` | Game parsing, enemy profiling, item scoring, buy planning (pure Go, no OS code) |
| `render/` | Draws the panel into an image with per-pixel alpha |
| `app/` | Polling, patch data and icon cache, settings, build history |
| `main_windows.go`, `win32_windows.go` | Layered window, tray icon, hotkey |
| `main_other.go` | Preview tool for non-Windows systems |
| `tools/mockgame/` | Fake Live Client API for testing |
| `third_party/` | `golang.org/x/image` v0.31.0 and `golang.org/x/text` v0.29.0 subsets (BSD license) |
| `render/fonts/` | Go fonts used when Segoe UI is missing (BSD license) |

## Legal

Counterbuild isn't endorsed by Riot Games and doesn't reflect the views or opinions of Riot Games or anyone officially involved in producing or managing Riot Games properties. Riot Games, League of Legends and all associated properties are trademarks or registered trademarks of Riot Games, Inc.
