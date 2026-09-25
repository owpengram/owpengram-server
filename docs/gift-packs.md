# Gift packs

A gift pack is a portable bundle -- a `pack.json` manifest plus the Lottie/TGS
assets it references -- that adds one or more Star Gifts to the catalog in a
single admin action.

**Nothing ships installed.** A fresh server has an empty shelf and an empty
catalog. The loop is two deliberate steps, both in **StarGift Catalog →
Import Pack**:

1. **Upload** a pack `.zip`. It is parsed, every animation it declares is
   validated, and it is stored on the shelf. Nothing reaches the catalog.
2. **Import** from a pack on the shelf -- *Preview* opens every gift and
   every upgraded variant, and you publish either the whole pack
   (*Import all*) or one gift at a time (*Import this gift* on a gift's own
   page). A fifty-gift pack does not have to be taken whole.

Re-importing is always safe: a gift already present by title is skipped,
never duplicated, so handing an operator an updated pack that only adds a
few new gifts to one they already imported just works. Re-uploading a pack
with the same name replaces the stored archive rather than adding a second
copy of it, and removing a pack from the shelf leaves gifts already imported
from it untouched -- by then they are catalog entries of their own.

The packs authored in this repo (`internal/seed/giftpacks`) are archives
like any other. Build them with:

```bash
go run ./cmd/giftpack-export
```

which writes `dist/packs/grind-pack.zip` (desk gear and coffee; between them
its gifts exercise every Star Gift mechanic, so it doubles as the reference
pack) and `dist/packs/energy-pack.zip` (one strictly limited, craftable can
with a fifty-strong flavour pool).

## Build your own pack: the short version

This is the whole loop, start to finish. Everything in it is expanded on
further down.

**1. Draw the animations.** One Lottie per gift, plus one per model and per
pattern if the gift is upgradeable. Hard requirements the server checks:

| Rule | Value |
| --- | --- |
| Canvas | exactly 512 × 512 |
| Frame rate | `> 0`, at most 120 |
| Duration | `op > ip >= 0`, at most 30 seconds |
| Decompressed JSON | at most 4 MiB, at least one layer |
| Compressed `.tgs` | at most 512 KiB |
| Expressions (`"x"`) | rejected |
| Image assets, external URLs | rejected |
| Single file in the pack `.zip` | at most 8 MiB; whole `.zip` at most 32 MiB |

Read the rlottie section below **before** drawing anything -- the renderer
real clients use is stricter than any browser preview, and the server cannot
catch that for you.

**2. Convert to `.tgs`** (optional -- plain `.json` uploads fine and is
normalized server side):

```bash
gzip -9 -c gift.json > gift.tgs
```

**3. Write `pack.json`** at the archive root, referencing every animation by
its path inside the zip. Full field reference is below; the minimum is:

```json
{ "pack_name": "My Pack", "gifts": [ { "title": "My Gift", "stars": 25, "base_animation": "gift.json" } ] }
```

**4. Zip it** with `pack.json` at the root:

```bash
zip -r mypack.zip pack.json gift.json models/ patterns/
```

**5. Dry-run the upload** in **StarGift Catalog → Import Pack → Upload a
pack**. The dry run parses the manifest and resolves and validates every
animation, then shows exactly what the pack contains. Nothing is stored
until you confirm. Importing from the shelf afterwards has its own dry run,
which runs the limited/auction/craft checks a real import does.

**6. Confirm, then look at it in a real client** -- the admin preview uses a
browser Lottie player, which is *not* proof the pack renders in the app.

### Checklist before you hand a pack to someone

- Every gift `title` is unique inside the pack, distinct from what the
  target catalog already has (same title = skipped as already imported), and
  no two titles reduce to the same URL slug (the upload refuses that: they
  would share one preview handle).
- Every path in `pack.json` resolves inside the zip, relative to its root.
- Upgradeable gifts have **at least two selectable models, two patterns and
  two backdrops** (craft-only models do not count towards that).
- `slug_prefix` is set per upgradeable gift and is not reused across gifts
  in the same pack. If it collides with a prefix already in use on the
  target server, the import moves to `<prefix>-2` on its own.
- You watched every animation loop in a real rlottie client, not only in a
  browser.

### When a dry run fails

| Message | What it means |
| --- | --- |
| `open "x.json" in pack` | The path in `pack.json` does not exist at that exact path in the zip (check the archive root, and case). |
| `base animation: stargift: invalid animation file` | The animation broke one of the hard rules in the table above -- most often not 512×512, or an expression left in by the exporter. |
| `external assets are not allowed` | The Lottie references an image or a URL. Convert the artwork to vector shapes. |
| `auction requires gifts_per_round > 0` and friends | An auction gift is missing `auction_slug`, `gifts_per_round` or `availability_total`. |
| `... preview requires at least two selectable attributes` | An upgrade pool has too few selectable models/patterns/backdrops. |

## Read this before you build one: rlottie, not a browser preview

This server's validation is **structural only**. When you upload an
animation, it checks that the file is valid gzip/JSON, exactly 512×512, a
sane frame rate and duration, has no Lottie expressions, and references no
external or embedded image assets. **It never renders a single frame.**

Real Telegram-protocol clients -- including OwpenGram's own desktop and
mobile forks -- decode `.tgs` through **rlottie**, a small, fast renderer
that implements only a *subset* of the full Lottie/Bodymovin spec. An
animation that plays back perfectly in a browser Lottie preview, or looks
right in the After Effects/Bodymovin exporter, can come out **broken, blank,
or missing effects entirely** in the real app, because the two renderers
don't agree on what the file means.

There is no way for this server to catch that for you -- it would need to
embed rlottie itself, which it doesn't. So the practical rule is:

- **Known safe** (every one of these is used by the packs in
  `internal/seed/giftpacks` and verified frame by frame in rlottie): shape
  layers with ellipses, rectangles, polystars and bezier paths; solid,
  linear-gradient and radial-gradient fills, including gradients with
  opacity stops (soft shadows, glows, highlights); solid and gradient
  strokes; nested groups with their own transforms; null layers with
  parenting; and keyframed position, scale, rotation and opacity.
- **Avoid**: masks and mattes, merge paths, repeaters, image or
  pre-composition layers, and text layers. Support for these in rlottie is
  partial at best and varies by version.
- **Be cautious with**: blend modes and anything not in the "known safe"
  list above.
- **Always preview the final `.tgs` in a real rlottie-based client** --
  OwpenGram Desktop, or a stock Telegram client if you're not sure your fork
  differs -- before shipping a pack to anyone. A web Lottie player (or After
  Effects itself) is not a substitute; it will happily show you something
  rlottie can't.

  **Concrete examples, found the hard way** while building OwpenGram's own
  packs here -- three separate, cumulative defects,
  each one invisible to this server's structural validator and to a generic
  Lottie player, each confirmed only by diffing a genuine Telegram-issued
  `.tgs`'s raw JSON against the generated equivalent:

  1. **Key order.** If you generate Lottie JSON programmatically (a script,
     not an After Effects/Bodymovin export or a hand-typed file), watch your
     serializer's key order. rlottie's parser dispatches on a shape/layer's
     `"ty"` key while walking the object's keys in a single pass, the way a
     real export always orders them (`"ty"` early). Some serializers --
     notably a `map[string]any` run through Go's `encoding/json`, which
     always sorts keys alphabetically -- silently reorder that.
  2. **Flat shapes instead of groups.** A real export never places a
     drawable shape (`el`/`rc`/`sr`) or a paint (`fl`/`st`) directly in a
     layer's `"shapes"` array -- every one is wrapped in a `"gr"` group,
     ending with its own identity `"tr"` transform. Colours are 4-component
     RGBA, not 3-component RGB, and every shape/group carries `"hd"` and
     `"nm"`. A flat, ungrouped, 3-component-colour file is valid enough for
     a generic interpreter but not for rlottie.
  3. **A trailing `"i"`/`"o"` on an animated property's last keyframe.** A
     real export never includes temporal easing on the terminal keyframe of
     an animated ("a":1) property -- there's nothing after it to ease into.
     Including it anyway doesn't just get ignored: rlottie fails to render
     that entire shape from the first frame after 0 onward, silently. This
     is what made some of this pack's icons (the ones with a pulsing
     flame/star/gem, i.e. any part using an animated scale or rotation)
     render as nothing while fully static icons in the very same pack
     rendered perfectly -- easy to misdiagnose as "some gifts are just
     broken" rather than "every *animated* part is broken".

  Each of these three renders **perfectly in a browser Lottie preview and in
  at least one generic rlottie build**, and only the third one is even
  remotely well-known outside Telegram's own tooling. If a gift looks right
  everywhere except the actual client, check all three -- starting with the
  animated-property keyframes, since that one produces the most
  misleading symptom (partial breakage, not total).

## Adding a pack to this repo

The packs authored here live in `internal/seed/giftpacks`, drawn in Go with
the Lottie DSL in `lottie.go`, which emits only structures copied from a real
Telegram export (so all three rules above hold by construction). To add one,
create a file next to `grindpack.go` whose `init()` calls `register(...)`
with an id, name, author, description, an icon (the slug of one of its
gifts) and its gifts. `go run ./cmd/giftpack-export` then builds it into an
uploadable `.zip` along with the rest; the export is deterministic, so
re-running it on unchanged art produces byte-identical archives.

`go test ./internal/seed/giftpacks/` imports every registered pack through
the real Star Gift service, lints every animation against the rlottie rules
above, and exports each pack to check the archive alone is self-contained.
It can't render, though: still check new art frame by frame in a real
rlottie build before shipping it.

## `pack.json` reference

```json
{
  "pack_name": "My Pack",
  "author": "you",
  "description": "What this pack is, shown on its card.",
  "icon": "example-gift",
  "gifts": [
    {
      "id_slug": "example-gift",
      "title": "Example Gift",
      "stars": 25,
      "convert_stars": 25,
      "base_animation": "example.json",

      "limited": false,
      "availability_total": 0,
      "birthday": false,
      "require_premium": false,
      "support_only": false,
      "limited_per_user": false,
      "per_user_total": 0,
      "resell_min_stars": 0,

      "auction": false,
      "auction_slug": "",
      "gifts_per_round": 0,
      "auction_start_date": 0,
      "auction_round_duration": 0,

      "upgrade": {
        "upgrade_stars": 50,
        "supply_total": 1000,
        "slug_prefix": "example",
        "models": [
          { "name": "Classic", "animation": "example_classic.json", "permille": 700 },
          { "name": "Golden", "animation": "example_golden.json", "permille": 300 }
        ],
        "patterns": [
          { "name": "Dot", "animation": "pattern_dot.json", "permille": 600 },
          { "name": "Spark", "animation": "pattern_spark.json", "permille": 400 }
        ],
        "backdrops": [
          { "name": "Azure", "center": "#1F8FFF", "edge": "#0B4FA8", "pattern": "#0B4FA8", "text": "#FFFFFF", "permille": 500 },
          { "name": "Blush", "center": "#FF8FB3", "edge": "#C23A63", "pattern": "#C23A63", "text": "#FFFFFF", "permille": 500 }
        ]
      }
    }
  ]
}
```

Top level: `pack_name` (required -- it is also the pack's identity on the
shelf, slugified), `author`, `description` and `icon` (all optional;
`icon` names the gift whose animation represents the pack and defaults to
the first one), `gifts` (at least one, unique `title` per gift).

Every animation path (`base_animation`, and every model/pattern
`animation`) is resolved **relative to the zip root**, next to `pack.json`,
not relative to `pack.json`'s own location if you nest it. `.tgs` (gzip'd
Lottie), plain `.json`, or `.lottie` are all accepted -- the server always
normalizes to TGS internally.

Per-gift fields (all but `title`, `stars` and `base_animation` are optional,
default to their Go zero value):

| Field | Meaning |
| --- | --- |
| `stars` / `convert_stars` | Purchase price and Stars refunded on conversion. `convert_stars` must be `0..stars`. |
| `limited` / `availability_total` | Caps total supply. `limited` requires `availability_total > 0`, and vice versa. |
| `birthday` | Shown as a birthday-only gift. Purely a display flag -- the server does not check the recipient's birthday. |
| `require_premium` | Only Premium accounts can buy it. |
| `support_only` | Only the operator's support account can buy it. |
| `resell_min_stars` | The floor price for the *first* resale listing. After that, the market's own lowest active listing takes over -- this is a starting point, not a permanent policy. |
| `auction` | Distributed by auction instead of a normal purchase (excluded from the regular buy flow). Requires `auction_slug`, `gifts_per_round > 0`, `availability_total > 0`, `gifts_per_round <= availability_total`; forces `limited: true`. `auction_start_date` (Unix seconds) defaults to "now" when left `0`. |

`upgrade` (optional) publishes a collectible attribute pool alongside the
gift, so it can be upgraded into a unique collectible. If present:

- `models` and `patterns` need **at least two selectable entries each**
  (`permille > 0`, not crafted) -- the client's upgrade animation needs a
  non-target option in every category to know when to stop. `permille`
  values are relative weights, not required to sum to 1000.
- A model may instead be **craft-only**: `"crafted": true` with no
  `permille` (or `0`) and a named `rarity` (`"uncommon"`, `"rare"`,
  `"epic"`, or `"legendary"`). Crafted attributes are never drawn by the
  regular random upgrade -- only through crafting. `crafted` is only legal
  on a model, never a pattern or backdrop.
- `backdrops` also need at least two selectable entries; they carry colors
  only (`center`/`edge`/`pattern`/`text`, each `#RRGGBB`), never an
  animation.

## Packaging

```
mypack.zip
├── pack.json
├── example.json
├── example_classic.json
├── example_golden.json
├── pattern_dot.json
└── pattern_spark.json
```

A flat layout works fine, or organize assets into subfolders as long as the
paths in `pack.json` match. Upload the `.zip` from **StarGift Catalog →
Import Pack → Upload a pack**; the panel runs a dry-run first (parses the
manifest, resolves and validates every animation) and shows you exactly what
the pack contains before you confirm. It then appears on the shelf, where
*Preview* and the import actions live.

## Licensing

Pack assets must be your own work, or something you have the rights to
distribute. Do not repackage Telegram's own official gift animations or
other copyrighted material -- they're a useful reference for understanding
the *format* (how a gift's model/pattern/backdrop pool is put together,
what rlottie can and can't render), never something to ship in a pack.
