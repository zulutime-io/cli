# Releasing

Prerequisites (one-time):

1. Public tap: [zulutime-io/homebrew-tap](https://github.com/zulutime-io/homebrew-tap)
2. Actions secret **`HOMEBREW_TAP_GITHUB_TOKEN`** on this repo — fine-grained PAT with **Contents: Read and write** on `zulutime-io/homebrew-tap` only

Release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions runs GoReleaser → GitHub Release + Homebrew `Formula/ztime.rb` bump.
