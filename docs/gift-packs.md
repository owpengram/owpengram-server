# Gift packs

A gift pack is a portable bundle -- a `pack.json` manifest plus the Lottie/TGS
assets it references -- that adds one or more Star Gifts to the catalog in a
single admin action. Import it from **StarGift Catalog → Import Pack** in the
admin panel: either the built-in **default pack** (one click) or your own
`.zip` upload.

Re-importing a pack is always safe: a gift already present by title is
skipped, never duplicated, so handing an operator an updated pack that only
adds a few new gifts to one they already imported just works.

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

- **Stick to basic shape layers** (ellipse, rectangle, polygon/star) animated
  with **transform, opacity, scale and rotation keyframes only**. This is
  exactly what the built-in default pack does (see
  `internal/seed/giftpackdefault`) -- every animation in it is generated from
  these primitives alone, specifically so it's safe to ship without a manual
  rlottie check on each one.
- **Avoid**: masks and mattes, merge paths, repeaters, image or
  pre-composition layers, and text layers. Support for these in rlottie is
  partial at best and varies by version.
- **Be cautious with**: gradients and blend modes. Simple linear gradients
  usually work; anything more elaborate is a gamble.
- **Always preview the final `.tgs` in a real rlottie-based client** --
  OwpenGram Desktop, or a stock Telegram client if you're not sure your fork
  differs -- before shipping a pack to anyone. A web Lottie player (or After
  Effects itself) is not a substitute; it will happily show you something
  rlottie can't.

  **Concrete examples, found the hard way**, all from building
  `internal/seed/giftpackdefault` -- three separate, cumulative defects,
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

## `pack.json` reference

```json
{
  "pack_name": "My Pack",
  "author": "you",
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

Top level: `pack_name` (required), `author` (optional), `gifts` (at least
one, unique `title` per gift).

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
manifest, resolves and validates every animation, checks the same
limited/auction/craft rules as a real import) and shows you exactly what
would be created before you confirm.

## Licensing

Pack assets must be your own work, or something you have the rights to
distribute. Do not repackage Telegram's own official gift animations or
other copyrighted material -- they're a useful reference for understanding
the *format* (how a gift's model/pattern/backdrop pool is put together,
what rlottie can and can't render), never something to ship in a pack.
