#!/usr/bin/env bash
set -euo pipefail

readonly CONTRACT_VERSION='pgworkbench.external-driver-hosted-provision/v1'
readonly STATE_PREFIX='pgworkbench-external-driver-gate-'
readonly POSTGRES_PORT=5432

usage() {
  cat >&2 <<'EOF'
Usage:
  provision_external_driver_gate.sh provision --state-dir DIR --repo-root DIR
  provision_external_driver_gate.sh acquire-only --state-dir DIR
  provision_external_driver_gate.sh validate-configs --repo-root DIR
  provision_external_driver_gate.sh cleanup --state-dir DIR
  provision_external_driver_gate.sh pins

The release path accepts only built-in pins. Tests may supply
PGWORKBENCH_EXTERNAL_TEST_PINS_FILE together with
PGWORKBENCH_EXTERNAL_OFFLINE_TEST=1; that override is rejected in GitHub Actions.
EOF
  exit 2
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
}

sha256_file() {
  sha256sum "$1" | awk '{print $1}'
}

file_size() {
  if stat -c %s "$1" >/dev/null 2>&1; then
    stat -c %s "$1"
  else
    stat -f %z "$1"
  fi
}

parse_args() {
  STATE_DIR=''
  REPO_ROOT=''
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --state-dir)
        [[ $# -ge 2 ]] || usage
        STATE_DIR=$2
        shift 2
        ;;
      --repo-root)
        [[ $# -ge 2 ]] || usage
        REPO_ROOT=$2
        shift 2
        ;;
      *) usage ;;
    esac
  done
}

validate_absolute_directory_input() {
  local label=$1
  local value=$2
  [[ -n "$value" && "$value" = /* && "$value" != *$'\n'* && "$value" != *$'\r'* ]] ||
    fail "$label must be an absolute one-line path"
  [[ "$value" =~ ^/[A-Za-z0-9._+/-]+$ ]] || fail "$label contains unsupported path characters"
  [[ "$value" != '/' && "$value" != '/tmp' && "$value" != '/private/tmp' ]] ||
    fail "$label is too broad"
}

validate_state_dir() {
  validate_absolute_directory_input 'state directory' "$STATE_DIR"
  local base parent
  base=${STATE_DIR##*/}
  parent=${STATE_DIR%/*}
  [[ "$base" == "$STATE_PREFIX"* && -n "${base#"$STATE_PREFIX"}" ]] ||
    fail "state directory basename must start with $STATE_PREFIX"
  [[ -d "$parent" && ! -L "$parent" ]] || fail 'state directory parent must be a real directory'
  if [[ -e "$STATE_DIR" && -L "$STATE_DIR" ]]; then
    fail 'state directory must not be a symlink'
  fi
}

validate_repo_root() {
  validate_absolute_directory_input 'repository root' "$REPO_ROOT"
  [[ -d "$REPO_ROOT" && ! -L "$REPO_ROOT" ]] || fail 'repository root must be a real directory'
  [[ -f "$REPO_ROOT/pgworkbench-pack.json" ]] || fail 'repository root has no pgworkbench-pack.json'
}

write_default_pins() {
  jq -n '{
    schema_version: "pgworkbench.external-driver-acquisition-pins/v1",
    assets: [
      {
        id: "benchbase-source",
        url: "https://codeload.github.com/cmu-db/benchbase/tar.gz/33c00473807ebd49304d114a6d769d2d2b2bbb34",
        sha256: "804c9b3018f2f230f4ebbb5d0ebfed28ca417650037736f13fe9212d406fc4bc",
        size_bytes: 43098598,
        format: "tar.gz",
        root: "benchbase-33c00473807ebd49304d114a6d769d2d2b2bbb34",
        license_expression: "GPL-3.0-or-later AND Apache-2.0"
      },
      {
        id: "sysbench-source",
        url: "https://codeload.github.com/akopytov/sysbench/tar.gz/ebf1c90da05dea94648165e4f149abc20c979557",
        sha256: "2a664cb397ebb0678a91d7b876c1ffcebe728a52cbb1ffe0aa63b36fad1c9e1c",
        size_bytes: 1509951,
        format: "tar.gz",
        root: "sysbench-ebf1c90da05dea94648165e4f149abc20c979557",
        license_expression: "GPL-2.0-or-later"
      },
      {
        id: "hammerdb-distribution",
        url: "https://github.com/TPC-Council/HammerDB/releases/download/v6.0/HammerDB-6.0-Prod-Lin-UBU24.tar.gz",
        sha256: "6e0b94724356f35f60760fdcacd0b19de655ec0477383d59e585a7235c4d4a58",
        size_bytes: 36188110,
        format: "tar.gz-links",
        root: "HammerDB-6.0",
        license_expression: "GPL-3.0-or-later",
        entrypoint: "hammerdbcli",
        entrypoint_sha256: "373dee97827a43c1598d7f49f157b7bd2baa10f28c0812d1e93f987c058b6ad4",
        entrypoint_size_bytes: 11427109
      },
      {
        id: "temurin-jdk",
        url: "https://github.com/adoptium/temurin23-binaries/releases/download/jdk-23.0.2%2B7/OpenJDK23U-jdk_x64_linux_hotspot_23.0.2_7.tar.gz",
        sha256: "870ac8c05c6fe563e7a3878a47d0234b83c050e83651d2c47e8b822ec74512dd",
        size_bytes: 214525906,
        format: "tar.gz-links",
        root: "jdk-23.0.2+7",
        license_expression: "NOASSERTION"
      },
      {
        id: "temurin-jre",
        url: "https://github.com/adoptium/temurin23-binaries/releases/download/jdk-23.0.2%2B7/OpenJDK23U-jre_x64_linux_hotspot_23.0.2_7.tar.gz",
        sha256: "1a16c654e67a72dadfa632969a457404ad1cc30c6375857fdcb393e0592ce3ba",
        size_bytes: 51939941,
        format: "tar.gz-links",
        root: "jdk-23.0.2+7-jre",
        license_expression: "NOASSERTION"
      },
      {
        id: "apache-maven",
        url: "https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.8.4/apache-maven-3.8.4-bin.zip",
        sha256: "ccd67d1ee4fd79339c9b6f95d1e5e1e0e0209a8c1b095d9291e009afa0a492a5",
        size_bytes: 9130223,
        format: "zip",
        root: "apache-maven-3.8.4",
        license_expression: "Apache-2.0"
      }
    ]
  }'
}

write_effective_pins() {
  local destination=$1
  local override=${PGWORKBENCH_EXTERNAL_TEST_PINS_FILE:-}
  if [[ -n "$override" ]]; then
    [[ "${PGWORKBENCH_EXTERNAL_OFFLINE_TEST:-}" == 1 ]] ||
      fail 'test pin override requires PGWORKBENCH_EXTERNAL_OFFLINE_TEST=1'
    [[ "${GITHUB_ACTIONS:-false}" != true ]] || fail 'test pin override is forbidden in GitHub Actions'
    [[ -f "$override" && ! -L "$override" ]] || fail 'test pin override must be a regular file'
    cp "$override" "$destination"
  else
    write_default_pins > "$destination"
  fi
  jq -e '
    .schema_version == "pgworkbench.external-driver-acquisition-pins/v1" and
    (.assets | type == "array" and length == 6) and
    ([.assets[].id] | sort) == ([
      "apache-maven", "benchbase-source", "hammerdb-distribution",
      "sysbench-source", "temurin-jdk", "temurin-jre"
    ] | sort) and
    all(.assets[];
      (.url | test("^https://")) and
      (.sha256 | test("^[0-9a-f]{64}$")) and
      (.size_bytes | type == "number" and . > 0) and
      (.format | IN("tar.gz", "tar.gz-links", "zip")) and
      (.root | test("^[A-Za-z0-9][A-Za-z0-9.+_-]*$")) and
      (.license_expression | type == "string" and length > 0)
    ) and
    ([.assets[].id] | unique | length) == 6
  ' "$destination" >/dev/null || fail 'acquisition pin manifest is invalid or incomplete'
}

initialize_state() {
  validate_state_dir
  [[ ! -e "$STATE_DIR" ]] || fail 'state directory already exists'
  mkdir -m 0700 "$STATE_DIR"
  printf '%s\n' "$CONTRACT_VERSION" > "$STATE_DIR/.pgworkbench-external-driver-gate"
  mkdir -m 0700 \
    "$STATE_DIR/downloads" "$STATE_DIR/extracted" "$STATE_DIR/logs" \
    "$STATE_DIR/runtimes" "$STATE_DIR/distributions" "$STATE_DIR/evidence"
  write_effective_pins "$STATE_DIR/acquisition-pins.json"
}

safe_extract() {
  local archive=$1
  local destination=$2
  local format=$3
  local expected_root=$4
  mkdir -m 0700 "$destination"
  python3 - "$archive" "$destination" "$format" "$expected_root" <<'PY'
import os
import pathlib
import stat
import sys
import tarfile
import zipfile

archive, destination, archive_format, expected_root = sys.argv[1:]

def checked_name(raw):
    if "\\" in raw or "\x00" in raw:
        raise SystemExit("unsafe archive member")
    path = pathlib.PurePosixPath(raw)
    if path.is_absolute() or not path.parts or any(part in ("", ".", "..") for part in path.parts):
        raise SystemExit("unsafe archive member")
    if path.parts[0] != expected_root:
        raise SystemExit("archive does not have the exact pinned top-level root")
    return path

if archive_format in ("tar.gz", "tar.gz-links"):
    with tarfile.open(archive, "r:gz") as source:
        members = source.getmembers()
        if not members:
            raise SystemExit("empty archive")
        member_paths = {}
        links = {}
        for member in members:
            member_path = checked_name(member.name.rstrip("/"))
            member_name = member_path.as_posix()
            if member_name in member_paths:
                raise SystemExit("duplicate archive member")
            member_paths[member_name] = member
            if member.ischr() or member.isblk() or member.isfifo():
                raise SystemExit("special archive member")
            if member.issym() or member.islnk():
                if archive_format != "tar.gz-links":
                    raise SystemExit("links are forbidden in this archive")
                target = pathlib.PurePosixPath(member.linkname)
                if target.is_absolute() or "\\" in member.linkname:
                    raise SystemExit("unsafe archive link")
                if member.issym():
                    resolved = pathlib.PurePosixPath(os.path.normpath(str(pathlib.PurePosixPath(member.name).parent / target)))
                else:
                    resolved = pathlib.PurePosixPath(os.path.normpath(str(target)))
                if resolved.is_absolute() or not resolved.parts or resolved.parts[0] != expected_root or ".." in resolved.parts:
                    raise SystemExit("archive link escapes the pinned top-level root")
                links[member_name] = resolved.as_posix()

        # Reject a member below any archive link regardless of member order.
        # Otherwise a link followed by a child member could redirect extraction
        # outside the validated tree even when the child's lexical path is safe.
        for member_name in member_paths:
            parts = pathlib.PurePosixPath(member_name).parts
            for length in range(1, len(parts)):
                if pathlib.PurePosixPath(*parts[:length]).as_posix() in links:
                    raise SystemExit("archive member traverses an archive link")

        # Resolve every complete link chain before extraction. Each lexical
        # target was already constrained to expected_root; this pass rejects
        # cycles and chains that traverse another unsafe link definition.
        def resolve_link(path):
            current = pathlib.PurePosixPath(path)
            seen = set()
            while True:
                replaced = False
                parts = current.parts
                for length in range(1, len(parts) + 1):
                    prefix = pathlib.PurePosixPath(*parts[:length]).as_posix()
                    if prefix not in links:
                        continue
                    if prefix in seen:
                        raise SystemExit("archive link cycle")
                    seen.add(prefix)
                    current = pathlib.PurePosixPath(
                        os.path.normpath(str(pathlib.PurePosixPath(links[prefix], *parts[length:])))
                    )
                    if (current.is_absolute() or not current.parts or
                            current.parts[0] != expected_root or ".." in current.parts):
                        raise SystemExit("archive link chain escapes the pinned top-level root")
                    replaced = True
                    break
                if not replaced:
                    return current

        for target in links.values():
            resolve_link(target)
        source.extractall(destination, filter="data")
elif archive_format == "zip":
    with zipfile.ZipFile(archive) as source:
        infos = source.infolist()
        if not infos:
            raise SystemExit("empty archive")
        for info in infos:
            checked_name(info.filename.rstrip("/"))
            mode = (info.external_attr >> 16) & 0o170000
            if mode == stat.S_IFLNK:
                raise SystemExit("zip links are forbidden")
        source.extractall(destination)
        for info in infos:
            extracted = os.path.join(destination, *pathlib.PurePosixPath(info.filename.rstrip("/")).parts)
            archived_mode = (info.external_attr >> 16) & 0o777
            if archived_mode and os.path.exists(extracted):
                os.chmod(extracted, archived_mode)
else:
    raise SystemExit("unsupported archive format")

root = os.path.join(destination, expected_root)
if not os.path.isdir(root) or os.path.islink(root):
    raise SystemExit("pinned archive root is missing or unsafe")
if sorted(os.listdir(destination)) != [expected_root]:
    raise SystemExit("archive has more than one top-level root")
root_real = os.path.realpath(root)
for current, dirs, names in os.walk(root, topdown=True, followlinks=False):
    for name in dirs + names:
        candidate = os.path.join(current, name)
        resolved = os.path.realpath(candidate)
        if os.path.commonpath((root_real, resolved)) != root_real:
            raise SystemExit("extracted archive link escapes the pinned top-level root")
PY
}

fetch_asset() {
  local url=$1
  local destination=$2
  local fetcher=${PGWORKBENCH_EXTERNAL_TEST_FETCHER:-}
  if [[ -n "$fetcher" ]]; then
    [[ "${PGWORKBENCH_EXTERNAL_OFFLINE_TEST:-}" == 1 ]] ||
      fail 'test fetcher requires PGWORKBENCH_EXTERNAL_OFFLINE_TEST=1'
    [[ "${GITHUB_ACTIONS:-false}" != true ]] || fail 'test fetcher is forbidden in GitHub Actions'
    [[ -x "$fetcher" && ! -L "$fetcher" ]] || fail 'test fetcher must be a regular executable'
    "$fetcher" "$url" "$destination"
  else
    curl --fail --location --proto '=https' --tlsv1.2 \
      --retry 3 --retry-all-errors --output "$destination" "$url"
  fi
}

validate_acquired_layout() {
  local id=$1
  local root=$2
  local pins=$3
  case "$id" in
    benchbase-source)
      [[ -f "$root/pom.xml" && -x "$root/mvnw" && -f "$root/.mvn/wrapper/maven-wrapper.jar" ]] ||
        fail 'BenchBase source layout is incomplete'
      ;;
    sysbench-source)
      [[ -x "$root/autogen.sh" && -f "$root/src/lua/oltp_common.lua" ]] ||
        fail 'sysbench source layout is incomplete'
      ;;
    hammerdb-distribution)
      local entrypoint expected_digest expected_size
      entrypoint=$(jq -r '.assets[] | select(.id == "hammerdb-distribution") | .entrypoint' "$pins")
      expected_digest=$(jq -r '.assets[] | select(.id == "hammerdb-distribution") | .entrypoint_sha256' "$pins")
      expected_size=$(jq -r '.assets[] | select(.id == "hammerdb-distribution") | .entrypoint_size_bytes' "$pins")
      [[ -f "$root/$entrypoint" && ! -L "$root/$entrypoint" ]] || fail 'HammerDB launcher is missing or unsafe'
      [[ "$(sha256_file "$root/$entrypoint")" == "$expected_digest" ]] || fail 'HammerDB launcher digest mismatch'
      [[ "$(file_size "$root/$entrypoint")" == "$expected_size" ]] || fail 'HammerDB launcher size mismatch'
      ;;
    temurin-jdk|temurin-jre)
      [[ -x "$root/bin/java" && ! -L "$root/bin/java" ]] || fail "$id java launcher is missing or unsafe"
      ;;
    apache-maven)
      [[ -x "$root/bin/mvn" && ! -L "$root/bin/mvn" ]] || fail 'Maven launcher is missing or unsafe'
      ;;
    *) fail "unexpected acquisition id: $id" ;;
  esac
}

acquire_assets() {
  local pins="$STATE_DIR/acquisition-pins.json"
  local records="$STATE_DIR/evidence/acquisitions.jsonl"
  : > "$records"
  while IFS=$'\t' read -r id url expected_digest expected_size format root license_expression; do
    local archive="$STATE_DIR/downloads/$id.archive"
    local extract="$STATE_DIR/extracted/$id"
    fetch_asset "$url" "$archive"
    [[ -f "$archive" && ! -L "$archive" ]] || fail "downloaded asset is unsafe: $id"
    local observed_digest observed_size
    observed_digest=$(sha256_file "$archive")
    observed_size=$(file_size "$archive")
    [[ "$observed_digest" == "$expected_digest" ]] || fail "$id archive digest mismatch"
    [[ "$observed_size" == "$expected_size" ]] || fail "$id archive size mismatch"
    safe_extract "$archive" "$extract" "$format" "$root"
    validate_acquired_layout "$id" "$extract/$root" "$pins"
    jq -n -c \
      --arg id "$id" --arg url "$url" --arg sha256 "$observed_digest" \
      --argjson size_bytes "$observed_size" --arg format "$format" --arg root "$root" \
      --arg license_expression "$license_expression" \
      '{id:$id,url:$url,sha256:("sha256:"+$sha256),size_bytes:$size_bytes,format:$format,root:$root,status:"verified",license_expression:$license_expression,dependency_license_closure_asserted:false,project_redistribution:false,runtime_replay_available:false}' \
      >> "$records"
  done < <(jq -r '.assets[] | [.id,.url,.sha256,(.size_bytes|tostring),.format,.root,.license_expression] | @tsv' "$pins")
  jq -sS '{
    schema_version:"pgworkbench.external-driver-acquisitions/v1",
    artifact_type:"pgworkbench.external-driver-acquisitions",
    project_redistribution:false,
    runtime_replay_available:false,
    complete_license_or_source_closure_attested:false,
    assets:sort_by(.id)
  }' "$records" > "$STATE_DIR/evidence/acquisitions.json"
  rm "$records"
}

validate_configs() {
  validate_repo_root
  local config_root="$REPO_ROOT/configs/benchmark-drivers"
  jq -e '
    (keys | sort) == (["artifact_type","mode","postgresql","schema_version","tprocc"] | sort) and
    .schema_version == "pgworkbench.hammerdb-v6-native-run-config/v1" and
    .artifact_type == "pgworkbench.hammerdb-v6-native-run-config" and
    .mode == "execute-only-prepared-schema" and
    .postgresql == {host:"127.0.0.1",port:5432,user:"tpcc",database:"tpcc",sslmode:"prefer"} and
    .tprocc == {warehouses:20,virtual_users:4,rampup_minutes:1,duration_minutes:2,total_iterations:1000000}
  ' "$config_root/hammerdb-v6-tprocc-release-smoke.json" >/dev/null ||
    fail 'release-only HammerDB config drifted from 20W/4VU/1m/2m/1M'
  jq -e '
    .tprocc == {warehouses:100,virtual_users:32,rampup_minutes:2,duration_minutes:5,total_iterations:10000000}
  ' "$config_root/hammerdb-v6-tprocc-postgresql.json" >/dev/null ||
    fail 'manual 100W HammerDB configuration drifted'
  jq -e '
    (keys | sort) == ([
      "artifact_type","duration_seconds","postgresql","random_seed",
      "rate","report_interval_seconds","schema_version","threads"
    ] | sort) and
    .schema_version == "pgworkbench.sysbench-native-run-config/v1" and
    .artifact_type == "pgworkbench.sysbench-native-run-config" and
    .threads == 4 and .duration_seconds == 60 and .report_interval_seconds == 1 and
    .rate == 0 and .random_seed == 424242 and
    .postgresql == {host:"127.0.0.1",port:5432,user:"postgres",database:"benchmark"}
  ' "$config_root/sysbench-postgresql.json" >/dev/null || fail 'sysbench release-smoke config drifted'
  python3 - "$config_root/benchbase-33c0047-tpcc-release-smoke.xml" <<'PY'
import sys
import xml.etree.ElementTree as ET

root = ET.parse(sys.argv[1]).getroot()
if root.tag != "parameters" or root.attrib:
    raise SystemExit("BenchBase release-smoke root drifted")
if [node.tag for node in list(root)] != [
    "type", "driver", "url", "username", "password", "randomSeed",
    "reconnectOnConnectionFailure", "isolation", "batchsize", "scalefactor",
    "terminals", "works", "transactiontypes"
]:
    raise SystemExit("BenchBase release-smoke top-level shape drifted")
if any(node.attrib for node in list(root)):
    raise SystemExit("BenchBase release-smoke attributes are forbidden")
expected = {
    "type":"POSTGRES", "driver":"org.postgresql.Driver",
    "url":"jdbc:postgresql://127.0.0.1:5432/benchbase",
    "username":"benchbase", "password":"", "randomSeed":"424242",
    "reconnectOnConnectionFailure":"true", "isolation":"TRANSACTION_SERIALIZABLE",
    "batchsize":"128", "scalefactor":"1", "terminals":"1"
}
for name, value in expected.items():
    nodes = root.findall(name)
    if len(nodes) != 1 or (nodes[0].text or "") != value:
        raise SystemExit(f"BenchBase release-smoke field drifted: {name}")
works = root.findall("./works/work")
if len(works) != 1 or root.find("works").attrib or works[0].attrib or [node.tag for node in list(works[0])] != ["time", "rate", "weights"]:
    raise SystemExit("BenchBase release-smoke must have exactly one work phase")
phase = works[0]
if phase.findtext("time") != "15" or phase.findtext("rate") != "100" or phase.findtext("weights") != "45,43,4,4,4":
    raise SystemExit("BenchBase release-smoke work phase drifted")
transactions = root.findall("./transactiontypes/transactiontype")
if root.find("transactiontypes").attrib or any(node.attrib or [child.tag for child in list(node)] != ["name"] for node in transactions):
    raise SystemExit("BenchBase release-smoke transaction shape drifted")
if [node.findtext("name") for node in transactions] != [
    "NewOrder", "Payment", "OrderStatus", "Delivery", "StockLevel"
]:
    raise SystemExit("BenchBase release-smoke transaction set drifted")
PY
  if grep -En 'PGWORKBENCH_DRIVER_PASSWORD|password[[:space:]]*=' "$config_root"/*release-smoke* >/dev/null; then
    fail 'release-smoke configs may not retain a driver password'
  fi
}

runtime_inventory() {
  local id=$1
  local root=$2
  local output=$3
  local license_expression=$4
  python3 - "$id" "$root" "$output" "$license_expression" <<'PY'
import hashlib
import json
import os
import pathlib
import stat
import sys

runtime_id, root, output, license_expression = sys.argv[1:]
files = []
for current, dirs, names in os.walk(root, topdown=True, followlinks=False):
    dirs.sort()
    names.sort()
    for name in dirs:
        path = os.path.join(current, name)
        if os.path.islink(path):
            raise SystemExit("runtime directory symlink is forbidden")
    for name in names:
        path = os.path.join(current, name)
        info = os.lstat(path)
        if not stat.S_ISREG(info.st_mode):
            raise SystemExit("runtime contains a non-regular file")
        relative = pathlib.Path(path).relative_to(root).as_posix()
        digest = hashlib.sha256()
        with open(path, "rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
        files.append({
            "path": relative,
            "sha256": "sha256:" + digest.hexdigest(),
            "size_bytes": info.st_size,
            "mode": stat.S_IMODE(info.st_mode)
        })
files.sort(key=lambda item: item["path"])
canonical = json.dumps(files, sort_keys=True, separators=(",", ":")).encode()
document = {
    "schema_version":"pgworkbench.external-driver-runtime-inventory/v1",
    "artifact_type":"pgworkbench.external-driver-runtime-inventory",
    "runtime_id":runtime_id,
    "file_count":len(files),
    "total_size_bytes":sum(item["size_bytes"] for item in files),
    "tree_digest":"sha256:" + hashlib.sha256(canonical).hexdigest(),
    "license_expression":license_expression,
    "dependency_license_closure_asserted":False,
    "project_redistribution":False,
    "runtime_replay_available":False,
    "files":files
}
with open(output, "w", encoding="utf-8", newline="\n") as target:
    json.dump(document, target, sort_keys=True, indent=2)
    target.write("\n")
PY
}

curate_benchbase_runtime() {
  local distribution=$1
  local runtime=$2
  mkdir -m 0700 "$runtime"
  python3 - "$distribution" "$runtime" <<'PY'
import os
import pathlib
import shutil
import sys
import urllib.parse
import zipfile

source = pathlib.Path(sys.argv[1]).resolve()
target = pathlib.Path(sys.argv[2]).resolve()
queue = [pathlib.PurePosixPath("benchbase.jar")]
seen = set()

def manifest_classpath(jar):
    with zipfile.ZipFile(jar) as archive:
        names = [name for name in archive.namelist() if name.upper() == "META-INF/MANIFEST.MF"]
        if len(names) != 1:
            raise SystemExit(f"JAR must have exactly one manifest: {jar}")
        raw = archive.read(names[0])
    if len(raw) > 1024 * 1024 or b"\x00" in raw:
        raise SystemExit("unsafe JAR manifest")
    lines = raw.replace(b"\r\n", b"\n").replace(b"\r", b"\n").split(b"\n")
    unfolded = []
    for line in lines:
        if line.startswith(b" "):
            if not unfolded:
                raise SystemExit("orphan manifest continuation")
            unfolded[-1] += line[1:]
        else:
            unfolded.append(line)
    values = []
    for line in unfolded:
        if not line:
            break
        if b":" not in line:
            raise SystemExit("malformed manifest header")
        name, value = line.split(b":", 1)
        if name.lower() == b"class-path":
            if values:
                raise SystemExit("duplicate Class-Path header")
            values = value.strip().decode("ascii").split()
    return values

while queue:
    relative = queue.pop(0)
    if relative in seen:
        continue
    seen.add(relative)
    path = source.joinpath(*relative.parts)
    if not path.is_file() or path.is_symlink():
        raise SystemExit(f"missing or unsafe BenchBase closure member: {relative}")
    destination = target.joinpath(*relative.parts)
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(path, destination)
    os.chmod(destination, 0o444)
    for item in manifest_classpath(path):
        parsed = urllib.parse.urlsplit(item)
        if parsed.scheme or parsed.netloc or parsed.query or parsed.fragment or "%" in item or "\\" in item:
            raise SystemExit("unsafe manifest Class-Path entry")
        candidate = pathlib.PurePosixPath(item)
        resolved = pathlib.PurePosixPath(os.path.normpath(str(relative.parent / candidate)))
        if resolved.is_absolute() or ".." in resolved.parts or len(resolved.parts) != 2 or resolved.parts[0] != "lib" or resolved.suffix != ".jar":
            raise SystemExit("manifest dependency escaped exact lib/*.jar closure")
        if resolved not in seen:
            queue.append(resolved)

plugin = source / "config" / "plugin.xml"
if not plugin.is_file() or plugin.is_symlink():
    raise SystemExit("BenchBase config/plugin.xml is missing or unsafe")
destination = target / "config" / "plugin.xml"
destination.parent.mkdir(parents=True, exist_ok=True)
shutil.copyfile(plugin, destination)
os.chmod(destination, 0o444)

actual = sorted(path.relative_to(target).as_posix() for path in target.rglob("*") if path.is_file())
expected = sorted([path.as_posix() for path in seen] + ["config/plugin.xml"])
if actual != expected or len(actual) != 28:
    raise SystemExit(f"BenchBase curated closure is not exact: got {len(actual)} files, expected 28")
PY
}

build_runtimes() {
  local pins="$STATE_DIR/acquisition-pins.json"
  local bench_root sysbench_root hammer_root jdk_root jre_root maven_root
  bench_root=$(jq -r '.assets[]|select(.id=="benchbase-source")|.root' "$pins")
  sysbench_root=$(jq -r '.assets[]|select(.id=="sysbench-source")|.root' "$pins")
  hammer_root=$(jq -r '.assets[]|select(.id=="hammerdb-distribution")|.root' "$pins")
  jdk_root=$(jq -r '.assets[]|select(.id=="temurin-jdk")|.root' "$pins")
  jre_root=$(jq -r '.assets[]|select(.id=="temurin-jre")|.root' "$pins")
  maven_root=$(jq -r '.assets[]|select(.id=="apache-maven")|.root' "$pins")

  local bench_source="$STATE_DIR/extracted/benchbase-source/$bench_root"
  local sysbench_source="$STATE_DIR/extracted/sysbench-source/$sysbench_root"
  local hammer_distribution="$STATE_DIR/extracted/hammerdb-distribution/$hammer_root"
  local jdk="$STATE_DIR/extracted/temurin-jdk/$jdk_root"
  local jre="$STATE_DIR/extracted/temurin-jre/$jre_root"
  local maven="$STATE_DIR/extracted/apache-maven/$maven_root"
  "$jdk/bin/java" -version 2>&1 | grep -F '23.0.2' >/dev/null || fail 'pinned Temurin JDK version mismatch'
  "$jre/bin/java" -version 2>&1 | grep -F '23.0.2' >/dev/null || fail 'pinned Temurin JRE version mismatch'
  "$maven/bin/mvn" --version | grep -F 'Apache Maven 3.8.4' >/dev/null || fail 'pinned Maven version mismatch'

  local m2="$STATE_DIR/maven-repository"
  local maven_home="$STATE_DIR/maven-home"
  mkdir -m 0700 "$m2" "$maven_home"
  (
    cd "$bench_source"
    HOME="$maven_home" JAVA_HOME="$jdk" PATH="$jdk/bin:$PATH" \
      "$maven/bin/mvn" -B -Duser.home="$maven_home" \
        -Dmaven.repo.local="$m2" clean package verify \
        -P postgres -DskipTests -Ddescriptors=src/main/assembly/tgz.xml
  ) >"$STATE_DIR/logs/benchbase-build.log" 2>&1
  local bench_archive="$bench_source/target/benchbase-postgres.tgz"
  [[ -f "$bench_archive" && ! -L "$bench_archive" ]] || fail 'BenchBase build did not produce benchbase-postgres.tgz'
  safe_extract "$bench_archive" "$STATE_DIR/distributions/benchbase" tar.gz benchbase-postgres
  local bench_distribution="$STATE_DIR/distributions/benchbase/benchbase-postgres"
  local bench_runtime="$STATE_DIR/runtimes/benchbase"
  curate_benchbase_runtime "$bench_distribution" "$bench_runtime"

  local hammer_runtime="$STATE_DIR/runtimes/hammerdb"
  mkdir -m 0700 "$hammer_runtime"
  install -m 0555 "$hammer_distribution/hammerdbcli" "$hammer_runtime/hammerdbcli"
  [[ "$(sha256_file "$hammer_runtime/hammerdbcli")" == \
    '373dee97827a43c1598d7f49f157b7bd2baa10f28c0812d1e93f987c058b6ad4' ]] ||
    fail 'curated HammerDB launcher digest mismatch'
  [[ "$(find "$hammer_runtime" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d ' ')" == 1 ]] ||
    fail 'HammerDB runtime must contain exactly one file'

  local sysbench_distribution="$STATE_DIR/distributions/sysbench"
  mkdir -m 0700 "$sysbench_distribution"
  (
    cd "$sysbench_source"
    PATH="/usr/lib/postgresql/16/bin:$PATH" ./autogen.sh
    PATH="/usr/lib/postgresql/16/bin:$PATH" \
      ./configure --without-mysql --with-pgsql --prefix="$sysbench_distribution"
    make -j2
    make install
  ) >"$STATE_DIR/logs/sysbench-build.log" 2>&1
  "$sysbench_distribution/bin/sysbench" --version | grep -Fx 'sysbench 1.0.20' >/dev/null ||
    fail 'built sysbench version mismatch'
  local sysbench_runtime="$STATE_DIR/runtimes/sysbench"
  mkdir -p "$sysbench_runtime/bin" "$sysbench_runtime/share/sysbench"
  install -m 0555 "$sysbench_distribution/bin/sysbench" "$sysbench_runtime/bin/sysbench"
  install -m 0444 \
    "$sysbench_distribution/share/sysbench/oltp_read_write.lua" \
    "$sysbench_distribution/share/sysbench/oltp_common.lua" \
    "$sysbench_runtime/share/sysbench/"

  mkdir -m 0700 "$STATE_DIR/evidence/runtime-inventories"
  runtime_inventory benchbase-33c0047 "$bench_runtime" \
    "$STATE_DIR/evidence/runtime-inventories/benchbase.json" \
    'GPL-3.0-or-later AND Apache-2.0'
  runtime_inventory hammerdb-6.0 "$hammer_runtime" \
    "$STATE_DIR/evidence/runtime-inventories/hammerdb.json" 'GPL-3.0-or-later'
  runtime_inventory sysbench-1.0.20 "$sysbench_runtime" \
    "$STATE_DIR/evidence/runtime-inventories/sysbench.json" 'GPL-2.0-or-later'
  jq -sS '{
    schema_version:"pgworkbench.external-driver-runtime-set/v1",
    artifact_type:"pgworkbench.external-driver-runtime-set",
    source_to_binary_attested:false,
    host_runtime_dependencies_attested:false,
    complete_license_or_source_closure_attested:false,
    project_redistribution:false,
    runtime_replay_available:false,
    runtimes:sort_by(.runtime_id)
  }' "$STATE_DIR/evidence/runtime-inventories/"*.json > "$STATE_DIR/evidence/runtime-set.json"

  jq --arg benchbase_build_archive_sha256 "sha256:$(sha256_file "$bench_archive")" \
    --arg benchbase_build_archive_size "$(file_size "$bench_archive")" \
    --arg maven_repository_digest "$(directory_digest "$m2")" \
    '. + {builds:{
      benchbase:{commit:"33c00473807ebd49304d114a6d769d2d2b2bbb34",profile:"postgres",source_to_binary_attested:false,archive_sha256:$benchbase_build_archive_sha256,archive_size_bytes:($benchbase_build_archive_size|tonumber),maven_repository_digest:$maven_repository_digest},
      sysbench:{commit:"ebf1c90da05dea94648165e4f149abc20c979557",configure:["--without-mysql","--with-pgsql"],source_to_binary_attested:false}
    }}' "$STATE_DIR/evidence/acquisitions.json" > "$STATE_DIR/evidence/acquisitions.json.next"
  mv "$STATE_DIR/evidence/acquisitions.json.next" "$STATE_DIR/evidence/acquisitions.json"

  printf '%s\n' "$jre/bin/java" > "$STATE_DIR/benchbase-java.path"
  printf '%s\n' "$bench_distribution" > "$STATE_DIR/benchbase-distribution.path"
  printf '%s\n' "$hammer_distribution" > "$STATE_DIR/hammerdb-distribution.path"
}

directory_digest() {
  local root=$1
  python3 - "$root" <<'PY'
import hashlib
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
lines = []
for path in sorted(item for item in root.rglob("*") if item.is_file() and not item.is_symlink()):
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    lines.append(f"{path.relative_to(root).as_posix()}\0{path.stat().st_size}\0{digest}\n")
print("sha256:" + hashlib.sha256("".join(lines).encode()).hexdigest())
PY
}

start_postgres() {
  local bindir=/usr/lib/postgresql/16/bin
  for command in initdb pg_ctl pg_isready psql postgres; do
    [[ -x "$bindir/$command" ]] || fail "PostgreSQL 16 command is missing: $bindir/$command"
  done
  "$bindir/postgres" --version | grep -Eq 'PostgreSQL 16([.[:space:]]|$)' ||
    fail 'external-driver gate requires PostgreSQL 16'
  if "$bindir/pg_isready" -h 127.0.0.1 -p "$POSTGRES_PORT" -t 1 >/dev/null 2>&1; then
    fail "port $POSTGRES_PORT is already serving PostgreSQL; refusing to reuse it"
  fi
  local pgdata="$STATE_DIR/pgdata"
  local socket="$STATE_DIR/pgsocket"
  mkdir -m 0700 "$socket"
  "$bindir/initdb" -D "$pgdata" --username=postgres --auth-local=trust --auth-host=trust \
    --no-locale --encoding=UTF8 >"$STATE_DIR/logs/initdb.log" 2>&1
  {
    printf "listen_addresses = '127.0.0.1'\n"
    printf 'port = %d\n' "$POSTGRES_PORT"
    printf "unix_socket_directories = '%s'\n" "$socket"
    printf 'fsync = off\n'
    printf 'synchronous_commit = off\n'
    printf 'full_page_writes = off\n'
    printf 'shared_buffers = 128MB\n'
    printf 'max_connections = 100\n'
    printf 'max_locks_per_transaction = 256\n'
  } >> "$pgdata/postgresql.conf"
  "$bindir/pg_ctl" -D "$pgdata" -l "$STATE_DIR/logs/postgres.log" -w start
  "$bindir/pg_isready" -h 127.0.0.1 -p "$POSTGRES_PORT" -t 5 >/dev/null
  printf '%s\n' "$bindir" > "$STATE_DIR/postgres-bindir.path"
}

psql_scalar() {
  local database=$1
  local sql=$2
  local bindir
  bindir=$(<"$STATE_DIR/postgres-bindir.path")
  "$bindir/psql" -XAtq -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$POSTGRES_PORT" \
    -U postgres -d "$database" -c "$sql"
}

prepare_datasets() {
  local bindir java bench_distribution hammer_distribution
  bindir=$(<"$STATE_DIR/postgres-bindir.path")
  java=$(<"$STATE_DIR/benchbase-java.path")
  bench_distribution=$(<"$STATE_DIR/benchbase-distribution.path")
  hammer_distribution=$(<"$STATE_DIR/hammerdb-distribution.path")
  local config_root="$REPO_ROOT/configs/benchmark-drivers"
  local hammer_build="$STATE_DIR/hammerdb-build.tcl"

  install -d -m 0700 \
    "$STATE_DIR/benchbase-home" "$STATE_DIR/hammerdb-home" \
    "$STATE_DIR/hammerdb-tmp" "$STATE_DIR/sysbench-home" "$STATE_DIR/sysbench-tmp"

  "$bindir/psql" -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$POSTGRES_PORT" \
    -U postgres -d postgres <<'SQL'
CREATE ROLE benchbase LOGIN;
CREATE DATABASE benchbase OWNER benchbase;
SQL
  (
    cd "$bench_distribution"
    HOME="$STATE_DIR/benchbase-home" TMPDIR="$STATE_DIR" \
      "$java" -jar benchbase.jar -b tpcc \
        -c "$config_root/benchbase-33c0047-tpcc-release-smoke.xml" \
        --create=true --load=true --execute=false
  ) >"$STATE_DIR/logs/benchbase-prepare.log" 2>&1
  local bench_warehouses
  bench_warehouses=$(psql_scalar benchbase 'SELECT count(*) FROM warehouse')
  [[ "$bench_warehouses" == 1 ]] || fail 'BenchBase prepared warehouse count is not 1'

  # This ephemeral command list is limited to HammerDB's documented CLI
  # configuration and build commands. It is destroyed with the runner state
  # and is never retained in source, release, or Actions artifacts.
  {
    printf 'dbset db pg\n'
    printf 'dbset bm TPC-C\n'
    printf 'diset connection pg_host 127.0.0.1\n'
    printf 'diset connection pg_port 5432\n'
    printf 'diset connection pg_sslmode prefer\n'
    printf 'diset tpcc pg_count_ware 20\n'
    printf 'diset tpcc pg_num_vu 4\n'
    printf 'diset tpcc pg_superuser postgres\n'
    printf 'diset tpcc pg_superuserpass {}\n'
    printf 'diset tpcc pg_defaultdbase postgres\n'
    printf 'diset tpcc pg_user tpcc\n'
    printf 'diset tpcc pg_pass {}\n'
    printf 'diset tpcc pg_dbase tpcc\n'
    printf 'diset tpcc pg_tspace pg_default\n'
    printf 'diset tpcc pg_storedprocs true\n'
    printf 'diset tpcc pg_partition false\n'
    printf 'buildschema\n'
    printf 'puts {PGWORKBENCH_HAMMERDB_BUILD_COMPLETE}\n'
    printf 'exit\n'
  } > "$hammer_build"
  chmod 0600 "$hammer_build"
  (
    cd "$hammer_distribution"
    HOME="$STATE_DIR/hammerdb-home" TMPDIR="$STATE_DIR/hammerdb-tmp" \
      TMP="$STATE_DIR/hammerdb-tmp" TEMP="$STATE_DIR/hammerdb-tmp" \
      ./hammerdbcli auto "$hammer_build"
  ) >"$STATE_DIR/logs/hammerdb-prepare.log" 2>&1
  grep -Fq 'PGWORKBENCH_HAMMERDB_BUILD_COMPLETE' "$STATE_DIR/logs/hammerdb-prepare.log" ||
    fail 'HammerDB buildschema did not reach its completion marker'
  local hammer_warehouses hammer_tables
  hammer_warehouses=$(psql_scalar tpcc 'SELECT count(*) FROM warehouse')
  hammer_tables=$(psql_scalar tpcc "SELECT count(*) FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relkind IN ('r','p') AND relname IN ('warehouse','district','customer','history','new_order','orders','order_line','item','stock')")
  [[ "$hammer_warehouses" == 20 && "$hammer_tables" == 9 ]] ||
    fail 'HammerDB prepared schema failed warehouse/table postconditions'

  "$bindir/createdb" -h 127.0.0.1 -p "$POSTGRES_PORT" -U postgres -O postgres benchmark
  local sysbench_runtime="$STATE_DIR/runtimes/sysbench"
  HOME="$STATE_DIR/sysbench-home" TMPDIR="$STATE_DIR/sysbench-tmp" \
    TMP="$STATE_DIR/sysbench-tmp" TEMP="$STATE_DIR/sysbench-tmp" \
    LUA_PATH="$sysbench_runtime/share/sysbench/?.lua" \
    "$sysbench_runtime/bin/sysbench" --db-driver=pgsql \
      --pgsql-host=127.0.0.1 --pgsql-port="$POSTGRES_PORT" \
      --pgsql-user=postgres --pgsql-db=benchmark --tables=1 --table-size=10000 \
      --rand-seed=424242 \
      "$sysbench_runtime/share/sysbench/oltp_read_write.lua" prepare \
      >"$STATE_DIR/logs/sysbench-prepare.log" 2>&1
  local sysbench_rows
  sysbench_rows=$(psql_scalar benchmark 'SELECT count(*) FROM sbtest1')
  [[ "$sysbench_rows" == 10000 ]] || fail 'sysbench prepared row count is not 10000'

  jq -n \
    --arg benchbase_config_digest "sha256:$(sha256_file "$config_root/benchbase-33c0047-tpcc-release-smoke.xml")" \
    --arg hammerdb_build_command_digest "sha256:$(sha256_file "$hammer_build")" \
    --arg hammerdb_execution_config_digest "sha256:$(sha256_file "$config_root/hammerdb-v6-tprocc-release-smoke.json")" \
    --arg sysbench_execution_config_digest "sha256:$(sha256_file "$config_root/sysbench-postgresql.json")" \
    --argjson benchbase_warehouses "$bench_warehouses" \
    --argjson hammerdb_warehouses "$hammer_warehouses" \
    --argjson hammerdb_required_tables "$hammer_tables" \
    --argjson sysbench_rows "$sysbench_rows" \
    '{
      schema_version:"pgworkbench.external-driver-dataset-provisioning/v1",
      artifact_type:"pgworkbench.external-driver-dataset-provisioning",
      purpose:"release-smoke-adapter-compatibility",
      performance_claim:false,
      target:{engine:"PostgreSQL",major:16,host:"127.0.0.1",port:5432,authentication:"ephemeral-loopback-trust",fresh_cluster:true},
      datasets:[
        {driver_id:"benchbase-postgresql-33c0047",database:"benchbase",role:"benchbase",mode:"create-load-only",scale_factor:1,pinned_loader_rng_seed:0,pinned_workload_random_seed:424242,observed_warehouses:$benchbase_warehouses,config_digest:$benchbase_config_digest},
        {driver_id:"hammerdb-postgresql-6.0",database:"tpcc",role:"tpcc",mode:"ephemeral-documented-cli-buildschema",warehouses:20,build_virtual_users:4,observed_warehouses:$hammerdb_warehouses,observed_required_tables:$hammerdb_required_tables,ephemeral_build_command_digest:$hammerdb_build_command_digest,execution_config_digest:$hammerdb_execution_config_digest},
        {driver_id:"sysbench-postgresql-1.0.20",database:"benchmark",role:"postgres",mode:"oltp_read_write-prepare",tables:1,table_size:10000,random_seed:424242,observed_rows:$sysbench_rows,execution_config_digest:$sysbench_execution_config_digest}
      ]
    }' > "$STATE_DIR/evidence/datasets.json"
}

capture_host() {
  local java hammer sysbench bindir
  java=$(<"$STATE_DIR/benchbase-java.path")
  hammer="$STATE_DIR/runtimes/hammerdb/hammerdbcli"
  sysbench="$STATE_DIR/runtimes/sysbench/bin/sysbench"
  bindir=$(<"$STATE_DIR/postgres-bindir.path")
  local packages ldd_java ldd_hammer ldd_sysbench
  packages=$(dpkg-query -W -f='${Package}=${Version}\n' \
    postgresql-16 postgresql-client-16 build-essential automake libtool pkg-config libaio-dev libpq-dev 2>/dev/null | LC_ALL=C sort | jq -Rsc 'split("\n")|map(select(length>0))')
  ldd_java=$(ldd "$java" 2>&1 | jq -Rsc 'split("\n")|map(select(length>0))')
  ldd_hammer=$(ldd "$hammer" 2>&1 | jq -Rsc 'split("\n")|map(select(length>0))')
  ldd_sysbench=$(ldd "$sysbench" 2>&1 | jq -Rsc 'split("\n")|map(select(length>0))')
  [[ "${PGWORKBENCH_RUNNER_ENVIRONMENT:-}" == github-hosted ]] ||
    fail 'runner environment is not github-hosted'
  [[ -n "${PGWORKBENCH_IMAGE_OS:-}" && -n "${PGWORKBENCH_IMAGE_VERSION:-}" ]] ||
    fail 'hosted runner image identity is missing'
  [[ "${PGWORKBENCH_RUNNER_ARCH:-}" == X64 ]] || fail 'hosted runner architecture is not X64'
  jq -n \
    --arg runner_image "$PGWORKBENCH_IMAGE_OS/$PGWORKBENCH_IMAGE_VERSION" \
    --arg runner_arch "$PGWORKBENCH_RUNNER_ARCH" \
    --arg uname "$(uname -a)" \
    --arg os_release "$(tr '\n' ' ' </etc/os-release)" \
    --arg postgres_version "$("$bindir/postgres" --version)" \
    --arg java_version "$("$java" -version 2>&1 | tr '\n' ' ')" \
    --arg sysbench_version "$("$sysbench" --version)" \
    --argjson packages "$packages" \
    --argjson ldd_java "$ldd_java" --argjson ldd_hammer "$ldd_hammer" --argjson ldd_sysbench "$ldd_sysbench" \
    '{
      schema_version:"pgworkbench.external-driver-host/v1",
      artifact_type:"pgworkbench.external-driver-host",
      runner:{provider:"github-hosted",environment:"github-hosted",label:"ubuntu-24.04",image:$runner_image,architecture:$runner_arch},
      kernel:$uname,os_release:$os_release,postgresql:$postgres_version,java:$java_version,sysbench:$sysbench_version,
      packages:$packages,
      dynamic_dependencies:{java:$ldd_java,hammerdb:$ldd_hammer,sysbench:$ldd_sysbench},
      host_runtime_dependencies_attested:false,
      performance_qualified:false
    }' > "$STATE_DIR/evidence/host.json"
}

write_paths() {
  local java
  java=$(<"$STATE_DIR/benchbase-java.path")
  printf '%s\n' 'pgworkbench.hammerdb-v6-execute-only-template/v1' \
    > "$STATE_DIR/hammerdb-execute-only.marker"
  chmod 0400 "$STATE_DIR/hammerdb-execute-only.marker"
  {
    printf 'BENCHBASE_JAVA=%s\n' "$java"
    printf 'BENCHBASE_RUNTIME_ROOT=%s\n' "$STATE_DIR/runtimes/benchbase"
    printf 'BENCHBASE_JAR=%s\n' "$STATE_DIR/runtimes/benchbase/benchbase.jar"
    printf 'BENCHBASE_CONFIG=%s\n' "$REPO_ROOT/configs/benchmark-drivers/benchbase-33c0047-tpcc-release-smoke.xml"
    printf 'HAMMERDB_RUNTIME_ROOT=%s\n' "$STATE_DIR/runtimes/hammerdb"
    printf 'HAMMERDB_BINARY=%s\n' "$STATE_DIR/runtimes/hammerdb/hammerdbcli"
    printf 'HAMMERDB_TEMPLATE=%s\n' "$STATE_DIR/hammerdb-execute-only.marker"
    printf 'SYSBENCH_RUNTIME_ROOT=%s\n' "$STATE_DIR/runtimes/sysbench"
    printf 'SYSBENCH_BINARY=%s\n' "$STATE_DIR/runtimes/sysbench/bin/sysbench"
    printf 'SYSBENCH_SCRIPT=%s\n' "$STATE_DIR/runtimes/sysbench/share/sysbench/oltp_read_write.lua"
    printf 'PROVISIONING_ACQUISITIONS_JSON=%s\n' "$STATE_DIR/evidence/acquisitions.json"
    printf 'PROVISIONING_DATASETS_JSON=%s\n' "$STATE_DIR/evidence/datasets.json"
    printf 'PROVISIONING_HOST_JSON=%s\n' "$STATE_DIR/evidence/host.json"
    printf 'PROVISIONING_RUNTIME_SET_JSON=%s\n' "$STATE_DIR/evidence/runtime-set.json"
  } > "$STATE_DIR/paths.env"
  chmod 0600 "$STATE_DIR/paths.env"
}

provision() {
  validate_repo_root
  for command in curl jq python3 sha256sum tar unzip zipinfo make install grep dpkg-query ldd; do
    need_command "$command"
  done
  initialize_state
  validate_configs
  acquire_assets
  build_runtimes
  start_postgres
  prepare_datasets
  capture_host
  write_paths
  printf 'PASS: hosted external-driver runtime and fresh PostgreSQL 16 datasets provisioned\n'
}

acquire_only() {
  for command in jq python3 sha256sum; do need_command "$command"; done
  initialize_state
  acquire_assets
  printf 'PASS: all external-driver acquisitions match exact pins and safe layouts\n'
}

cleanup() {
  validate_state_dir
  [[ -f "$STATE_DIR/.pgworkbench-external-driver-gate" && ! -L "$STATE_DIR/.pgworkbench-external-driver-gate" ]] ||
    fail 'refusing to clean an unmanaged state directory'
  [[ "$(<"$STATE_DIR/.pgworkbench-external-driver-gate")" == "$CONTRACT_VERSION" ]] ||
    fail 'refusing to clean a state directory with another contract version'
  if [[ -f "$STATE_DIR/postgres-bindir.path" && -d "$STATE_DIR/pgdata" ]]; then
    local bindir
    bindir=$(<"$STATE_DIR/postgres-bindir.path")
    if [[ "$bindir" == /usr/lib/postgresql/16/bin && -x "$bindir/pg_ctl" ]]; then
      "$bindir/pg_ctl" -D "$STATE_DIR/pgdata" -m immediate -w stop >/dev/null 2>&1 || true
    fi
  fi
  rm -rf -- "$STATE_DIR"
  printf 'PASS: hosted external-driver state removed\n'
}

main() {
  [[ $# -ge 1 ]] || usage
  local command=$1
  shift
  case "$command" in
    pins)
      [[ $# -eq 0 ]] || usage
      write_default_pins
      ;;
    validate-configs)
      parse_args "$@"
      [[ -n "$REPO_ROOT" && -z "$STATE_DIR" ]] || usage
      for dependency in jq python3 grep; do need_command "$dependency"; done
      validate_configs
      printf 'PASS: release-only external-driver configurations are closed and bounded\n'
      ;;
    provision)
      parse_args "$@"
      [[ -n "$STATE_DIR" && -n "$REPO_ROOT" ]] || usage
      provision
      ;;
    acquire-only)
      parse_args "$@"
      [[ -n "$STATE_DIR" && -z "$REPO_ROOT" ]] || usage
      acquire_only
      ;;
    cleanup)
      parse_args "$@"
      [[ -n "$STATE_DIR" && -z "$REPO_ROOT" ]] || usage
      cleanup
      ;;
    *) usage ;;
  esac
}

main "$@"
