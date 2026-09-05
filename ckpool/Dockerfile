# ============================================================================
# CKPool-solo build stage
# ============================================================================
# Builds a patched ckpool binary from upstream source at a pinned commit.
# Binaries land in /build/ckpool/src/, we copy `ckpool` and `ckpmsg` into
# the runtime image. We do NOT `make install` because upstream's
# install-exec-hook calls setcap which fails inside a docker build.
#
# Portability:
#   - No yasm installed -> ckpool falls back to its portable C SHA256
#     implementation. This trades some per-core hashrate validation
#     throughput for a binary that runs on any x86_64/aarch64 CPU
#     regardless of the build machine's feature flags.
#   - We override CFLAGS to drop the upstream default `-march=native`
#     for the same reason.
# ----------------------------------------------------------------------------
FROM debian:bookworm-slim AS ckpool-build

ARG CKPOOL_REPO=https://bitbucket.org/ckolivas/ckpool.git
ARG CKPOOL_COMMIT=cfb0f83b70d7b382b85d2bd0710cf4cb2dda4007

RUN apt-get update && apt-get install --no-install-recommends -y \
        build-essential \
        autoconf \
        automake \
        libtool \
        pkg-config \
        libzmq3-dev \
        ca-certificates \
        git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

RUN git clone "${CKPOOL_REPO}" ckpool \
    && cd ckpool \
    && git checkout "${CKPOOL_COMMIT}"

# Apply Kamado patches in alphabetical order, if any exist
COPY patches/ /tmp/kamado-patches/
RUN set -eux; \
    cd /build/ckpool; \
    for p in /tmp/kamado-patches/*.patch; do \
        [ -f "$p" ] || continue; \
        echo "Applying $(basename "$p")"; \
        git apply --verbose "$p"; \
    done

# Configure and build. Explicit CFLAGS override upstream's default
# `-O2 -Wall -march=native`. We keep -O2 -Wall but drop -march=native.
RUN cd /build/ckpool \
    && ./autogen.sh \
    && CFLAGS="-O2 -Wall -pipe" ./configure --prefix=/usr/local \
    && make -j"$(nproc)"

# Sanity check
RUN test -x /build/ckpool/src/ckpool \
    && test -x /build/ckpool/src/ckpmsg \
    && file /build/ckpool/src/ckpool || true

# ============================================================================
# Runtime stage (minimal)
# ============================================================================
FROM debian:bookworm-slim AS ckpool-runtime

RUN apt-get update && apt-get install --no-install-recommends -y \
        libzmq5 \
        ca-certificates \
        tini \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ckpool-build /build/ckpool/src/ckpool /usr/local/bin/ckpool
COPY --from=ckpool-build /build/ckpool/src/ckpmsg /usr/local/bin/ckpmsg
COPY config/ckpool.conf.template /etc/ckpool/ckpool.conf.template
COPY entrypoint.sh /usr/local/bin/ckpool-entrypoint.sh
RUN chmod +x /usr/local/bin/ckpool-entrypoint.sh

RUN mkdir -p /run/ckpool /var/lib/ckpool /var/log/ckpool

EXPOSE 3333

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/ckpool-entrypoint.sh"]
