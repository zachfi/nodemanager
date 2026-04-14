#!/usr/bin/env bash
# tools/test-release-downstream.sh
#
# Integration test for tools/release-downstream.sh.
#
# Sets up local bare git repos mimicking nodemanager-bin, aur (with
# nodemanager-bin as a submodule), jsonnet-libs, and personal-ports.  Runs the
# release script in DRY_RUN mode against fake fixtures and asserts the outputs.
#
# The jsonnet Docker step is skipped (DRY_RUN=1); the test validates every
# other operation: awk checksum extraction, PKGBUILD templating, sed on
# config.jsonnet, distinfo generation, git commit creation, and idempotency.
#
# Usage:
#   bash tools/test-release-downstream.sh
#   bash tools/test-release-downstream.sh -v   # verbose (show script output)

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
SCRIPT="${REPO_ROOT}/tools/release-downstream.sh"
VERBOSE=0
[[ "${1:-}" == "-v" ]] && VERBOSE=1

# ── Helpers ───────────────────────────────────────────────────────────────────

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; (( PASS++ )) || true; }
fail() { echo "  FAIL: $1"; (( FAIL++ )) || true; }

assert_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -qF "${pattern}" "${file}"; then
    pass "${label}"
  else
    fail "${label} (expected '${pattern}' in ${file})"
    echo "       actual content:"
    cat "${file}" | sed 's/^/       /'
  fi
}

assert_not_contains() {
  local file="$1" pattern="$2" label="$3"
  if ! grep -qF "${pattern}" "${file}"; then
    pass "${label}"
  else
    fail "${label} (did not expect '${pattern}' in ${file})"
  fi
}

run_script() {
  local workdir="$1" outfile="$2" mode="$3"  # mode: append or overwrite
  local tmp_out="${TMP}/run_tmp.log"
  set +e
  env \
    DRY_RUN=1 \
    WORK_DIR="${workdir}" \
    WORK_DIR_EXTERNAL=1 \
    NODEMANAGER_BIN_REMOTE="${BIN_BARE}" \
    AUR_REMOTE="${AUR_BARE}" \
    AUR_DIR="${AUR_WORK}" \
    JSONNET_LIBS_REMOTE="${JL_BARE}" \
    JSONNET_LIBS_DIR="${JL_WORK}" \
    PERSONAL_PORTS_REMOTE="${PP_BARE}" \
    PERSONAL_PORTS_DIR="${PP_WORK}" \
    CHECKSUMS_URL="file://${CHECKSUMS_FILE}" \
    PORT_MOD_URL="file://${FAKE_MOD_FILE}" \
    PORT_ZIP_URL="file://${FAKE_ZIP_FILE}" \
    PORT_TGZ_URL="file://${FAKE_TGZ_FILE}" \
    GIT_AUTHOR_NAME="Test" \
    GIT_AUTHOR_EMAIL="test@test" \
    bash "${SCRIPT}" > "${tmp_out}" 2>&1
  LAST_EXIT=$?
  set -e
  if [[ "${mode}" == "append" ]]; then
    cat "${tmp_out}" >> "${outfile}"
  else
    cat "${tmp_out}" > "${outfile}"
  fi
}

# ── Setup ─────────────────────────────────────────────────────────────────────

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# Disable GPG signing for all git operations in this test process and any
# subprocesses (fixture setup commits + the release script itself).
export GIT_CONFIG_GLOBAL="${TMP}/gitconfig"
printf '[commit]\n\tgpgsign = false\n[protocol "file"]\n\tallow = always\n[user]\n\tname = Test\n\temail = test@test\n[init]\n\tdefaultBranch = main\n' \
  > "${TMP}/gitconfig"

TEST_VERSION="v9.9.9"
TEST_VERSION_NO_V="9.9.9"
FAKE_SHA_AMD64="aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
FAKE_SHA_ARM64="bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"
FAKE_SHA_ARMV7="cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333"
FAKE_AGENT_SHA_AMD64="dddd4444dddd4444dddd4444dddd4444dddd4444dddd4444dddd4444dddd4444"
FAKE_AGENT_SHA_ARM64="eeee5555eeee5555eeee5555eeee5555eeee5555eeee5555eeee5555eeee5555"
FAKE_AGENT_SHA_ARMV7="ffff6666ffff6666ffff6666ffff6666ffff6666ffff6666ffff6666ffff6666"

echo "==> Setting up test fixtures in ${TMP}"

# ── Fake checksums.txt ────────────────────────────────────────────────────────

CHECKSUMS_FILE="${TMP}/checksums.txt"
cat > "${CHECKSUMS_FILE}" <<EOF
${FAKE_SHA_AMD64}  nodemanager_${TEST_VERSION_NO_V}_linux_amd64.tar.gz
${FAKE_SHA_ARM64}  nodemanager_${TEST_VERSION_NO_V}_linux_arm64.tar.gz
${FAKE_SHA_ARMV7}  nodemanager_${TEST_VERSION_NO_V}_linux_armv7.tar.gz
deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  nodemanager_${TEST_VERSION_NO_V}_freebsd_amd64.tar.gz
${FAKE_AGENT_SHA_AMD64}  nodemanager-agent_${TEST_VERSION_NO_V}_linux_amd64.tar.gz
${FAKE_AGENT_SHA_ARM64}  nodemanager-agent_${TEST_VERSION_NO_V}_linux_arm64.tar.gz
${FAKE_AGENT_SHA_ARMV7}  nodemanager-agent_${TEST_VERSION_NO_V}_linux_armv7.tar.gz
EOF

# ── Fake port distfiles (stand-ins for Go proxy + GitHub) ────────────────────

FAKE_MOD_FILE="${TMP}/fake-v${TEST_VERSION_NO_V}.mod"
FAKE_ZIP_FILE="${TMP}/fake-v${TEST_VERSION_NO_V}.zip"
FAKE_TGZ_FILE="${TMP}/fake-v${TEST_VERSION_NO_V}.tar.gz"
echo "module github.com/zachfi/nodemanager" > "${FAKE_MOD_FILE}"
echo "fake zip content"                     > "${FAKE_ZIP_FILE}"
echo "fake tarball content"                 > "${FAKE_TGZ_FILE}"

EXPECTED_SHA_MOD="$(sha256sum "${FAKE_MOD_FILE}" | awk '{print $1}')"
EXPECTED_SZ_MOD="$(wc -c < "${FAKE_MOD_FILE}")"
EXPECTED_SHA_ZIP="$(sha256sum "${FAKE_ZIP_FILE}" | awk '{print $1}')"
EXPECTED_SZ_ZIP="$(wc -c < "${FAKE_ZIP_FILE}")"
EXPECTED_SHA_TGZ="$(sha256sum "${FAKE_TGZ_FILE}" | awk '{print $1}')"
EXPECTED_SZ_TGZ="$(wc -c < "${FAKE_TGZ_FILE}")"

# ── nodemanager-bin bare repo ─────────────────────────────────────────────────

BIN_BARE="${TMP}/nodemanager-bin.git"
BIN_WORK="${TMP}/nodemanager-bin-work"
git init --bare -b main "${BIN_BARE}" -q
git init -b main "${BIN_WORK}" -q
git -C "${BIN_WORK}" config user.email "test@test"
git -C "${BIN_WORK}" config user.name "Test"

# Copy PKGBUILD.template and nodemanager.service from the real nodemanager-bin
# checkout if present; otherwise create minimal stubs.
NM_BIN_SRC="${HOME}/go/src/github.com/zachfi/nodemanager-bin"
if [[ -f "${NM_BIN_SRC}/PKGBUILD.template" ]]; then
  cp "${NM_BIN_SRC}/PKGBUILD.template" "${BIN_WORK}/PKGBUILD.template"
else
  cat > "${BIN_WORK}/PKGBUILD.template" <<'TMPL'
pkgname=nodemanager-bin
pkgver={{ version }}
pkgrel=1
arch=(aarch64 armv7h x86_64)
source_x86_64=("https://example.com/nodemanager_${pkgver}_linux_amd64.tar.gz")
source_aarch64=("https://example.com/nodemanager_${pkgver}_linux_arm64.tar.gz")
source_armv7h=("https://example.com/nodemanager_${pkgver}_linux_armv7.tar.gz")
sha256sums_x86_64=('SKIP' 'SKIP')
sha256sums_aarch64=('SKIP' 'SKIP')
sha256sums_armv7h=('SKIP' 'SKIP')
TMPL
fi

if [[ -f "${NM_BIN_SRC}/nodemanager.service" ]]; then
  cp "${NM_BIN_SRC}/nodemanager.service" "${BIN_WORK}/nodemanager.service"
else
  echo "[Unit]" > "${BIN_WORK}/nodemanager.service"
fi

git -C "${BIN_WORK}" add .
git -C "${BIN_WORK}" commit -m "initial" -q
git -C "${BIN_WORK}" remote add origin "${BIN_BARE}"
git -C "${BIN_WORK}" push origin HEAD:main -q

# ── jsonnet-libs bare repo ────────────────────────────────────────────────────

JL_BARE="${TMP}/jsonnet-libs.git"
JL_WORK="${TMP}/jsonnet-libs-work"
git init --bare "${JL_BARE}" -q
git init "${JL_WORK}" -q
git -C "${JL_WORK}" config user.email "test@test"
git -C "${JL_WORK}" config user.name "Test"

mkdir -p "${JL_WORK}/libs/nodemanager"
cat > "${JL_WORK}/libs/nodemanager/config.jsonnet" <<'JSONNET'
local versions = [
  '0.6.0',
  '0.5.21',
];

// placeholder config
versions
JSONNET

git -C "${JL_WORK}" add .
git -C "${JL_WORK}" commit -m "initial" -q
git -C "${JL_WORK}" remote add origin "${JL_BARE}"
git -C "${JL_WORK}" push origin HEAD:main -q

# ── aur bare repo (nodemanager-bin as submodule) ──────────────────────────────

AUR_BARE="${TMP}/aur.git"
AUR_WORK="${TMP}/aur-work"
git init --bare "${AUR_BARE}" -q
git init "${AUR_WORK}" -q
git -C "${AUR_WORK}" config user.email "test@test"
git -C "${AUR_WORK}" config user.name "Test"

git -C "${AUR_WORK}" -c protocol.file.allow=always submodule add -q "${BIN_BARE}" nodemanager-bin
git -C "${AUR_WORK}" commit -m "initial" -q
git -C "${AUR_WORK}" remote add origin "${AUR_BARE}"
git -C "${AUR_WORK}" push origin HEAD:main -q

# ── personal-ports bare repo ──────────────────────────────────────────────────

PP_BARE="${TMP}/personal-ports.git"
PP_WORK="${TMP}/personal-ports-work"
git init --bare "${PP_BARE}" -q
git init "${PP_WORK}" -q
git -C "${PP_WORK}" config user.email "test@test"
git -C "${PP_WORK}" config user.name "Test"

mkdir -p "${PP_WORK}/sysutils/nodemanager"

# Copy real port files if available, otherwise use stubs
PP_SRC="${HOME}/Code/personal-ports/sysutils/nodemanager"
if [[ -f "${PP_SRC}/Makefile" ]]; then
  cp "${PP_SRC}/Makefile"  "${PP_WORK}/sysutils/nodemanager/Makefile"
  cp "${PP_SRC}/distinfo"  "${PP_WORK}/sysutils/nodemanager/distinfo"
  cp "${PP_SRC}/pkg-descr" "${PP_WORK}/sysutils/nodemanager/pkg-descr"
else
  cat > "${PP_WORK}/sysutils/nodemanager/Makefile" <<'MK'
PORTNAME=	nodemanager
PORTVERSION=	0.6.5
PORTREVISION=	0
DISTVERSIONPREFIX=v
CATEGORIES=	sysutils
MAINTAINER=	contact@zach.fi
COMMENT=	A Kubernetes controller for node management
LICENSE=	MIT
USES=		go:1.26,modules
USE_GITHUB=	yes
GH_ACCOUNT=	zachfi
GH_PROJECT=	nodemanager
GO_MODULE=	github.com/zachfi/nodemanager
.include <bsd.port.mk>
MK
  echo "old distinfo" > "${PP_WORK}/sysutils/nodemanager/distinfo"
  echo "nodemanager port" > "${PP_WORK}/sysutils/nodemanager/pkg-descr"
fi

git -C "${PP_WORK}" add .
git -C "${PP_WORK}" commit -m "initial" -q
git -C "${PP_WORK}" remote add origin "${PP_BARE}"
git -C "${PP_WORK}" push origin HEAD:main -q

# ── Tag the nodemanager repo ──────────────────────────────────────────────────

EXISTING_TAG="$(git -C "${REPO_ROOT}" tag -l "${TEST_VERSION}")"
if [[ -z "${EXISTING_TAG}" ]]; then
  git -C "${REPO_ROOT}" tag "${TEST_VERSION}" -m "test tag"
  CLEANUP_TAG=1
else
  CLEANUP_TAG=0
fi
trap 'rm -rf "${TMP}"; [[ "${CLEANUP_TAG}" == "1" ]] && git -C "${REPO_ROOT}" tag -d "${TEST_VERSION}" 2>/dev/null || true' EXIT

# ── Run the script ────────────────────────────────────────────────────────────

echo ""
echo "==> Running release-downstream.sh (DRY_RUN=1)"

SCRIPT_WORK_DIR="${TMP}/script-workdir"
mkdir -p "${SCRIPT_WORK_DIR}"

RUN_OUTPUT="${TMP}/run.log"
run_script "${SCRIPT_WORK_DIR}" "${RUN_OUTPUT}" "overwrite"

if [[ "${VERBOSE}" == "1" ]]; then
  echo "--- script output ---"
  cat "${RUN_OUTPUT}"
  echo "---------------------"
fi

if [[ "${LAST_EXIT}" != "0" ]]; then
  echo "FAIL: script exited with code ${LAST_EXIT}"
  cat "${RUN_OUTPUT}"
  exit 1
fi

# ── Assertions ────────────────────────────────────────────────────────────────

echo ""
echo "==> Assertions"

VERIFY_WORK="${SCRIPT_WORK_DIR}/nodemanager-bin"

# PKGBUILD version
assert_contains "${VERIFY_WORK}/PKGBUILD" "pkgver=${TEST_VERSION_NO_V}" "PKGBUILD: pkgver updated"

# Checksums
assert_contains "${VERIFY_WORK}/PKGBUILD" "sha256sums_x86_64=('${FAKE_SHA_AMD64}' '${FAKE_AGENT_SHA_AMD64}')" "PKGBUILD: sha256sums_x86_64 correct"
assert_contains "${VERIFY_WORK}/PKGBUILD" "sha256sums_aarch64=('${FAKE_SHA_ARM64}' '${FAKE_AGENT_SHA_ARM64}')" "PKGBUILD: sha256sums_aarch64 correct"
assert_contains "${VERIFY_WORK}/PKGBUILD" "sha256sums_armv7h=('${FAKE_SHA_ARMV7}' '${FAKE_AGENT_SHA_ARMV7}')" "PKGBUILD: sha256sums_armv7h correct"

# Template placeholder should be gone
assert_not_contains "${VERIFY_WORK}/PKGBUILD" "{{ version }}" "PKGBUILD: no leftover template tokens"

# config.jsonnet new version inserted
assert_contains "${JL_WORK}/libs/nodemanager/config.jsonnet" "'${TEST_VERSION_NO_V}'" "config.jsonnet: new version present"

# config.jsonnet existing versions preserved
assert_contains "${JL_WORK}/libs/nodemanager/config.jsonnet" "'0.6.0'" "config.jsonnet: previous version 0.6.0 preserved"
assert_contains "${JL_WORK}/libs/nodemanager/config.jsonnet" "'0.5.21'" "config.jsonnet: previous version 0.5.21 preserved"

# config.jsonnet was committed
if git -C "${JL_WORK}" diff --quiet HEAD; then
  pass "config.jsonnet: changes committed"
else
  fail "config.jsonnet: working tree not clean after run"
fi

# port: PORTVERSION bumped
assert_contains "${PP_WORK}/sysutils/nodemanager/Makefile" "PORTVERSION=	${TEST_VERSION_NO_V}" "port: PORTVERSION updated"

# port: PORTREVISION reset
assert_contains "${PP_WORK}/sysutils/nodemanager/Makefile" "PORTREVISION=	0" "port: PORTREVISION reset to 0"

# port: distinfo path format correct
DIST_PREFIX="go/sysutils_nodemanager/zachfi-nodemanager-v${TEST_VERSION_NO_V}_GH0"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "${DIST_PREFIX}/v${TEST_VERSION_NO_V}.mod" "port: distinfo .mod path correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "${DIST_PREFIX}/v${TEST_VERSION_NO_V}.zip" "port: distinfo .zip path correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "${DIST_PREFIX}/zachfi-nodemanager-v${TEST_VERSION_NO_V}_GH0.tar.gz" "port: distinfo .tar.gz path correct"

# port: distinfo checksums match what we fed in
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "SHA256 (${DIST_PREFIX}/v${TEST_VERSION_NO_V}.mod) = ${EXPECTED_SHA_MOD}" "port: distinfo .mod sha256 correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "SIZE (${DIST_PREFIX}/v${TEST_VERSION_NO_V}.mod) = ${EXPECTED_SZ_MOD}" "port: distinfo .mod size correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "SHA256 (${DIST_PREFIX}/v${TEST_VERSION_NO_V}.zip) = ${EXPECTED_SHA_ZIP}" "port: distinfo .zip sha256 correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "SIZE (${DIST_PREFIX}/v${TEST_VERSION_NO_V}.zip) = ${EXPECTED_SZ_ZIP}" "port: distinfo .zip size correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "SHA256 (${DIST_PREFIX}/zachfi-nodemanager-v${TEST_VERSION_NO_V}_GH0.tar.gz) = ${EXPECTED_SHA_TGZ}" "port: distinfo .tar.gz sha256 correct"
assert_contains "${PP_WORK}/sysutils/nodemanager/distinfo" "SIZE (${DIST_PREFIX}/zachfi-nodemanager-v${TEST_VERSION_NO_V}_GH0.tar.gz) = ${EXPECTED_SZ_TGZ}" "port: distinfo .tar.gz size correct"

# port: changes were committed
if git -C "${PP_WORK}" diff --quiet HEAD; then
  pass "port: Makefile + distinfo committed"
else
  fail "port: working tree not clean after run"
fi

# ── Idempotency: run again, expect no second commits ─────────────────────────

echo ""
echo "==> Idempotency check (second run)"

JL_COMMIT_BEFORE="$(git -C "${JL_WORK}" rev-parse HEAD)"
PP_COMMIT_BEFORE="$(git -C "${PP_WORK}" rev-parse HEAD)"

SCRIPT_WORK_DIR_2="${TMP}/script-workdir-2"
mkdir -p "${SCRIPT_WORK_DIR_2}"

run_script "${SCRIPT_WORK_DIR_2}" "${RUN_OUTPUT}" "append"

if [[ "${LAST_EXIT}" != "0" ]]; then
  echo "FAIL: second run exited with code ${LAST_EXIT}"
  cat "${RUN_OUTPUT}"
  exit 1
fi

JL_COMMIT_AFTER="$(git -C "${JL_WORK}" rev-parse HEAD)"
PP_COMMIT_AFTER="$(git -C "${PP_WORK}" rev-parse HEAD)"

if [[ "${JL_COMMIT_BEFORE}" == "${JL_COMMIT_AFTER}" ]]; then
  pass "idempotency: no extra commit in jsonnet-libs"
else
  fail "idempotency: jsonnet-libs got a second commit"
fi

if [[ "${PP_COMMIT_BEFORE}" == "${PP_COMMIT_AFTER}" ]]; then
  pass "idempotency: no extra commit in personal-ports"
else
  fail "idempotency: personal-ports got a second commit"
fi

VERSION_COUNT="$(grep -c "'${TEST_VERSION_NO_V}'" "${JL_WORK}/libs/nodemanager/config.jsonnet" || true)"
if [[ "${VERSION_COUNT}" == "1" ]]; then
  pass "idempotency: version appears exactly once in config.jsonnet"
else
  fail "idempotency: version appears ${VERSION_COUNT} times in config.jsonnet (expected 1)"
fi

# ── Summary ───────────────────────────────────────────────────────────────────

echo ""
echo "==> Results: ${PASS} passed, ${FAIL} failed"
[[ "${FAIL}" == "0" ]] && exit 0 || exit 1
