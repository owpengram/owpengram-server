# Migration numbering

Upstream (`iamxvbaba/gramsrv`) numbers its migrations sequentially
(`0001`, `0002`, ..., currently up to `0092`). This fork periodically merges
upstream, and upstream keeps adding its own sequentially-numbered migrations
between syncs.

**Any new OwpenGram-specific migration must use a `YYYYMMDDHHMMSS` timestamp
as its version instead of continuing the sequential numbering** — e.g.
`20260713103024_owpengram_system_account_brand.up.sql`.

Why: golang-migrate treats the version as a plain number and only cares
about ordering, not format. If we keep picking "the next free sequential
number" for our own migrations, the next upstream sync is very likely to
have independently picked the *same* number for one of its own new
migrations — two different new files, so git sees no conflict, and the
collision only surfaces at server startup (`iofs source: failed to init
driver ... duplicate migration file`, see `deploy/deploy_test.go`'s
`TestMigrationsLoad` for the regression test that now catches this at
`go test` time instead). This happened once already (our `0086`-`0088`
collided with upstream's own new `0086`-`0088`), and renumbering our
migrations to fit after upstream's (`0093`-`0095`) would only have deferred
the exact same problem to the *next* sync.

A 14-digit timestamp is always numerically larger than upstream's 4-5 digit
sequential numbers, so it always sorts after everything upstream has today
or will plausibly reach, and it never collides with another OwpenGram
migration as long as two aren't created in the same second. No renumbering
is ever needed again on future syncs — just keep using the timestamp at the
time the migration is written.

Upstream's own migrations keep their original sequential numbers untouched;
only migrations authored in this fork use the timestamp scheme.
