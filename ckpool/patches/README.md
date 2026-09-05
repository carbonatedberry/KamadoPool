# CKPool Patches

Patches applied on top of the upstream CKPool commit pinned in `../CKPOOL_COMMIT`.

Patches are applied in alphabetical order by filename. Use a numeric prefix to enforce ordering:

- `0001-short-description.patch`
- `0002-another-fix.patch`

## Current state

Three Kamado patches are applied on top of the pinned upstream commit, in
alphabetical order:

| Patch                                                    | What it does                                                                                  |
| -------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `0001-expose-bestever-in-runtime-json.patch`             | Adds `bestever` field to the `users` / `workers` runtime socket JSON                          |
| `0002-enable-socket-api-responses.patch`                 | Always reply on the listener socket so kamado-api gets responses even with `btcsolo: true`    |
| `0003-share-error-as-stratum-array.patch`                | Maps `share_err` to Stratum spec error codes; emits `[code, msg, null]` per Slush             |
| `0007-rename-coinbase-tag-ckpool-to-kamado.patch`        | Replaces hardcoded "ckpool" branding in coinbase scriptSig with "kamado" (same 6 bytes)       |

### Why 0001 matters

Upstream tracks `best_ever` internally in `user_instance_t` / `worker_instance_t`
and zeroes `best_diff` on every block solve via `reset_bestshares()`. That
is correct: `bestdiff` is "best share in the current round". But the runtime
socket API (`userinfo()` / `workerinfo()` in `stratifier.c`) only emits
`bestdiff`, so any consumer that talks to the socket, like `kamado-api`,
sees the best share reset to 0 after every block and has no all-time field
to fall back on. The on-disk `users.json` / `workers.json` persistence files
do include `bestever`, but polling those is racy and lags the socket.

This patch adds `bestever` to the runtime JSON so the UI can show both
"this round" and "all-time" best share side by side. No behavioral change
to share validation or block handling. Candidate for upstreaming.

Beyond this patch, the pinned upstream commit (`cfb0f83b`, tagged as
version 1.0) already includes every fix that Bassin issue #29 asked to
backport, plus several improvements:

| Upstream commit | What it fixes                                                  |
| --------------- | -------------------------------------------------------------- |
| `a439cf96`      | workbase_id double increment bug                               |
| `590fb2a2`      | Extended timeouts for low-powered miners (NerdMiner, ESP32)    |
| `b13f3eee`      | Configurable `dropidle` timeout (exposed via our env var)      |
| `66db3aa3`      | Better vardiff for bursty hashers                              |
| `130c755d`      | Fix unlikely fopen segfault                                    |
| `0bd3d751`      | Consistent error field in mining.submit rejections             |
| `988b2687`      | Version 1.0, longstanding stability bump                      |

Kamado therefore starts from a CKPool that is strictly ahead of what Bassin
ships today.

## When to add a patch

Use this directory for:

1. Fixes needed before they land upstream (and only after attempting
   to submit upstream first).
2. Kamado-specific behavior changes that would not be accepted upstream,
   e.g. tighter integration hooks with `kamado-api`.
3. Temporary workarounds with a clear removal plan, documented in the
   patch commit message.

Do NOT use this directory for:

- Pure configuration changes (expose via the entrypoint env vars instead).
- Build system tweaks (put those in the Dockerfile).

## Creating a patch

From a clean clone of upstream at the pinned commit:

```sh
git clone https://bitbucket.org/ckolivas/ckpool.git
cd ckpool
git checkout $(cat /path/to/KamadoPool/ckpool/CKPOOL_COMMIT)
# ... make your changes ...
git diff > /path/to/KamadoPool/ckpool/patches/0001-my-fix.patch
```

The Dockerfile applies each `*.patch` file in this directory with
`git apply --verbose` during the build.
