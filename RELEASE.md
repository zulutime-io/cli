# Releasing

1. Create public repo [zulutime-io/homebrew-tap](https://github.com/zulutime-io/homebrew-tap) if needed.
2. Add Actions secret `HOMEBREW_TAP_GITHUB_TOKEN` (Contents: write on the tap).
3. Tag and push:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions runs GoReleaser → GitHub Release + Homebrew formula bump.
