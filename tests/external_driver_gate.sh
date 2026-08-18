#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
HELPER="$REPO_ROOT/scripts/provision_external_driver_gate.sh"
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/pgw-external-gate-test.XXXXXX")
FIXTURES="$TEST_ROOT/fixtures"
mkdir "$FIXTURES"

cleanup() {
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

sha256_file() {
  sha256sum "$1" | awk '{print $1}'
}

file_size() {
  if stat -c %s "$1" >/dev/null 2>&1; then stat -c %s "$1"; else stat -f %z "$1"; fi
}

make_tar_fixture() {
  local id=$1
  local root=$2
  local tree="$TEST_ROOT/tree-$id"
  shift 2
  mkdir -p "$tree/$root"
  "$@" "$tree/$root"
  COPYFILE_DISABLE=1 tar -C "$tree" -czf "$FIXTURES/$id.tar.gz" "$root"
}

benchbase_tree() {
  local root=$1
  mkdir -p "$root/.mvn/wrapper"
  printf '<project/>\n' > "$root/pom.xml"
  printf '#!/usr/bin/env sh\nexit 0\n' > "$root/mvnw"
  printf 'fixture\n' > "$root/.mvn/wrapper/maven-wrapper.jar"
  chmod +x "$root/mvnw"
}

sysbench_tree() {
  local root=$1
  mkdir -p "$root/src/lua"
  printf '#!/usr/bin/env sh\nexit 0\n' > "$root/autogen.sh"
  printf 'fixture\n' > "$root/src/lua/oltp_common.lua"
  chmod +x "$root/autogen.sh"
}

hammerdb_tree() {
  local root=$1
  mkdir -p "$root/pylib/tclpy0.4.1"
  printf '#!/usr/bin/env sh\nexit 0\n' > "$root/hammerdbcli"
  printf 'upstream distribution member\n' > "$root/README.md"
  printf 'fixture library\n' > "$root/pylib/tclpy0.4.1/libtclpy0.4.1.so"
  ln -s libtclpy0.4.1.so "$root/pylib/tclpy0.4.1/libtclpy.so"
  ln -s libtclpy.so "$root/pylib/tclpy0.4.1/tclpy.so"
  chmod +x "$root/hammerdbcli"
}

java_tree() {
  local root=$1
  mkdir -p "$root/bin"
  printf '#!/usr/bin/env sh\nexit 0\n' > "$root/bin/java"
  chmod +x "$root/bin/java"
}

make_tar_fixture benchbase-source benchbase-fixture benchbase_tree
make_tar_fixture sysbench-source sysbench-fixture sysbench_tree
make_tar_fixture hammerdb-distribution HammerDB-fixture hammerdb_tree
make_tar_fixture temurin-jdk jdk-fixture java_tree
make_tar_fixture temurin-jre jre-fixture java_tree

maven_tree="$TEST_ROOT/tree-apache-maven"
mkdir -p "$maven_tree/maven-fixture/bin"
printf '#!/usr/bin/env sh\nexit 0\n' > "$maven_tree/maven-fixture/bin/mvn"
chmod +x "$maven_tree/maven-fixture/bin/mvn"
(
  cd "$maven_tree"
  COPYFILE_DISABLE=1 zip -qry "$FIXTURES/apache-maven.zip" maven-fixture
)

FETCHER="$TEST_ROOT/fetcher"
# The generated helper must expand these variables when it runs, not while the
# fixture is created.
# shellcheck disable=SC2016
{
  printf '#!/usr/bin/env bash\n'
  printf 'set -euo pipefail\n'
  printf 'source_file="$PGWORKBENCH_EXTERNAL_TEST_FIXTURES/${1##*/}"\n'
  printf 'test -f "$source_file"\n'
  printf 'cp "$source_file" "$2"\n'
} > "$FETCHER"
chmod +x "$FETCHER"

write_pins() {
  local output=$1
  local hammer_entry="$TEST_ROOT/tree-hammerdb-distribution/HammerDB-fixture/hammerdbcli"
  jq -n \
    --arg bench_sha "$(sha256_file "$FIXTURES/benchbase-source.tar.gz")" \
    --argjson bench_size "$(file_size "$FIXTURES/benchbase-source.tar.gz")" \
    --arg sysbench_sha "$(sha256_file "$FIXTURES/sysbench-source.tar.gz")" \
    --argjson sysbench_size "$(file_size "$FIXTURES/sysbench-source.tar.gz")" \
    --arg hammer_sha "$(sha256_file "$FIXTURES/hammerdb-distribution.tar.gz")" \
    --argjson hammer_size "$(file_size "$FIXTURES/hammerdb-distribution.tar.gz")" \
    --arg hammer_entry_sha "$(sha256_file "$hammer_entry")" \
    --argjson hammer_entry_size "$(file_size "$hammer_entry")" \
    --arg jdk_sha "$(sha256_file "$FIXTURES/temurin-jdk.tar.gz")" \
    --argjson jdk_size "$(file_size "$FIXTURES/temurin-jdk.tar.gz")" \
    --arg jre_sha "$(sha256_file "$FIXTURES/temurin-jre.tar.gz")" \
    --argjson jre_size "$(file_size "$FIXTURES/temurin-jre.tar.gz")" \
    --arg maven_sha "$(sha256_file "$FIXTURES/apache-maven.zip")" \
    --argjson maven_size "$(file_size "$FIXTURES/apache-maven.zip")" \
    '{
      schema_version:"pgworkbench.external-driver-acquisition-pins/v1",
      assets:[
        {id:"benchbase-source",url:"https://offline.test/benchbase-source.tar.gz",sha256:$bench_sha,size_bytes:$bench_size,format:"tar.gz",root:"benchbase-fixture",license_expression:"GPL-3.0-or-later AND Apache-2.0"},
        {id:"sysbench-source",url:"https://offline.test/sysbench-source.tar.gz",sha256:$sysbench_sha,size_bytes:$sysbench_size,format:"tar.gz",root:"sysbench-fixture",license_expression:"GPL-2.0-or-later"},
        {id:"hammerdb-distribution",url:"https://offline.test/hammerdb-distribution.tar.gz",sha256:$hammer_sha,size_bytes:$hammer_size,format:"tar.gz-links",root:"HammerDB-fixture",license_expression:"GPL-3.0-or-later",entrypoint:"hammerdbcli",entrypoint_sha256:$hammer_entry_sha,entrypoint_size_bytes:$hammer_entry_size},
        {id:"temurin-jdk",url:"https://offline.test/temurin-jdk.tar.gz",sha256:$jdk_sha,size_bytes:$jdk_size,format:"tar.gz-links",root:"jdk-fixture",license_expression:"NOASSERTION"},
        {id:"temurin-jre",url:"https://offline.test/temurin-jre.tar.gz",sha256:$jre_sha,size_bytes:$jre_size,format:"tar.gz-links",root:"jre-fixture",license_expression:"NOASSERTION"},
        {id:"apache-maven",url:"https://offline.test/apache-maven.zip",sha256:$maven_sha,size_bytes:$maven_size,format:"zip",root:"maven-fixture",license_expression:"Apache-2.0"}
      ]
    }' > "$output"
}

PINS="$TEST_ROOT/pins.json"
write_pins "$PINS"

run_acquire() {
  local state=$1
  GITHUB_ACTIONS=false PGWORKBENCH_EXTERNAL_OFFLINE_TEST=1 \
  PGWORKBENCH_EXTERNAL_TEST_PINS_FILE="$PINS" \
  PGWORKBENCH_EXTERNAL_TEST_FETCHER="$FETCHER" \
  PGWORKBENCH_EXTERNAL_TEST_FIXTURES="$FIXTURES" \
    "$HELPER" acquire-only --state-dir "$state"
}

good_state="$TEST_ROOT/${STATE_PREFIX:-pgworkbench-external-driver-gate-}good"
run_acquire "$good_state" >/dev/null
hammer_link_root="$good_state/extracted/hammerdb-distribution/HammerDB-fixture/pylib/tclpy0.4.1"
[[ -L "$hammer_link_root/libtclpy.so" && "$(readlink "$hammer_link_root/libtclpy.so")" == libtclpy0.4.1.so ]] ||
  fail 'contained HammerDB link was not preserved'
[[ -L "$hammer_link_root/tclpy.so" && "$(readlink "$hammer_link_root/tclpy.so")" == libtclpy.so ]] ||
  fail 'contained HammerDB symlink chain was not preserved'
jq -e '
  .schema_version == "pgworkbench.external-driver-acquisitions/v1" and
  .project_redistribution == false and .runtime_replay_available == false and
  .complete_license_or_source_closure_attested == false and
  (.assets | length == 6) and
  all(.assets[]; .status == "verified" and
    .dependency_license_closure_asserted == false and
    .project_redistribution == false and .runtime_replay_available == false) and
  (.assets[] | select(.id == "benchbase-source") | .license_expression) == "GPL-3.0-or-later AND Apache-2.0" and
  (.assets[] | select(.id == "hammerdb-distribution") | .license_expression) == "GPL-3.0-or-later" and
  (.assets[] | select(.id == "sysbench-source") | .license_expression) == "GPL-2.0-or-later"
' "$good_state/evidence/acquisitions.json" >/dev/null || fail 'verified acquisition evidence is incomplete'
"$HELPER" cleanup --state-dir "$good_state" >/dev/null
[[ ! -e "$good_state" ]] || fail 'managed state survived cleanup'

# A child member beneath an archive symlink must be rejected before extraction,
# even when both lexical member paths appear to remain under the pinned root.
python3 - "$FIXTURES/hammerdb-distribution.tar.gz" <<'PY'
import io
import sys
import tarfile

with tarfile.open(sys.argv[1], "w:gz") as archive:
    for name, payload, mode in (
        ("HammerDB-fixture/hammerdbcli", b"#!/usr/bin/env sh\nexit 0\n", 0o755),
        ("HammerDB-fixture/safe/real", b"real\n", 0o644),
    ):
        member = tarfile.TarInfo(name)
        member.mode = mode
        member.size = len(payload)
        archive.addfile(member, io.BytesIO(payload))
    link = tarfile.TarInfo("HammerDB-fixture/redirect")
    link.type = tarfile.SYMTYPE
    link.linkname = "safe"
    archive.addfile(link)
    child = tarfile.TarInfo("HammerDB-fixture/redirect/child")
    payload = b"must not extract through link\n"
    child.size = len(payload)
    archive.addfile(child, io.BytesIO(payload))
PY
write_pins "$PINS"
link_child_state="$TEST_ROOT/pgworkbench-external-driver-gate-link-child"
if run_acquire "$link_child_state" >"$TEST_ROOT/link-child.log" 2>&1; then
  fail 'archive member beneath a symlink passed'
fi
grep -Fq 'archive member traverses an archive link' "$TEST_ROOT/link-child.log" ||
  fail 'symlink-child archive failed for the wrong reason'
"$HELPER" cleanup --state-dir "$link_child_state" >/dev/null

# Restore the good HammerDB fixture for the remaining acquisition cases.
COPYFILE_DISABLE=1 tar -C "$TEST_ROOT/tree-hammerdb-distribution" -czf \
  "$FIXTURES/hammerdb-distribution.tar.gz" HammerDB-fixture

printf 'tamper\n' >> "$FIXTURES/benchbase-source.tar.gz"
tamper_state="$TEST_ROOT/pgworkbench-external-driver-gate-tamper"
if run_acquire "$tamper_state" >"$TEST_ROOT/tamper.log" 2>&1; then
  fail 'tampered pinned download passed'
fi
grep -Fq 'archive digest mismatch' "$TEST_ROOT/tamper.log" || fail 'tampered download did not fail on digest'
"$HELPER" cleanup --state-dir "$tamper_state" >/dev/null

# A malicious member must fail before extraction even when its archive digest is
# explicitly present in the offline-only test manifest.
python3 - "$FIXTURES/benchbase-source.tar.gz" <<'PY'
import io
import sys
import tarfile

with tarfile.open(sys.argv[1], "w:gz") as archive:
    payload = b"escape\n"
    member = tarfile.TarInfo("../escape")
    member.size = len(payload)
    archive.addfile(member, io.BytesIO(payload))
PY
write_pins "$PINS"
unsafe_state="$TEST_ROOT/pgworkbench-external-driver-gate-unsafe"
if run_acquire "$unsafe_state" >"$TEST_ROOT/unsafe.log" 2>&1; then
  fail 'unsafe archive path passed'
fi
grep -Fq 'unsafe archive member' "$TEST_ROOT/unsafe.log" || fail 'unsafe archive failed for the wrong reason'
"$HELPER" cleanup --state-dir "$unsafe_state" >/dev/null

# A correctly pinned archive with an incomplete exact layout must also fail;
# digest verification alone is not sufficient acquisition evidence.
layout_tree="$TEST_ROOT/tree-layout"
mkdir -p "$layout_tree/benchbase-fixture"
printf '<project/>\n' > "$layout_tree/benchbase-fixture/pom.xml"
COPYFILE_DISABLE=1 tar -C "$layout_tree" -czf \
  "$FIXTURES/benchbase-source.tar.gz" benchbase-fixture
write_pins "$PINS"
layout_state="$TEST_ROOT/pgworkbench-external-driver-gate-layout"
if run_acquire "$layout_state" >"$TEST_ROOT/layout.log" 2>&1; then
  fail 'incomplete pinned source layout passed'
fi
grep -Fq 'BenchBase source layout is incomplete' "$TEST_ROOT/layout.log" ||
  fail 'incomplete source layout failed for the wrong reason'
"$HELPER" cleanup --state-dir "$layout_state" >/dev/null

override_state="$TEST_ROOT/pgworkbench-external-driver-gate-ci-override"
if GITHUB_ACTIONS=true PGWORKBENCH_EXTERNAL_OFFLINE_TEST=1 \
  PGWORKBENCH_EXTERNAL_TEST_PINS_FILE="$PINS" \
  "$HELPER" acquire-only --state-dir "$override_state" >"$TEST_ROOT/override.log" 2>&1; then
  fail 'GitHub Actions accepted offline test pins'
fi
grep -Fq 'test pin override is forbidden in GitHub Actions' "$TEST_ROOT/override.log" ||
  fail 'GitHub Actions pin override failed for the wrong reason'
"$HELPER" cleanup --state-dir "$override_state" >/dev/null

"$HELPER" validate-configs --repo-root "$REPO_ROOT" >/dev/null
plus_repo="$TEST_ROOT/repo+build"
mkdir "$plus_repo"
cp "$REPO_ROOT/pgworkbench-pack.json" "$plus_repo/"
cp -R "$REPO_ROOT/configs" "$plus_repo/"
"$HELPER" validate-configs --repo-root "$plus_repo" >/dev/null
[[ ! -e "$REPO_ROOT/configs/benchmark-drivers/hammerdb-v6-execute-only.template" ]] ||
  fail 'HammerDB execute-only marker must be ephemeral, not checked in'
if find "$REPO_ROOT/configs/benchmark-drivers" -type f -name '*.tcl' | grep -q .; then
  fail 'release config directory may not retain HammerDB Tcl'
fi
drift_root="$TEST_ROOT/repo-drift"
mkdir -p "$drift_root/configs"
cp "$REPO_ROOT/pgworkbench-pack.json" "$drift_root/"
cp -R "$REPO_ROOT/configs/benchmark-drivers" "$drift_root/configs/"
jq '.tprocc.warehouses = 21' \
  "$drift_root/configs/benchmark-drivers/hammerdb-v6-tprocc-release-smoke.json" \
  > "$TEST_ROOT/drift.json"
mv "$TEST_ROOT/drift.json" \
  "$drift_root/configs/benchmark-drivers/hammerdb-v6-tprocc-release-smoke.json"
if "$HELPER" validate-configs --repo-root "$drift_root" >"$TEST_ROOT/drift.log" 2>&1; then
  fail 'release-only HammerDB configuration drift passed'
fi
grep -Fq '20W/4VU/1m/2m/1M' "$TEST_ROOT/drift.log" || fail 'config drift failed for the wrong reason'

default_pins="$TEST_ROOT/default-pins.json"
"$HELPER" pins > "$default_pins"
jq -e '
  (.assets[] | select(.id == "benchbase-source") | .sha256) == "804c9b3018f2f230f4ebbb5d0ebfed28ca417650037736f13fe9212d406fc4bc" and
  (.assets[] | select(.id == "benchbase-source") | .license_expression) == "GPL-3.0-or-later AND Apache-2.0" and
  (.assets[] | select(.id == "hammerdb-distribution") | .entrypoint_sha256) == "373dee97827a43c1598d7f49f157b7bd2baa10f28c0812d1e93f987c058b6ad4" and
  (.assets[] | select(.id == "hammerdb-distribution") | {sha256,size_bytes,format,root}) == {sha256:"6e0b94724356f35f60760fdcacd0b19de655ec0477383d59e585a7235c4d4a58",size_bytes:36188110,format:"tar.gz-links",root:"HammerDB-6.0"} and
  (.assets[] | select(.id == "hammerdb-distribution") | .license_expression) == "GPL-3.0-or-later" and
  (.assets[] | select(.id == "sysbench-source") | .sha256) == "2a664cb397ebb0678a91d7b876c1ffcebe728a52cbb1ffe0aa63b36fad1c9e1c" and
  (.assets[] | select(.id == "sysbench-source") | .license_expression) == "GPL-2.0-or-later" and
  (.assets[] | select(.id == "temurin-jdk") | {sha256,size_bytes,format,root}) == {sha256:"870ac8c05c6fe563e7a3878a47d0234b83c050e83651d2c47e8b822ec74512dd",size_bytes:214525906,format:"tar.gz-links",root:"jdk-23.0.2+7"} and
  (.assets[] | select(.id == "temurin-jre") | {sha256,size_bytes,format,root}) == {sha256:"1a16c654e67a72dadfa632969a457404ad1cc30c6375857fdcb393e0592ce3ba",size_bytes:51939941,format:"tar.gz-links",root:"jdk-23.0.2+7-jre"} and
  (.assets[] | select(.id == "apache-maven") | {sha256,size_bytes,format,root}) == {sha256:"ccd67d1ee4fd79339c9b6f95d1e5e1e0e0209a8c1b095d9291e009afa0a492a5",size_bytes:9130223,format:"zip",root:"apache-maven-3.8.4"}
' "$default_pins" >/dev/null || fail 'built-in acquisition pins drifted'

printf 'PASS: hosted external-driver provisioning pins, layouts, cleanup, and release configs fail closed\n'
