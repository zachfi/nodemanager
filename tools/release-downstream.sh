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
#   PORT_TGZ_URL              — override port source tarball URL (default: GitHub release archive)

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

NODEMANAGER_BIN_REMOTE="${NODEMANAGER_BIN_REMOTE:-git@code.znet:znet/nodemanager-bin.git}"
AUR_REMOTE="${AUR_REMOTE:-git@code.znet:znet/aur.git}"
JSONNET_LIBS_REMOTE="${JSONNET_LIBS_REMOTE:-git@github.com:zachfi/jsonnet-libs.git}"
PERSONAL_PORTS_REMOTE="${PERSONAL_PORTS_REMOTE:-git@code.znet:znet/personal-ports.git}"

# safe_push <repo_dir> <branch> — pull --rebase from origin then push, so a
# remote that's gained unrelated commits since we last fetched doesn't abort
# the release. Pushes to every remote configured for the branch (e.g. when a
# repo has both a code.znet origin and a github mirror).
safe_push() {
  local dir="$1" branch="$2"
  # --autostash so leftover unstaged changes (e.g. incidental codegen churn)
  # don't abort the rebase. Only committed history is pushed regardless.
  git -C "${dir}" pull --rebase --autostash origin "${branch}"
  while read -r remote; do
    [[ -z "${remote}" ]] && continue
    git -C "${dir}" push "${remote}" "${branch}"
  done < <(git -C "${dir}" remote)
}

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

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [1/4] nodemanager-bin"
# ─────────────────────────────────────────────────────────────────────────────

BIN_DIR="${WORK_DIR}/nodemanager-bin"
git clone "${NODEMANAGER_BIN_REMOTE}" "${BIN_DIR}"

# Render pkgver into PKGBUILD. The build-from-source PKGBUILD compiles via the
# sibling 'nodemanager' submodule in the aur repo, so no per-arch tarball
# checksums are needed here — those live on the GitHub binary-install branch
# (now defunct). sha256sums=('SKIP') in the template covers nodemanager.service
# which is shipped via the source= array.
sed "s/{{ version }}/${VERSION_NO_V}/" "${BIN_DIR}/PKGBUILD.template" > "${BIN_DIR}/PKGBUILD"

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
    safe_push "${BIN_DIR}" main
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

# Bump the 'nodemanager' source submodule to the new tag (PKGBUILD builds from
# this sibling) and the 'nodemanager-bin' PKGBUILD submodule to the commit we
# just pushed in step 1.
git -C "${AUR_DIR}/nodemanager" fetch origin --tags
git -C "${AUR_DIR}/nodemanager" checkout --detach "${VERSION}"
git -C "${AUR_DIR}" submodule update --remote nodemanager-bin
git -C "${AUR_DIR}" add nodemanager nodemanager-bin

if git -C "${AUR_DIR}" diff --staged --quiet; then
  echo "    nodemanager + nodemanager-bin submodules already at ${VERSION} — nothing to commit"
else
  git -C "${AUR_DIR}" commit -m "Bump nodemanager + nodemanager-bin to ${VERSION}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push aur"
  else
    safe_push "${AUR_DIR}" main
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

GEN_DIR="${JSONNET_LIBS_DIR}/gen/nodemanager-libsonnet/${VERSION_NO_V}"

if [[ "${DRY_RUN}" == "1" ]]; then
  echo "    DRY_RUN: would run: make -C ${JSONNET_LIBS_DIR} libs/nodemanager"
elif [[ -d "${GEN_DIR}" ]]; then
  echo "    ${GEN_DIR} already exists — skipping docker codegen"
  git -C "${JSONNET_LIBS_DIR}" add "${CONFIG}" "${GEN_DIR}"
else
  # Regenerate CRD libsonnet (runs Docker image k8s-gen)
  make -C "${JSONNET_LIBS_DIR}" libs/nodemanager OUTPUT_DIR="${JSONNET_LIBS_DIR}/gen"

  if [[ ! -d "${GEN_DIR}" ]]; then
    echo "ERROR: expected generated output at ${GEN_DIR} but it does not exist." >&2
    exit 1
  fi

  git -C "${JSONNET_LIBS_DIR}" add "${CONFIG}" "${GEN_DIR}"
fi

git -C "${JSONNET_LIBS_DIR}" add "${CONFIG}"

# The k8s-gen codegen also reformats previously-generated versions; we commit
# only the new version (the CONFIG + GEN_DIR staged above), so revert that
# incidental churn. Without this the leftover unstaged changes made the
# safe_push 'git pull --rebase' abort with "You have unstaged changes".
git -C "${JSONNET_LIBS_DIR}" checkout -- .

if git -C "${JSONNET_LIBS_DIR}" diff --staged --quiet; then
  echo "    No changes to commit in jsonnet-libs"
else
  git -C "${JSONNET_LIBS_DIR}" commit -m "Add nodemanager ${VERSION} CRD libsonnet"
  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push jsonnet-libs"
  else
    safe_push "${JSONNET_LIBS_DIR}" main
    echo "    jsonnet-libs pushed"
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "==> [4/4] personal-ports (FreeBSD)"
# ─────────────────────────────────────────────────────────────────────────────
# Fetches the GitHub source tarball and computes its checksum. The port builds
# with GOPROXY=off -mod=vendor, so the vendored deps already in the tarball are
# the only distfile required — no proxy.golang.org .mod/.zip needed.

PORT_SUBDIR="sysutils/nodemanager"
GH_ACCOUNT="zachfi"

if [[ ! -d "${PERSONAL_PORTS_DIR}/.git" ]]; then
  git clone "${PERSONAL_PORTS_REMOTE}" "${PERSONAL_PORTS_DIR}"
fi

PORT_DIR="${PERSONAL_PORTS_DIR}/${PORT_SUBDIR}"

CURRENT_VER="$(grep '^PORTVERSION' "${PORT_DIR}/Makefile" | awk '{print $NF}')"
if [[ "${CURRENT_VER}" == "${VERSION_NO_V}" ]]; then
  echo "    ${VERSION_NO_V} already set in port Makefile — skipping"
else
  # Resolve the upstream tag commit so GIT_COMMIT in the port Makefile lines up
  # with the release — `nodemanager version` then prints the real SHA instead
  # of the unexpanded $$Format:%H$$ placeholder from the archive tarball.
  RELEASE_COMMIT="$(git -C "${REPO_ROOT}" rev-list -n1 "${VERSION}")"

  sed -i "s|^PORTVERSION=.*|PORTVERSION=\t${VERSION_NO_V}|" "${PORT_DIR}/Makefile"
  sed -i "s|^PORTREVISION=.*|PORTREVISION=\t0|"             "${PORT_DIR}/Makefile"
  sed -i "s|^GIT_COMMIT=.*|GIT_COMMIT=\t${RELEASE_COMMIT}|" "${PORT_DIR}/Makefile"
  echo "    PORTVERSION → ${VERSION_NO_V}"
  echo "    GIT_COMMIT  → ${RELEASE_COMMIT}"

  # Fetch the GitHub source tarball (vendored deps included) and hash it.
  TGZ_FILE="${WORK_DIR}/${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0.tar.gz"
  PORT_TGZ_URL="${PORT_TGZ_URL:-https://github.com/${GH_ACCOUNT}/nodemanager/archive/refs/tags/v${VERSION_NO_V}.tar.gz}"

  echo "--> Fetching ${PORT_TGZ_URL}"
  curl -fsSL "${PORT_TGZ_URL}" -o "${TGZ_FILE}"

  SHA_TGZ="$(sha256sum "${TGZ_FILE}" | awk '{print $1}')"
  SZ_TGZ="$(wc -c < "${TGZ_FILE}")"

  echo "    .tar.gz sha256=${SHA_TGZ} size=${SZ_TGZ}"

  cat > "${PORT_DIR}/distinfo" <<EOF
TIMESTAMP = $(date +%s)
SHA256 (${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0.tar.gz) = ${SHA_TGZ}
SIZE (${GH_ACCOUNT}-nodemanager-v${VERSION_NO_V}_GH0.tar.gz) = ${SZ_TGZ}
EOF

  git -C "${PERSONAL_PORTS_DIR}" add "${PORT_DIR}/Makefile" "${PORT_DIR}/distinfo"
  git -C "${PERSONAL_PORTS_DIR}" commit \
    -m "chore: update sysutils/nodemanager/ for ${VERSION}"

  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "    DRY_RUN: would push personal-ports"
  else
    safe_push "${PERSONAL_PORTS_DIR}" main
    echo "    personal-ports pushed"
    echo "    NOTE: poudriere build validation requires a FreeBSD Woodpecker runner"
  fi
fi

echo ""
echo "==> Downstream release complete for ${VERSION}"
