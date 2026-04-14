#!/usr/bin/env bash
# tools/release-downstream.sh
#
# Propagates a nodemanager release to downstream repos:
#   1. nodemanager-bin   — renders PKGBUILD with new version + checksums, commits, pushes
#   2. aur               — updates nodemanager-bin submodule, commits, pushes
#   3. jsonnet-libs      — adds new version to CRD config, regenerates libsonnet, commits, pushes
#   4. personal-ports    — bumps PORTVERSION, regenerates distinfo from Go proxy + GitHub, commits, pushes
#
# Run from the repo root after `make release` has published binaries to GitHub.
#
# Required:
#   git, curl, awk, sed, make, docker (for jsonnet-libs step)
#
# Environment:
#   GITHUB_TOKEN              — used to fetch release assets; required if repo is private
#   AUR_DIR                   — path to the aur checkout (default: ~/Code/aur)
#   JSONNET_LIBS_DIR          — path to jsonnet-libs checkout (default: ~/Code/jsonnet-libs)
#   PERSONAL_PORTS_DIR        — path to personal-ports checkout (default: ~/Code/personal-ports)
#   GIT_AUTHOR_NAME           — overrides git commit author name (useful in CI)
#   GIT_AUTHOR_EMAIL          — overrides git commit author email (useful in CI)
#   DRY_RUN                   — set to 1 to skip git push and docker/make steps
#   NODEMANAGER_BIN_REMOTE    — override nodemanager-bin clone URL
#   AUR_REMOTE                — override aur clone URL
#   JSONNET_LIBS_REMOTE       — override jsonnet-libs clone URL
#   PERSONAL_PORTS_REMOTE     — override personal-ports clone URL
#   CHECKSUMS_URL             — override checksums download URL (default: GitHub release URL)

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"

WORK_DIR="${WORK_DIR:-$(mktemp -d)}"
# Only clean up if we created the directory ourselves (not caller-provided)
[[ -z "${WORK_DIR_EXTERNAL:-}" ]] && trap 'rm -rf "${WORK_DIR}"' EXIT

DRY_RUN="${DRY_RUN:-0}"

# In CI, clone repos fresh into WORK_DIR. Locally, default to known checkout paths.
if [[ -n "${CI:-}" ]]; then
  AUR_DIR="${WORK_DIR}/aur"
  JSONNET_LIBS_DIR="${WORK_DIR}/jsonnet-libs"
  PERSONAL_PORTS_DIR="${WORK_DIR}/personal-ports"
else
  AUR_DIR="${AUR_DIR:-${HOME}/Code/aur}"
  JSONNET_LIBS_DIR="${JSONNET_LIBS_DIR:-${HOME}/Code/jsonnet-libs}"
  PERSONAL_PORTS_DIR="${PERSONAL_PORTS_DIR:-${HOME}/Code/personal-ports}"
fi

NODEMANAGER_BIN_REMOTE="${NODEMANAGER_BIN_REMOTE:-git@github.com:zachfi/nodemanager-bin.git}"
AUR_REMOTE="${AUR_REMOTE:-git@github.com:zachfi/aur.git}"
JSONNET_LIBS_REMOTE="${JSONNET_LIBS_REMOTE:-git@github.com:zachfi/jsonnet-libs.git}"
PERSONAL_PORTS_REMOTE="${PERSONAL_PORTS_REMOTE:-git@github.com:zachfi/personal-ports.git}"

# ── Version ──────────────────────────────────────────────────────────────────

VERSION="$(git -C "${REPO_ROOT}" describe --tags --exact-match 2>/dev/null || true)"
if [[ -z "${VERSION}" ]]; then
  echo "ERROR: HEAD is not on an exact tag. Tag the commit before running." >&2
  exit 1
fi
VERSION_NO_V="${VERSION#v}"

echo "==> Downstream release: ${VERSION}"
[[ "${DRY_RUN}" == "1" ]] && echo "    (DRY_RUN: git push and docker/make steps will be skipped)"

# ── Git identity (CI-friendly) ────────────────────────────────────────────────

if [[ -n "${GIT_AUTHOR_NAME:-}" ]]; then
  git config --global user.name  "${GIT_AUTHOR_NAME}"
  git config --global user.email "${GIT_AUTHOR_EMAIL:-release@nodemanager}"
fi

# ── HTTPS rewrite for CI (token-based push) ───────────────────────────────────
# When GITHUB_TOKEN is set and CI is detected, rewrite SSH remote URLs so that
# git uses HTTPS + token.  Harmless if SSH agent is active locally.

if [[ -n "${GITHUB_TOKEN:-}" && -n "${CI:-}" ]]; then
  git config --global \
    url."https://${GITHUB_TOKEN}@github.com/".insteadOf "git@github.com:"
fi

# ── Checksums ─────────────────────────────────────────────────────────────────

DEFAULT_CHECKSUMS_URL="https://github.com/zachfi/nodemanager/releases/download/${VERSION}/checksums.txt"
CHECKSUMS_URL="${CHECKSUMS_URL:-${DEFAULT_CHECKSUMS_URL}}"

echo "--> Fetching ${CHECKSUMS_URL}"
CURL_ARGS=(-fsSL)
[[ -n "${GITHUB_TOKEN:-}" ]] && CURL_ARGS+=(-H "Authorization: token ${GITHUB_TOKEN}")
curl "${CURL_ARGS[@]}" "${CHECKSUMS_URL}" -o "${WORK_DIR}/checksums.txt"

SHA_AMD64="$(awk "/nodemanager_${VERSION_NO_V}_linux_amd64\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"
SHA_ARM64="$(awk "/nodemanager_${VERSION_NO_V}_linux_arm64\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"
SHA_ARMV7="$(awk "/nodemanager_${VERSION_NO_V}_linux_armv7\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"

AGENT_SHA_AMD64="$(awk "/nodemanager-agent_${VERSION_NO_V}_linux_amd64\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"
AGENT_SHA_ARM64="$(awk "/nodemanager-agent_${VERSION_NO_V}_linux_arm64\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"
AGENT_SHA_ARMV7="$(awk "/nodemanager-agent_${VERSION_NO_V}_linux_armv7\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"

if [[ -z "${SHA_AMD64}" || -z "${SHA_ARM64}" || -z "${SHA_ARMV7}" ]]; then
  echo "ERROR: one or more nodemanager checksums not found. Contents of checksums.txt:" >&2
  cat "${WORK_DIR}/checksums.txt" >&2
  exit 1
fi

if [[ -z "${AGENT_SHA_AMD64}" || -z "${AGENT_SHA_ARM64}" || -z "${AGENT_SHA_ARMV7}" ]]; then
  echo "ERROR: one or more nodemanager-agent checksums not found. Contents of checksums.txt:" >&2
  cat "${WORK_DIR}/checksums.txt" >&2
  exit 1
fi

echo "    nodemanager:"
echo "      amd64:  ${SHA_AMD64}"
echo "      arm64:  ${SHA_ARM64}"
echo "      armv7h: ${SHA_ARMV7}"
echo "    nodemanager-agent:"
echo "      amd64:  ${AGENT_SHA_AMD64}"
echo "      arm64:  ${AGENT_SHA_ARM64}"
echo "      armv7h: ${AGENT_SHA_ARMV7}"

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [1/4] nodemanager-bin"
# ─────────────────────────────────────────────────────────────────────────────

BIN_DIR="${WORK_DIR}/nodemanager-bin"
git clone "${NODEMANAGER_BIN_REMOTE}" "${BIN_DIR}"

# Render version + checksums into PKGBUILD
sed "s/{{ version }}/${VERSION_NO_V}/" "${BIN_DIR}/PKGBUILD.template" > "${BIN_DIR}/PKGBUILD"
sed -i "s|sha256sums_x86_64=('SKIP' 'SKIP')|sha256sums_x86_64=('${SHA_AMD64}' '${AGENT_SHA_AMD64}')|"   "${BIN_DIR}/PKGBUILD"
sed -i "s|sha256sums_aarch64=('SKIP' 'SKIP')|sha256sums_aarch64=('${SHA_ARM64}' '${AGENT_SHA_ARM64}')|" "${BIN_DIR}/PKGBUILD"
sed -i "s|sha256sums_armv7h=('SKIP' 'SKIP')|sha256sums_armv7h=('${SHA_ARMV7}' '${AGENT_SHA_ARMV7}')|"   "${BIN_DIR}/PKGBUILD"

echo "--> PKGBUILD for ${VERSION_NO_V}:"
grep -E "^pkgver|sha256" "${BIN_DIR}/PKGBUILD"

git -C "${BIN_DIR}" add PKGBUILD
if git -C "${BIN_DIR}" diff --staged --quiet; then
  echo "    PKGBUILD already at ${VERSION_NO_V} — nothing to commit"
else
  git -C "${BIN_DIR}" commit -m "Update nodemanager to ${VERSION_NO_V}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push nodemanager-bin"
  else
    git -C "${BIN_DIR}" push origin main
    echo "    nodemanager-bin pushed"
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [2/4] aur submodule"
# ─────────────────────────────────────────────────────────────────────────────

if [[ ! -d "${AUR_DIR}/.git" ]]; then
  git clone "${AUR_REMOTE}" "${AUR_DIR}"
  git -C "${AUR_DIR}" submodule update --init --recursive
fi

git -C "${AUR_DIR}" submodule update --remote nodemanager-bin
git -C "${AUR_DIR}" add nodemanager-bin

if git -C "${AUR_DIR}" diff --staged --quiet; then
  echo "    nodemanager-bin submodule already at latest — nothing to commit"
else
  git -C "${AUR_DIR}" commit -m "Update nodemanager-bin to ${VERSION}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push aur"
  else
    git -C "${AUR_DIR}" push
    echo "    aur pushed (Woodpecker will rebuild pacman repo image)"
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [3/4] jsonnet-libs"
# ─────────────────────────────────────────────────────────────────────────────

if [[ ! -d "${JSONNET_LIBS_DIR}/.git" ]]; then
  git clone "${JSONNET_LIBS_REMOTE}" "${JSONNET_LIBS_DIR}"
fi

CONFIG="${JSONNET_LIBS_DIR}/libs/nodemanager/config.jsonnet"

if grep -qF "'${VERSION_NO_V}'" "${CONFIG}"; then
  echo "    ${VERSION_NO_V} already present in config.jsonnet — skipping add"
else
  # Insert new version at the top of the versions array
  sed -i "s|local versions = \[|local versions = [\n  '${VERSION_NO_V}',|" "${CONFIG}"
  echo "    Added ${VERSION_NO_V} to config.jsonnet"
fi

if [[ "${DRY_RUN}" == "1" ]]; then
  echo "    DRY_RUN: would run: make -C ${JSONNET_LIBS_DIR} libs/nodemanager"
else
  # Regenerate CRD libsonnet (runs Docker image k8s-gen)
  make -C "${JSONNET_LIBS_DIR}" libs/nodemanager OUTPUT_DIR="${JSONNET_LIBS_DIR}/gen"

  GEN_DIR="${JSONNET_LIBS_DIR}/gen/nodemanager-libsonnet/${VERSION_NO_V}"
  if [[ ! -d "${GEN_DIR}" ]]; then
    echo "ERROR: expected generated output at ${GEN_DIR} but it does not exist." >&2
    exit 1
  fi

  git -C "${JSONNET_LIBS_DIR}" add "${CONFIG}" "${GEN_DIR}"
fi

git -C "${JSONNET_LIBS_DIR}" add "${CONFIG}"

if git -C "${JSONNET_LIBS_DIR}" diff --staged --quiet; then
  echo "    No changes to commit in jsonnet-libs"
else
  git -C "${JSONNET_LIBS_DIR}" commit -m "Add nodemanager ${VERSION} CRD libsonnet"
  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push jsonnet-libs"
  else
    git -C "${JSONNET_LIBS_DIR}" push
    echo "    jsonnet-libs pushed"
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [4/4] personal-ports (FreeBSD)"
# ─────────────────────────────────────────────────────────────────────────────
# Computes distinfo by fetching from the Go module proxy and GitHub — no
# FreeBSD toolchain required.  Build validation (poudriere) is left to a
# dedicated FreeBSD Woodpecker runner once one is available.

PORT_SUBDIR="sysutils/nodemanager"
GH_ACCOUNT="zachfi"
GO_MODULE="github.com/${GH_ACCOUNT}/nodemanager"
# distinfo path prefix matches the FreeBSD ports convention:
#   go/<CATEGORIES>_<PORTNAME>/<GH_ACCOUNT>-<GH_PROJECT>-<DISTVERSIONPREFIX><VER>_GH0/
DIST_PREFIX="go/sysutils_nodemanager/${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0"

if [[ ! -d "${PERSONAL_PORTS_DIR}/.git" ]]; then
  git clone "${PERSONAL_PORTS_REMOTE}" "${PERSONAL_PORTS_DIR}"
fi

PORT_DIR="${PERSONAL_PORTS_DIR}/${PORT_SUBDIR}"

# Check if already at this version
CURRENT_VER="$(grep '^PORTVERSION' "${PORT_DIR}/Makefile" | awk '{print $NF}')"
if [[ "${CURRENT_VER}" == "${VERSION_NO_V}" ]]; then
  echo "    ${VERSION_NO_V} already set in port Makefile — skipping"
else
  # Bump PORTVERSION and reset PORTREVISION
  sed -i "s|^PORTVERSION=.*|PORTVERSION=\t${VERSION_NO_V}|" "${PORT_DIR}/Makefile"
  sed -i "s|^PORTREVISION=.*|PORTREVISION=\t0|"             "${PORT_DIR}/Makefile"
  echo "    PORTVERSION → ${VERSION_NO_V}"

  # Fetch distfiles and compute SHA256 + SIZE
  echo "--> Fetching distfiles for ${GO_MODULE}@v${VERSION_NO_V}"

  MOD_FILE="${WORK_DIR}/v${VERSION_NO_V}.mod"
  ZIP_FILE="${WORK_DIR}/v${VERSION_NO_V}.zip"
  TGZ_FILE="${WORK_DIR}/${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0.tar.gz"

  PORT_MOD_URL="${PORT_MOD_URL:-https://raw.githubusercontent.com/${GH_ACCOUNT}/nodemanager/v${VERSION_NO_V}/go.mod}"
  PORT_ZIP_URL="${PORT_ZIP_URL:-https://github.com/${GH_ACCOUNT}/nodemanager/archive/refs/tags/v${VERSION_NO_V}.zip}"
  PORT_TGZ_URL="${PORT_TGZ_URL:-https://github.com/${GH_ACCOUNT}/nodemanager/archive/refs/tags/v${VERSION_NO_V}.tar.gz}"

  curl -fsSL "${PORT_MOD_URL}" -o "${MOD_FILE}"
  curl -fsSL "${PORT_ZIP_URL}" -o "${ZIP_FILE}"
  curl -fsSL "${PORT_TGZ_URL}" -o "${TGZ_FILE}"

  SHA_MOD="$(sha256sum "${MOD_FILE}" | awk '{print $1}')"
  SZ_MOD="$(wc -c < "${MOD_FILE}")"
  SHA_ZIP="$(sha256sum "${ZIP_FILE}" | awk '{print $1}')"
  SZ_ZIP="$(wc -c < "${ZIP_FILE}")"
  SHA_TGZ="$(sha256sum "${TGZ_FILE}" | awk '{print $1}')"
  SZ_TGZ="$(wc -c < "${TGZ_FILE}")"

  echo "    .mod  sha256=${SHA_MOD} size=${SZ_MOD}"
  echo "    .zip  sha256=${SHA_ZIP} size=${SZ_ZIP}"
  echo "    .tar.gz sha256=${SHA_TGZ} size=${SZ_TGZ}"

  # Write distinfo
  cat > "${PORT_DIR}/distinfo" <<EOF
TIMESTAMP = $(date +%s)
SHA256 (${DIST_PREFIX}/v${VERSION_NO_V}.mod) = ${SHA_MOD}
SIZE (${DIST_PREFIX}/v${VERSION_NO_V}.mod) = ${SZ_MOD}
SHA256 (${DIST_PREFIX}/v${VERSION_NO_V}.zip) = ${SHA_ZIP}
SIZE (${DIST_PREFIX}/v${VERSION_NO_V}.zip) = ${SZ_ZIP}
SHA256 (${DIST_PREFIX}/${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0.tar.gz) = ${SHA_TGZ}
SIZE (${DIST_PREFIX}/${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0.tar.gz) = ${SZ_TGZ}
EOF

  git -C "${PERSONAL_PORTS_DIR}" add "${PORT_DIR}/Makefile" "${PORT_DIR}/distinfo"
  git -C "${PERSONAL_PORTS_DIR}" commit \
    -m "chore: update sysutils/nodemanager/ for ${VERSION}"

  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push personal-ports"
  else
    git -C "${PERSONAL_PORTS_DIR}" push
    echo "    personal-ports pushed"
    echo "    NOTE: poudriere build validation requires a FreeBSD Woodpecker runner"
  fi
fi

echo ""
echo "==> Downstream release complete for ${VERSION}"
