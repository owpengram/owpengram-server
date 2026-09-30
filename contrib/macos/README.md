# macOS launcher (unsupported, untested)

`owpengram-macos.sh` is the same launcher as `owpengram-server.sh`, adapted
for macOS (`sysctl` instead of `/proc` for the process checks the Linux/BSD
variants use, etc.). It lives here instead of the repo root because nobody
on this project has a Mac to actually run it on -- nothing in CI builds it,
tests it, or ships it in release archives.

If you use it and hit a bug, patches welcome. Run it from the repo root:

```bash
./contrib/macos/owpengram-macos.sh
```
