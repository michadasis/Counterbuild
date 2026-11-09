# Counterbuild

[![CI](https://github.com/michadasis/Counterbuild/actions/workflows/ci.yml/badge.svg)](https://github.com/michadasis/Counterbuild/actions/workflows/ci.yml)

A tiny click-through overlay for League of Legends that reads your live game and tells you what to build next against the enemy team you are actually facing.

- One 7 MB `.exe`. No installer, no Overwolf, no Electron, no runtime to install.
- About 20 to 30 MB of RAM and well under 1% CPU: one poll and a redraw of about 3 ms a second. In ARAM Mayhem, reading the augment cards adds about 30 ms of work every 3 seconds. It runs below normal priority in Windows' efficiency mode, so the game always gets the CPU first.
- Updates itself to the current patch from Riot's Data Dragon.
- Starts from what players actually build on your champion and role (OP.GG stats), then adapts it to your game.

## What it does

Every second it reads the official Live Client Data API that the game serves on `https://127.0.0.1:2999`, then:

1. **Profiles every enemy**: kit traits (healing, shields, crowd control, burst, auto-attack reliance, tankiness), what they have really bought (armor, magic resist, crit, lifesteal, AD vs AP), their level, and how fed they are. Fed enemies weigh more.
2. **Follows OP.GG's build order** for your champion, role and mode (fetched once a day): the core build, then the usual 4th, 5th and 6th item options, so it suggests the item players build *next*, not just a popular one.
3. **Scores every completed item in the current patch** for your champion and build style:
   - stat values come from the shop's own prices (Long Sword, Amplifying Tome, Ruby Crystal, ...)
   - armor and magic resist are weighted by the enemy damage split, tenacity by enemy crowd control, lifesteal goes down when they own anti-heal
   - penetration uses the real damage formula against each enemy's current armor or magic resist, so it knows when lethality beats % penetration and the other way round
   - amplifiers (Rabadon's, crit damage) scale with what you already have
   - counter effects (anti-heal, anti-shield, anti-crit, stasis, spell shield, lifeline, cleanse, max-health damage, shred) count only as much as the enemy team needs them
4. **Shows the top 3 items with the reason for each**, a boots pick if you have none, and **exactly what to buy with the gold you have right now** (the most expensive useful component toward your next item, using what you already own).
5. **Flags urgent cheap counters**, for example Executioner's Calling or Oblivion Orb against heavy healing, Quicksilver Sash against suppression.
6. **Learns your builds**: it remembers which items you finish on each champion, separately for Summoner's Rift, ARAM and URF, and leans toward them over time. You can also pin a core build per champion (see Settings).
7. **Rates ARAM Mayhem augment cards**: while the augment choice is on screen it reads the card titles and marks which to take and which to reroll, using OP.GG's augment tiers and ranking for your champion.

## Requirements

- Windows 10 or 11, 64-bit.
- League set to **Borderless** (Settings, Video, Window Mode). No external overlay can draw over exclusive Fullscreen.
- For augment ratings: the game client in English (OP.GG's augment names are English) and English in Windows' languages, which provides Windows' built-in text recognition (Settings, Time & language, Language & region; most PCs already have it). The log says so if it's missing.

## Run

1. Download `Counterbuild.exe` from the [latest release](https://github.com/michadasis/Counterbuild/releases/latest) and run it. Windows SmartScreen may warn because the file is not code-signed: choose *More info*, then *Run anyway*. You can also build it yourself (see below).
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
- **Build next**: the 3 best items right now with the reasons. The price is what you still have to pay given the components you own, and it turns teal when you can afford it. "OP.GG core (52% WR)" means the item is part of the most-built core build, with its win rate; "OP.GG item 4" means players usually build it as their 4th item. "off-meta" means the pick is outside OP.GG's builds for your champion and is there because of this game (for example anti-heal against heavy healing). "with OP.GG stats" appears next to the title once that data has loaded.
- **Boots**: shown when you have no upgraded boots yet.
- **Buy now / Next component**: what to buy at your next back.
- **Anti-heal now / Cleanse now**: a cheap counter when the enemy team calls for it.
- **Augment pick** (ARAM Mayhem): the augment cards on screen, left to right, with OP.GG's tier for your champion (S, A, B, or below B) and its place in OP.GG's list (`A · #13`). *Reroll* means the card is B tier or below: reroll that card. Of the S and A cards, *Take* is the one highest in OP.GG's list and the others say *OK*. If OP.GG's augment page can't be read, the app falls back to OP.GG's data feed, which only has the tiers below B: cards missing from it say *S to B tier* and *Even*, since it can't tell them apart. *No data* means OP.GG has no data on that augment for your champion. The section appears within about 3 seconds of the cards and disappears a couple of seconds after you pick. On a very weak PC you can turn it off with *Rate augment cards* in the tray menu.

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
| `metaSource` | `opgg` | Where most-built data comes from: `opgg`, or `off` to use only the built-in logic |
| `metaUrl` | `https://mcp-api.op.gg/mcp` | OP.GG endpoint |
| `augments` | `auto` | Rate augment cards: `auto` in ARAM Mayhem, `always` on any Howling Abyss game, `off` to never read the screen. Also in the tray menu |

Example `core`:

```json
"core": {
  "Ezreal": ["Trinity Force", "Manamune"],
  "Jinx": ["Yun Tal Wildarrows", "Infinity Edge"]
}
```

## Is it allowed?

Counterbuild uses Riot's local Live Client Data API, which exists for third-party tools, Riot's public Data Dragon files, OP.GG's public stats endpoint and, in ARAM Mayhem, OP.GG's augment page for your champion (its robots.txt allows it), and CommunityDragon's copy of the game's augment list (to recognize every augment card by name). In ARAM Mayhem it also takes a screenshot of the strip of the game window where the card titles are, every 3 seconds, and reads the augment card titles with Windows' built-in text recognition; the screenshots stay in memory and are never saved or sent anywhere. It does not read game memory, inject anything, press keys, pick augments, or buy items for you. It shows no timers and no information you cannot see in game. It is an ordinary always-on-top window, the same approach other desktop overlays use. Riot's third-party rules prohibit automating decisions and tracking things like enemy ultimate timers; build suggestions are not in that category.

Riot has restricted showing augment statistics during the augment choice in some modes (for example in TFT). Check Riot's current third-party policies if that matters to you, and set `augments` to `off` (or untick *Rate augment cards* in the tray menu) to switch the screen reading off completely.

It is a personal tool, not registered with or endorsed by Riot or OP.GG. Use it at your own discretion.

### What it sends to OP.GG

Only your champion, role and game mode (for example "Ahri, mid, ranked"), once per champion and role per day, plus in ARAM Mayhem a request for OP.GG's public augment page of your champion (for example op.gg/lol/modes/aram-mayhem/lux/augments), once per champion per day; the answers are cached. Nothing from the screen is sent. Nothing about your account, name or match. OP.GG made that endpoint for AI assistants rather than as a documented app API and publishes no terms for it either way, so if they change or close it, the overlay keeps working with its built-in logic. Set `metaSource` to `off` to never contact OP.GG.

## How it decides, and its limits

Your build follows the order players most often build on your champion and role (from OP.GG): with one item done it suggests what they build second, and a usual 5th item waits until you are there. Among OP.GG's options for that slot it picks the one that suits this game. On top of that it reacts to the game in front of you: who is fed, what they built, how much they heal or shield, how tanky they are, and what you can afford, and urgent counters can outrank the usual core.

If OP.GG data is unavailable, first and core items come from your champion's class and build style, so for champions with unusual builds, pin a `core` or just play a few games and let it learn. Brand new champions fall back to their class defaults until their traits are added. Arena and Swarm are not supported.

## Files

Everything lives in `%LOCALAPPDATA%\Counterbuild\`:

- `config.json` settings
- `history.json` items you built per champion and kind of game
- `counterbuild.log` errors, if any
- `ddragon\` cached patch data and icons
- `meta\` cached OP.GG builds and augment scores, and `augment_names.json`; `opgg_last_response.txt`, `opgg_augments_last_response.txt` and `opgg_tools.json` help debug if no OP.GG data shows up

To uninstall: turn off *Start with Windows* in the tray menu, exit, then delete the `.exe` and that folder.

## Build from source

Go 1.24 or newer. Dependencies are included under `third_party/`, so no downloads are needed.

```sh
./build.sh        # Linux or macOS, cross-compiles Counterbuild.exe
build.bat         # Windows
go test ./...     # engine, OP.GG client, renderer and app tests (use real patch data from engine/testdata)
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
| `meta/` | OP.GG builds and augment tiers (a small MCP client, response parsing, daily cache) and the augment name list |
| `main_windows.go`, `win32_windows.go` | Layered window, tray icon, hotkey |
| `ocr_windows.go` | Screen capture and Windows OCR (WinRT) for the augment cards |
| `main_other.go` | Preview tool for non-Windows systems |
| `tools/mockgame/` | Fake Live Client API for testing |
| `third_party/` | `golang.org/x/image` v0.31.0 and `golang.org/x/text` v0.29.0 subsets (BSD license) |
| `render/fonts/` | Go fonts used when Segoe UI is missing (BSD license) |

## License

MIT, see [LICENSE](LICENSE). The code under `third_party/` (`golang.org/x/image` and `golang.org/x/text`) and the Go fonts in `render/fonts/` keep their own BSD licenses, included next to them.

## Legal

Counterbuild isn't endorsed by Riot Games and doesn't reflect the views or opinions of Riot Games or anyone officially involved in producing or managing Riot Games properties. Riot Games, League of Legends and all associated properties are trademarks or registered trademarks of Riot Games, Inc.
