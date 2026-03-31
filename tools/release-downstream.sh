#!/usr/bin/env bash
# tools/release-downstream.sh
#
# Propagates a nodemanager release to downstream repos:
#   1. nodemanager-bin  — renders PKGBUILD with new version + checksums, commits, pushes
#   2. aur              — updates nodemanager-bin submodule, commits, pushes
#   3. jsonnet-libs     — adds new version to CRD config, regenerates libsonnet, commits, pushes
#
# Run from the repo root after `make release` has published binaries to GitHub.
#
# Required:
#   git, curl, awk, sed, make, docker (for jsonnet-libs step)
#
# Environment:
#   GITHUB_TOKEN      — used to fetch release assets; required if repo is private
#   AUR_DIR           — path to the aur checkout (default: ~/Code/aur)
#   JSONNET_LIBS_DIR  — path to jsonnet-libs checkout (default: ~/Code/jsonnet-libs)
#   GIT_AUTHOR_NAME   — overrides git commit author name (useful in CI)
#   GIT_AUTHOR_EMAIL  — overrides git commit author email (useful in CI)

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# In CI, clone repos fresh into WORK_DIR. Locally, default to known checkout paths.
if [[ -n "${CI:-}" ]]; then
  AUR_DIR="${WORK_DIR}/aur"
  JSONNET_LIBS_DIR="${WORK_DIR}/jsonnet-libs"
else
  AUR_DIR="${AUR_DIR:-${HOME}/Code/aur}"
  JSONNET_LIBS_DIR="${JSONNET_LIBS_DIR:-${HOME}/Code/jsonnet-libs}"
fi

# ── Version ──────────────────────────────────────────────────────────────────

VERSION="$(git -C "${REPO_ROOT}" describe --tags --exact-match 2>/dev/null || true)"
if [[ -z "${VERSION}" ]]; then
  echo "ERROR: HEAD is not on an exact tag. Tag the commit before running." >&2
  exit 1
fi
VERSION_NO_V="${VERSION#v}"

echo "==> Downstream release: ${VERSION}"

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

CHECKSUMS_URL="https://github.com/zachfi/nodemanager/releases/download/${VERSION}/checksums.txt"

echo "--> Fetching ${CHECKSUMS_URL}"
CURL_ARGS=(-fsSL)
[[ -n "${GITHUB_TOKEN:-}" ]] && CURL_ARGS+=(-H "Authorization: token ${GITHUB_TOKEN}")
curl "${CURL_ARGS[@]}" "${CHECKSUMS_URL}" -o "${WORK_DIR}/checksums.txt"

SHA_AMD64="$(awk "/nodemanager_${VERSION_NO_V}_linux_amd64\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"
SHA_ARM64="$(awk "/nodemanager_${VERSION_NO_V}_linux_arm64\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"
SHA_ARMV7="$(awk "/nodemanager_${VERSION_NO_V}_linux_armv7\\.tar\\.gz/{print \$1}" "${WORK_DIR}/checksums.txt")"

if [[ -z "${SHA_AMD64}" || -z "${SHA_ARM64}" || -z "${SHA_ARMV7}" ]]; then
  echo "ERROR: one or more checksums not found. Contents of checksums.txt:" >&2
  cat "${WORK_DIR}/checksums.txt" >&2
  exit 1
fi

echo "    amd64:  ${SHA_AMD64}"
echo "    arm64:  ${SHA_ARM64}"
echo "    armv7h: ${SHA_ARMV7}"

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [1/3] nodemanager-bin"
# ─────────────────────────────────────────────────────────────────────────────

BIN_DIR="${WORK_DIR}/nodemanager-bin"
git clone git@github.com:zachfi/nodemanager-bin.git "${BIN_DIR}"

# Render version + checksums into PKGBUILD
sed "s/{{ version }}/${VERSION_NO_V}/" "${BIN_DIR}/PKGBUILD.template" > "${BIN_DIR}/PKGBUILD"
sed -i "s|sha256sums_x86_64=('[^']*')|sha256sums_x86_64=('${SHA_AMD64}')|"   "${BIN_DIR}/PKGBUILD"
sed -i "s|sha256sums_aarch64=('[^']*')|sha256sums_aarch64=('${SHA_ARM64}')|" "${BIN_DIR}/PKGBUILD"
sed -i "s|sha256sums_armv7h=('[^']*')|sha256sums_armv7h=('${SHA_ARMV7}')|"   "${BIN_DIR}/PKGBUILD"

echo "--> PKGBUILD for ${VERSION_NO_V}:"
grep -E "^pkgver|sha256" "${BIN_DIR}/PKGBUILD"

git -C "${BIN_DIR}" add PKGBUILD
git -C "${BIN_DIR}" commit -m "Update nodemanager to ${VERSION_NO_V}"
git -C "${BIN_DIR}" push origin main

echo "    nodemanager-bin pushed"

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [2/3] aur submodule"
# ─────────────────────────────────────────────────────────────────────────────

if [[ ! -d "${AUR_DIR}/.git" ]]; then
  git clone git@github.com:zachfi/aur.git "${AUR_DIR}"
  git -C "${AUR_DIR}" submodule update --init --recursive
fi

git -C "${AUR_DIR}" submodule update --remote nodemanager-bin
git -C "${AUR_DIR}" add nodemanager-bin

if git -C "${AUR_DIR}" diff --staged --quiet; then
  echo "    nodemanager-bin submodule already at latest — nothing to commit"
else
  git -C "${AUR_DIR}" commit -m "Update nodemanager-bin to ${VERSION}"
  git -C "${AUR_DIR}" push
  echo "    aur pushed (Woodpecker will rebuild pacman repo image)"
fi

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [3/3] jsonnet-libs"
# ─────────────────────────────────────────────────────────────────────────────

if [[ ! -d "${JSONNET_LIBS_DIR}/.git" ]]; then
  git clone git@github.com:zachfi/jsonnet-libs.git "${JSONNET_LIBS_DIR}"
fi

CONFIG="${JSONNET_LIBS_DIR}/libs/nodemanager/config.jsonnet"

if grep -qF "'${VERSION_NO_V}'" "${CONFIG}"; then
  echo "    ${VERSION_NO_V} already present in config.jsonnet — skipping add"
else
  # Insert new version at the top of the versions array
  sed -i "s|local versions = \[|local versions = [\n  '${VERSION_NO_V}',|" "${CONFIG}"
  echo "    Added ${VERSION_NO_V} to config.jsonnet"
fi

# Regenerate CRD libsonnet (runs Docker image k8s-gen)
make -C "${JSONNET_LIBS_DIR}" libs/nodemanager

GEN_DIR="${JSONNET_LIBS_DIR}/gen/nodemanager-libsonnet/${VERSION_NO_V}"
if [[ ! -d "${GEN_DIR}" ]]; then
  echo "ERROR: expected generated output at ${GEN_DIR} but it does not exist." >&2
  exit 1
fi

git -C "${JSONNET_LIBS_DIR}" add "${CONFIG}" "${GEN_DIR}"

if git -C "${JSONNET_LIBS_DIR}" diff --staged --quiet; then
  echo "    No changes to commit in jsonnet-libs"
else
  git -C "${JSONNET_LIBS_DIR}" commit -m "Add nodemanager ${VERSION} CRD libsonnet"
  git -C "${JSONNET_LIBS_DIR}" push
  echo "    jsonnet-libs pushed"
fi

echo ""
echo "==> Downstream release complete for ${VERSION}"
