#!/usr/bin/env bash
# Run s390x tests with QEMU and the cross C toolchain.
# TAGS selects Go build tags. QEMU overrides qemu-s390x-static.
# Arguments select test packages. The default is `./...`.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
QEMU="${QEMU:-qemu-s390x-static}"
SYSROOT=/usr/s390x-linux-gnu

CC=s390x-linux-gnu-gcc "$ROOT/testdata/build_libbrotli_cref.sh"

cd "$ROOT"
# -short skips TestFixedShiftGenerated, which starts a binary without QEMU.
CGO_ENABLED=1 GOOS=linux GOARCH=s390x CC=s390x-linux-gnu-gcc \
    go test -short -tags "${TAGS:-}" -exec "$QEMU -L $SYSROOT" -timeout=80m "${@:-./...}"
