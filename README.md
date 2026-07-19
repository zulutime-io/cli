# ztime

Developer CLI for [ZuluTime](https://zulutime.io) — book hours from the terminal, with git commits as suggestions.

Docs: [zulutime.io/docs/cli](https://zulutime.io/docs/cli)

## Install

**Homebrew**

```bash
brew install zulutime-io/tap/ztime
```

**Go**

```bash
go install github.com/zulutime-io/cli/cmd/ztime@latest
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`.

**Binary**

Download an archive from [Releases](https://github.com/zulutime-io/cli/releases), extract `ztime`, and put it on your `PATH`.

## Quick start

```bash
ztime login
ztime whoami
ztime book      # interactive: client → project → hours (+ git commits)
ztime submit    # submit today's drafts
```

Default API: `https://zulutime.io/api/v1`. Override with `ZTIME_API_URL` or `api_url` in config.

## Commands

```bash
ztime login / logout / whoami / version

ztime book                          # interactive booking
ztime book --hours 2 --date 2026-07-16 --submit --no-git

ztime status                        # today's entries
ztime status --date 2026-07-15

ztime edit                          # edit latest draft/rejected (+ commits)
ztime amend                         # amend git commits on latest entry
ztime squash                        # merge drafts (hours + commits)
ztime squash --force                # allow different projects (keep target)

ztime submit                        # submit today's drafts
ztime submit --from 2026-07-01 --to 2026-07-16

ztime hook install                  # post-commit tip → ztime book
ztime hook uninstall
ztime hint

ztime timer start [label]           # local timer (Starship-friendly)
ztime timer status / stop / cancel
```

## Git integration

Inside a git repo, `ztime book` and `ztime amend`:

- Suggest today's commits (from your `user.email`), otherwise recent ones
- Skip commits already booked (by SHA)
- Store selected commits with the time entry (visible in the web app)
- Remember the project per `origin` remote

### Post-commit tip

```bash
ztime hook install
```

After `git commit`:

```
⏱  ztime · my-repo (main)
    "feat: invoice lock"
    → ztime book
```

Hints are throttled (max once per 10 minutes per repo). Disable with `ZTIME_HINT=0`, or force with `ZTIME_HINT=force`. The hook never fails the commit.

## Timer + Starship

```bash
ztime timer start
ztime timer start invoice
ztime timer stop          # optional: book the elapsed time
```

In `~/.config/starship.toml`:

```toml
[custom.ztime]
command = "ztime timer prompt"
when = "ztime timer running"
format = "[$output]($style) "
style = "bold green"
shell = ["sh"]
```

`ztime` must be on your login `PATH` (Starship does not load interactive shell config).

## Config

| | macOS | Linux |
|---|---|---|
| Directory | `~/Library/Application Support/ztime/` | `~/.config/ztime/` |

| File | Purpose |
|------|---------|
| `config.json` | `api_url`, remembered projects |
| `credentials.json` | tokens (mode `0600`) |
| `timer.json` | local timer state |

## License

[MIT](LICENSE)
